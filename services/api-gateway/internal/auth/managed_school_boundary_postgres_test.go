package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func managedSchoolPostgresFixture(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	if dsn := strings.TrimSpace(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")); dsn != "" {
		return authScopePostgresFixtureDSN(t, dsn)
	}
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL or YUEJUAN_BINDING_TEST_DATABASE_URL is required for PostgreSQL account management regression")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	databaseName := "auth_management_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedName := pgx.Identifier{databaseName}.Sanitize()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+quotedName); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanupCtx, "DROP DATABASE "+quotedName+" WITH (FORCE)"); err != nil {
			t.Errorf("remove isolated account management database: %v", err)
		}
		admin.Close()
	})
	ownerConfig := config.Copy()
	ownerConfig.Database = databaseName
	owner := stdlib.OpenDB(*ownerConfig)
	defer owner.Close()
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("find account management migrations: %v (count=%d)", err, len(paths))
	}
	slices.Sort(paths)
	// 每个历史迁移单独提交，保持正式迁移的锁与审计时间边界；只写入本次随机隔离库。
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := owner.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, string(raw)); err != nil {
			tx.Rollback()
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	return authScopePostgresFixtureConfig(t, ownerConfig)
}

func TestPostgresManagedUserMutationsRequireManagedSchoolForMixedRoleActor(t *testing.T) {
	ctx, db := managedSchoolPostgresFixture(t)
	for _, table := range []string{"tenant", "school", "auth_activation", "auth_recovery"} {
		execAuthScopeFixture(t, ctx, db, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	tenant, schoolA, schoolB, classB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	actor := User{ID: uuid.NewString(), TenantID: tenant, TenantCode: "synthetic", Roles: []string{"school_admin", "teacher"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school"},
		"teacher":      map[string]any{"scope": "class"},
	}}
	execAuthScopeFixture(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic tenant','synthetic','active')`, tenant)
	for _, school := range []string{schoolA, schoolB} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2,'Synthetic school',$1::text,'active')`, school, tenant)
	}
	execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1,$2,$3,'synthetic-manager','Synthetic manager','unused','active')`, actor.ID, tenant, schoolA)
	execAuthScopeFixture(t, ctx, db, `INSERT INTO school_class(id,tenant_id,school_id,grade_id,academic_year_id,grade_cohort_id,name,code,status) VALUES($1,$2,$3,$4,$5,$6,'Synthetic class','synthetic','active')`, classB, tenant, schoolB, uuid.NewString(), uuid.NewString(), uuid.NewString())
	execAuthScopeFixture(t, ctx, db, `INSERT INTO teacher_class(tenant_id,teacher_id,class_id) VALUES($1,$2,$3)`, tenant, actor.ID, classB)
	roles := map[string]string{"teacher": uuid.NewString(), "grader": uuid.NewString()}
	for code, id := range roles {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, id, tenant, code, RolePolicy(code).CanonicalScope)
	}
	store := NewPostgresStore(db)
	// 历史跨校教师绑定保留在夹具中，确认它不再扩大管理员的实际管理范围。
	scope, err := store.ResolveAccessScope(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if scope.AllowsSchool(schoolB) || scope.managesSchool(schoolB) || !scope.managesSchool(schoolA) {
		t.Fatalf("unexpected actual mixed school scope: %#v", scope)
	}
	for _, operation := range []string{"create", "activation", "recovery", "status", "profile", "access"} {
		for _, school := range []string{schoolA, schoolB} {
			t.Run(operation+"/"+map[bool]string{true: "managed", false: "teaching"}[school == schoolA], func(t *testing.T) {
				target, token := uuid.NewString(), HashToken(uuid.NewString())
				status := "active"
				if operation == "activation" {
					status = "invited"
				}
				execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,teaching_subjects) VALUES($1::uuid,$2,$3,'target-' || $1::text,'Original','old-hash',$4,'{mathematics}')`, target, tenant, school, status)
				execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenant, target, roles["teacher"])
				execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_activation(tenant_id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, tenant, target, HashToken("activation-"+target))
				execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_recovery(tenant_id,user_id,token_hash,created_by,expires_at) VALUES($1,$2,$3,$4,now()+interval '1 hour')`, tenant, target, HashToken("recovery-"+target), actor.ID)
				before := managedSchoolDatabaseSnapshot(t, ctx, db)
				err := runManagedSchoolMutation(ctx, store, actor, scope, operation, target, school, token)
				if school == schoolA {
					if err != nil {
						t.Fatalf("managed school %s failed: %v", operation, err)
					}
					return
				}
				if !errors.Is(err, ErrOrganizationScope) {
					t.Fatalf("foreign teaching school %s error=%v", operation, err)
				}
				if after := managedSchoolDatabaseSnapshot(t, ctx, db); after != before {
					t.Fatal("rejected management changed accounts, authorization, or tokens")
				}
			})
		}
	}
	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users?q=target", nil)
		req = req.WithContext(WithAccessScope(WithUser(ctx, actor), scope))
		rec := httptest.NewRecorder()
		NewHandler(store, time.Hour).ListManagedUsers(rec, req)
		var response struct {
			Users []ManagedUser `json:"users"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Users) == 0 {
			t.Fatalf("list failed: status=%d body=%s", rec.Code, rec.Body.String())
		}
		for _, user := range response.Users {
			if user.SchoolID != schoolA {
				t.Fatalf("foreign teaching school exposed account: %+v", user)
			}
		}
	})
}

func managedSchoolDatabaseSnapshot(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	var result strings.Builder
	for _, table := range []string{"app_user", "user_role", "teacher_class", "auth_activation", "auth_recovery", "auth_session", "auth_trusted_device"} {
		var data string
		if err := db.QueryRowContext(ctx, "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY id),'[]'::jsonb)::text FROM "+table+" t").Scan(&data); err != nil {
			t.Fatal(err)
		}
		result.WriteString(data)
	}
	return result.String()
}

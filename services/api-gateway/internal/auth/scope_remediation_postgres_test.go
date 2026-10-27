package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func authScopePostgresFixture(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not set; requires a migrated isolated test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "auth_scope_fix_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		cancel()
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		cleanupCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove isolated auth schema: %v", err)
		}
		admin.Close()
		cancel()
	})
	// 复制正式迁移后的列、默认值、检查约束与主键/唯一索引；不复制外键和业务触发器，避免触及 public 的业务记录。
	// 本测试验证真实 SQL 的范围解析与撤销事务，跨模块数据一致性另由既有迁移/E2E 门禁保护。
	for _, table := range []string{"app_user", "role", "user_role", "teacher_class", "grade", "school_class", "exam", "exam_class", "review_task", "arbitration_task", "submission", "auth_session", "auth_trusted_device", "audit_log"} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)"); err != nil {
			t.Fatalf("clone table %s: %v", table, err)
		}
	}
	return ctx, db
}

func execAuthScopeFixture(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresSchoolManagerAssignedForeignExamKeepsSubmissionIsolation(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	tenant, actorID, schoolA, schoolB, examA, examB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1,$2,$3,'synthetic-manager','Synthetic manager','unused','active')`, actorID, tenant, schoolA)
	for _, item := range []struct{ id, school string }{{examA, schoolA}, {examB, schoolB}} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic scope','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$4)`, item.id, tenant, item.school, actorID)
	}
	own, assigned, sibling, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, item := range []struct{ id, exam string }{{own, examA}, {assigned, examB}, {sibling, examB}} {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,source_type,status,collected_by) VALUES($1,$2,$3,'manual_import','created',$4)`, item.id, tenant, item.exam, actorID)
	}
	execAuthScopeFixture(t, ctx, db, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by) VALUES($1,$2,$3,$4,'1',$5,$6,'synthetic','manual_sample','assigned',$7,$7)`, taskID, tenant, examB, uuid.NewString(), uuid.NewString(), assigned, actorID)
	user := User{ID: actorID, TenantID: tenant, Roles: []string{"school_admin", "grader"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school"}, "grader": map[string]any{"scope": "exam_task"}}}
	store := NewPostgresStore(db)
	scope, err := store.ResolveAccessScope(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.managesSchool(schoolA) || scope.managesSchool(schoolB) || !scope.AllowsExam(examB) {
		t.Fatalf("unexpected actual scope: %#v", scope)
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{{own, true}, {assigned, true}, {sibling, false}} {
		boundary, err := store.ResolveResourceBoundary(ctx, scope, "submission", tc.id)
		if err != nil {
			t.Fatal(err)
		}
		if got := AllowsResourceBoundary(scope, boundary); got != tc.want {
			t.Fatalf("actual submission boundary %s allowed=%v want=%v", tc.id, got, tc.want)
		}
	}
}

func TestPostgresManagedSessionRevokeEnforcesTargetsAndAtomicRevocation(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	tenant, schoolA, schoolB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	actor := User{ID: uuid.NewString(), TenantID: tenant, Roles: []string{"school_admin"}}
	scope := AccessScope{TenantID: tenant, ActorID: actor.ID, SchoolIDs: []string{schoolA, schoolB}, schoolWide: true, schoolManagementIDs: []string{schoolA}}
	store := NewPostgresStore(db)
	// 只让合成的故障设备在 UPDATE 时失败，以验证已执行的会话撤销也会回滚。
	execAuthScopeFixture(t, ctx, db, `CREATE FUNCTION fail_synthetic_device_revoke() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.token_hash=repeat('f',64) THEN RAISE EXCEPTION 'synthetic device write failure'; END IF; RETURN NEW; END $$`)
	execAuthScopeFixture(t, ctx, db, `CREATE TRIGGER synthetic_device_revoke BEFORE UPDATE ON auth_trusted_device FOR EACH ROW EXECUTE FUNCTION fail_synthetic_device_revoke()`)
	roles := map[string]string{"teacher": uuid.NewString(), "tenant_admin": uuid.NewString()}
	for code, id := range roles {
		execAuthScopeFixture(t, ctx, db, `INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1,$2,$3,$3,$4)`, id, tenant, code, RolePolicy(code).CanonicalScope)
	}
	for _, tc := range []struct {
		name, school, role string
		deviceFailure      bool
		want               error
	}{
		{"local teacher", schoolA, "teacher", false, nil},
		{"foreign teacher", schoolB, "teacher", false, ErrOrganizationScope},
		{"higher administrator", schoolA, "tenant_admin", false, ErrUserStatusForbidden},
		{"device write failure", schoolA, "teacher", true, errors.New("failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, roleID := uuid.NewString(), roles[tc.role]
			execAuthScopeFixture(t, ctx, db, `INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2,$3,$1::text,'Synthetic target','unused','active')`, target, tenant, tc.school)
			execAuthScopeFixture(t, ctx, db, `INSERT INTO user_role(tenant_id,user_id,role_id) VALUES($1,$2,$3)`, tenant, target, roleID)
			execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_session(tenant_id,user_id,token_hash,device_id,expires_at) VALUES($1,$2::uuid,$2::text,'synthetic',now()+interval '1 hour')`, tenant, target)
			deviceToken := HashToken(target)
			if tc.deviceFailure {
				deviceToken = strings.Repeat("f", 64)
			}
			execAuthScopeFixture(t, ctx, db, `INSERT INTO auth_trusted_device(tenant_id,user_id,token_hash,expires_at) VALUES($1,$2,$3,now()+interval '1 hour')`, tenant, target, deviceToken)
			count, err := store.RevokeManagedUserSessions(ctx, actor, scope, target, "synthetic administrator revoke")
			if tc.deviceFailure {
				if err == nil || !strings.Contains(err.Error(), "synthetic device write failure") {
					t.Fatalf("expected injected failure, got %v", err)
				}
			} else if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			var sessions, devices int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_session WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenant, target).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM auth_trusted_device WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL`, tenant, target).Scan(&devices); err != nil {
				t.Fatal(err)
			}
			if tc.want == nil && (count != 1 || sessions != 0 || devices != 0) {
				t.Fatalf("success count=%d active sessions=%d devices=%d", count, sessions, devices)
			}
			if tc.want != nil && (count != 0 || sessions != 1 || devices != 1) {
				t.Fatalf("failure changed credentials: count=%d sessions=%d devices=%d", count, sessions, devices)
			}
		})
	}
}

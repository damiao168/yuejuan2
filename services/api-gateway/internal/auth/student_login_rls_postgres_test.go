package auth

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/config"
	dbpool "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// 使用正式租户连接器和非特权登录角色；普通表克隆不会复制 RLS，单用 owner 连接会漏掉登录前读不到学生的问题。
func TestPostgresStudentLoginEstablishesTenantBeforeReadingProtectedStudent(t *testing.T) {
	ctx, owner := managedSchoolPostgresFixture(t)
	for _, table := range []string{"tenant", "school", "student", "permission", "role_permission"} {
		execAuthScopeFixture(t, ctx, owner, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	var schema, database string
	if err := owner.QueryRowContext(ctx, `SELECT current_schema(),current_database()`).Scan(&schema, &database); err != nil {
		t.Fatal(err)
	}
	tenant, school, student, account := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic login institution','login-rls-tenant','active')`, tenant)
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'Synthetic login school','LOGIN053','active')`, school, tenant)
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1,$2,$3,$4,'00123','Synthetic student','active')`, student, tenant, school, uuid.NewString())
	hash, err := HashPassword("SyntheticStudent123!")
	if err != nil {
		t.Fatal(err)
	}
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO app_user(id,tenant_id,school_id,student_id,username,display_name,password_hash,status,must_change_password) VALUES($1,$2,$3,$4,'student_internal','Synthetic student',$5,'active',true)`, account, tenant, school, student, hash)
	execAuthScopeFixture(t, ctx, owner, `ALTER TABLE student ENABLE ROW LEVEL SECURITY`)
	execAuthScopeFixture(t, ctx, owner, `CREATE POLICY login_student_tenant ON student USING (tenant_id::text=current_setting('edugrade.tenant_id',true))`)
	execAuthScopeFixture(t, ctx, owner, `GRANT USAGE ON SCHEMA `+pgx.Identifier{schema}.Sanitize()+` TO edugrade_tenant_runtime`)
	execAuthScopeFixture(t, ctx, owner, `GRANT SELECT ON ALL TABLES IN SCHEMA `+pgx.Identifier{schema}.Sanitize()+` TO edugrade_tenant_runtime`)
	role, password := "login_rls_"+strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
	quotedRole := pgx.Identifier{role}.Sanitize()
	execAuthScopeFixture(t, ctx, owner, `CREATE ROLE `+quotedRole+` LOGIN PASSWORD '`+password+`'`)
	t.Cleanup(func() {
		if _, err := owner.ExecContext(context.Background(), `DROP ROLE `+quotedRole); err != nil {
			t.Errorf("remove synthetic login role: %v", err)
		}
	})
	execAuthScopeFixture(t, ctx, owner, `GRANT edugrade_tenant_runtime TO `+quotedRole)
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL"))
	if dsn == "" {
		dsn = os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// pgx 的 ConnString 保留原始输入，不会反映修改后的 User/Database；显式生成夹具专用 URL。
	connection := url.URL{Scheme: "postgres", Host: net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))), User: url.UserPassword(role, password), Path: "/" + database}
	params := url.Values{"search_path": {schema + ",public"}}
	if cfg.TLSConfig == nil {
		params.Set("sslmode", "disable")
	}
	connection.RawQuery = params.Encode()
	runtime, closeRuntime, err := dbpool.OpenPostgres(config.PostgresConfig{DSN: connection.String(), TenantRLSEnabled: true, MaxOpenConns: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeRuntime() })
	var visible int
	if err := runtime.QueryRowContext(ctx, `SELECT count(*) FROM student`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("anonymous runtime must not read students: count=%d err=%v", visible, err)
	}
	store := NewPostgresStore(runtime)
	user, err := store.FindUserByLogin(ctx, "LOGIN053", "00123")
	if err != nil || user.ID != account || !user.MustChangePassword || !CheckPassword(user.PasswordHash, "SyntheticStudent123!") {
		t.Fatalf("student school login failed under runtime RLS: user=%q err=%v", user.ID, err)
	}
	if _, err := store.FindUserByLogin(ctx, "login-rls-tenant", "00123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("tenant code must not replace school code for student number: %v", err)
	}
	if _, err := store.FindUserByLogin(ctx, "LOGIN053", "student_internal"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("internal student username became public login alias: %v", err)
	}
	// 同名学校跨租户时拒绝定位，不能由学生表的 RLS 可见性替调用方选择一个学校。
	otherTenant := uuid.NewString()
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Other synthetic institution','other-login-tenant','active')`, otherTenant)
	execAuthScopeFixture(t, ctx, owner, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'Other synthetic school','LOGIN053','active')`, uuid.NewString(), otherTenant)
	if _, err := store.FindUserByLogin(ctx, "LOGIN053", "00123"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ambiguous school code must fail closed: %v", err)
	}
}

package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/config"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// 回执与导出测试也必须使用真实合格的独立阅卷岗位；管理员身份不能代替同校学科教师的任务资格。
func e2eSeedPostgresCommandReviewers(t *testing.T, scope postgresWorkflowScope) (string, string) {
	t.Helper()
	seed := func(role string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := scope.DB.Exec(`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status,activated_at,teaching_subjects)
SELECT $1::uuid,$2::uuid,$3::uuid,$4,$4,source.password_hash,'active',now(),ARRAY['physics']::text[]
FROM app_user source WHERE source.tenant_id=$2::uuid AND source.id=$5::uuid`, id, scope.TenantID, scope.SchoolID, "story041_"+role+"_"+strings.ReplaceAll(id, "-", ""), scope.GraderID); err != nil {
			t.Fatalf("seed independent %s account: %v", role, err)
		}
		result, err := scope.DB.Exec(`INSERT INTO user_role(tenant_id,user_id,role_id,data_scope)
SELECT $1::uuid,$2::uuid,r.id,'{"scope":"exam_task"}'::jsonb FROM role r
WHERE r.tenant_id=$1::uuid AND r.code=$3 AND r.deleted_at IS NULL`, scope.TenantID, id, role)
		if err != nil {
			t.Fatalf("seed independent %s role: %v", role, err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatalf("independent %s role missing: count=%d err=%v", role, count, err)
		}
		return id
	}
	return seed("grader"), seed("arbitrator")
}

func e2eAssertHighRiskTenantRLS(t *testing.T, adminDB *sql.DB, dsn, tenantID, studentID string) {
	t.Helper()
	var privilegedLogin bool
	var adminDatabase string
	var runtimeCanReadStudent bool
	if err := adminDB.QueryRow(`
SELECT rolsuper OR rolbypassrls,
	   current_database(),
	   has_table_privilege('edugrade_tenant_runtime', 'public.student', 'SELECT')
FROM pg_roles
WHERE rolname=SESSION_USER
`).Scan(&privilegedLogin, &adminDatabase, &runtimeCanReadStudent); err != nil {
		t.Fatalf("inspect migration database identity: %v", err)
	}
	if !runtimeCanReadStudent {
		t.Fatalf("migration did not grant the tenant RLS runtime role access to public.student in %q", adminDatabase)
	}
	if privilegedLogin {
		e2eAssertPrivilegedTenantRLSLoginRejected(t, dsn)
	}

	roleName := "e2e_rls_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedRole := pgx.Identifier{roleName}.Sanitize()
	rolePassword := "Rls" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec(`CREATE ROLE ` + quotedRole + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '` + rolePassword + `'`); err != nil {
		t.Fatalf("create isolated RLS login: %v", err)
	}
	if _, err := adminDB.Exec(`GRANT edugrade_tenant_runtime TO ` + quotedRole); err != nil {
		t.Fatalf("grant isolated RLS runtime role: %v", err)
	}
	defer func() {
		_, _ = adminDB.Exec(`DROP ROLE IF EXISTS ` + quotedRole)
	}()
	parsedDSN, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse RLS test DSN: %v", err)
	}
	parsedDSN.Path = "/" + adminDatabase
	parsedDSN.User = url.UserPassword(roleName, rolePassword)
	scopedDB, closeScopedDB, err := database.OpenPostgres(config.PostgresConfig{
		DSN: parsedDSN.String(), TenantRLSEnabled: true,
		MaxOpenConns: 1, MaxIdleConns: 1, StatementTimeout: time.Minute, LockTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open tenant-scoped database: %v", err)
	}
	defer closeScopedDB()

	var currentRole, sessionRole, databaseName, searchPath string
	var hasSchemaUsage bool
	if err = scopedDB.QueryRowContext(context.Background(), `
SELECT current_user,
	   session_user,
	   current_database(),
       current_setting('search_path'),
	   has_schema_privilege(current_user, 'public', 'USAGE')
`).Scan(&currentRole, &sessionRole, &databaseName, &searchPath, &hasSchemaUsage); err != nil {
		t.Fatalf("inspect tenant RLS runtime privileges: %v", err)
	}
	var protectedTable sql.NullString
	if err = scopedDB.QueryRowContext(context.Background(), `SELECT to_regclass('public.student')::text`).Scan(&protectedTable); err != nil {
		t.Fatalf("resolve tenant RLS protected table: %v", err)
	}
	if currentRole != "edugrade_tenant_runtime" || !hasSchemaUsage || !protectedTable.Valid {
		var catalogTable string
		_ = scopedDB.QueryRowContext(context.Background(), `
SELECT coalesce(max(n.nspname || '.' || c.relname), '')
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='public' AND c.relname='student'
`).Scan(&catalogTable)
		t.Fatalf("tenant RLS runtime role is not ready: role=%q session_role=%q admin_database=%q database=%q search_path=%q schema_usage=%t protected_table=%q catalog_table=%q", currentRole, sessionRole, adminDatabase, databaseName, searchPath, hasSchemaUsage, protectedTable.String, catalogTable)
	}

	e2eAssertStudentTenantRLSScopes(t, scopedDB, tenantID, studentID, currentRole, searchPath)
	e2eAssertLoginFactTenantRLSScopes(t, scopedDB, tenantID, studentID)
}

// 即使显式启用 RLS，超级用户或 BYPASSRLS 登录也不能进入租户运行查询。
func e2eAssertPrivilegedTenantRLSLoginRejected(t *testing.T, dsn string) {
	t.Helper()
	unsafeDB, closeUnsafeDB, err := database.OpenPostgres(config.PostgresConfig{
		DSN: dsn, TenantRLSEnabled: true,
		MaxOpenConns: 1, MaxIdleConns: 1, StatementTimeout: time.Minute, LockTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("open privileged RLS database: %v", err)
	}
	var sentinel int
	err = unsafeDB.QueryRowContext(context.Background(), `SELECT 1`).Scan(&sentinel)
	_ = closeUnsafeDB()
	if err == nil || !strings.Contains(err.Error(), "non-superuser database login without BYPASSRLS") {
		t.Fatalf("tenant RLS accepted a privileged database login: sentinel=%d err=%v", sentinel, err)
	}
}

// 无租户与错误租户看不到学生；所属租户与显式维护上下文各能读到唯一记录。
func e2eAssertStudentTenantRLSScopes(t *testing.T, scopedDB *sql.DB, tenantID, studentID, currentRole, searchPath string) {
	t.Helper()
	var count int
	if err := scopedDB.QueryRowContext(context.Background(), `SELECT count(*) FROM student WHERE id=$1::uuid`, studentID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unscoped context did not fail closed: role=%q search_path=%q count=%d err=%v", currentRole, searchPath, count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), uuid.NewString()), `SELECT count(*) FROM student WHERE id=$1::uuid`, studentID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("RLS exposed a student to another tenant: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), tenantID), `SELECT count(*) FROM student WHERE id=$1::uuid`, studentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("RLS hid a student from its tenant: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenantMaintenance(context.Background()), `SELECT count(*) FROM student WHERE id=$1::uuid`, studentID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("explicit maintenance scope could not inspect a protected row: count=%d err=%v", count, err)
	}

}

// 先通过真实认证路由写入风险和设备事实，再验证两种事实都遵守租户隔离。
func e2eAssertLoginFactTenantRLSScopes(t *testing.T, scopedDB *sql.DB, tenantID, studentID string) {
	t.Helper()
	var count int
	runtimeRouter := e2ePostgresRouter(scopedDB)
	runtimeToken := e2eLoginWithTenant(t, runtimeRouter, "demo", "tenant_admin", "ChangeMe123!")
	_, runtimeDeviceCookie := e2eBrowserLogin(t, runtimeRouter, "demo", "tenant_admin", "ChangeMe123!", nil)
	if runtimeDeviceCookie == nil {
		t.Fatal("tenant-scoped browser login did not persist a device binding")
	}
	runtimeStudents := e2eGetJSON(t, runtimeRouter, "/api/v1/students?ids="+studentID, runtimeToken, http.StatusOK)["students"].([]any)
	if len(runtimeStudents) != 1 || e2eString(t, runtimeStudents[0].(map[string]any), "id") != studentID {
		t.Fatalf("authenticated API tenant context did not expose the owning tenant's student: %#v", runtimeStudents)
	}
	if err := scopedDB.QueryRowContext(context.Background(), `SELECT count(*) FROM auth_risk_event`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unscoped context exposed login risk events: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), uuid.NewString()), `SELECT count(*) FROM auth_risk_event`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("RLS exposed login risk events to another tenant: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), tenantID), `SELECT count(*) FROM auth_risk_event`).Scan(&count); err != nil || count == 0 {
		t.Fatalf("RLS hid login risk events from their tenant: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(context.Background(), `SELECT count(*) FROM auth_trusted_device`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unscoped context exposed device bindings: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), uuid.NewString()), `SELECT count(*) FROM auth_trusted_device`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("RLS exposed device bindings to another tenant: count=%d err=%v", count, err)
	}
	if err := scopedDB.QueryRowContext(database.WithTenant(context.Background(), tenantID), `SELECT count(*) FROM auth_trusted_device`).Scan(&count); err != nil || count == 0 {
		t.Fatalf("RLS hid device bindings from their tenant: count=%d err=%v", count, err)
	}
}

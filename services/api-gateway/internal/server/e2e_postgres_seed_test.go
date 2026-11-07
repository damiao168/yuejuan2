package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"net/http"
	"testing"
	"time"
)

func e2eLookupUserID(t *testing.T, db *sql.DB, tenantCode string, username string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(`
SELECT u.id::text
FROM app_user u
JOIN tenant t ON t.id = u.tenant_id
WHERE t.code = $1 AND u.username = $2 AND u.deleted_at IS NULL
`, tenantCode, username).Scan(&id); err != nil {
		t.Fatalf("lookup user %s/%s: %v", tenantCode, username, err)
	}
	return id
}

// 高层流程使用公开创建出来的真实学生账号，首次改密通过同一个 HTTP 入口完成，不能直接清数据库标记。
func e2eLoginProvisionedStudent(t *testing.T, router http.Handler, schoolCode, studentNo, initialPassword string) string {
	t.Helper()
	initialToken := e2eLoginWithTenant(t, router, schoolCode, studentNo, initialPassword)
	user := e2eGetJSON(t, router, "/api/v1/auth/me", initialToken, http.StatusOK)["user"].(map[string]any)
	if user["must_change_password"] != true {
		t.Fatalf("new student account did not require first password change: %#v", user)
	}
	newPassword := "ChangedStudent123!"
	body := story056JSON(t, map[string]string{"current_password": initialPassword, "new_password": newPassword})
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/password", initialToken, body, http.StatusOK)
	e2eExpectStatus(t, router, http.MethodGet, "/api/v1/auth/me", initialToken, "", http.StatusUnauthorized)
	token := e2eLoginWithTenant(t, router, schoolCode, studentNo, newPassword)
	user = e2eGetJSON(t, router, "/api/v1/auth/me", token, http.StatusOK)["user"].(map[string]any)
	if user["must_change_password"] != false {
		t.Fatalf("changed student password still blocked business access: %#v", user)
	}
	return token
}

func e2eActivatePostgresDemoUsers(t *testing.T, db *sql.DB, usernames []string) {
	t.Helper()
	e2eActivatePostgresUsers(t, db, "demo", usernames)
}

func e2eBindPostgresSchoolAdmin(t *testing.T, db *sql.DB, username, schoolID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin PostgreSQL school administrator binding: %v", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE app_user user_account
SET school_id = $2::uuid, updated_at = now()
FROM tenant
WHERE tenant.id = user_account.tenant_id
  AND tenant.code = 'demo'
  AND user_account.username = $1
  AND user_account.deleted_at IS NULL
`, username, schoolID)
	if err != nil {
		t.Fatalf("bind PostgreSQL school administrator to school: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("expected one PostgreSQL school administrator school binding, affected=%d err=%v", affected, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_role (tenant_id, user_id, role_id, data_scope, deleted_at)
SELECT user_account.tenant_id,
       user_account.id,
       role.id,
       jsonb_build_object('scope', 'school', 'school_id', $2::text),
       NULL
FROM app_user user_account
JOIN tenant ON tenant.id = user_account.tenant_id
JOIN role ON role.tenant_id = user_account.tenant_id
WHERE tenant.code = 'demo'
  AND user_account.username = $1
  AND user_account.deleted_at IS NULL
  AND role.code = 'school_admin'
  AND role.deleted_at IS NULL
ON CONFLICT (tenant_id, user_id, role_id) DO UPDATE
SET data_scope = EXCLUDED.data_scope, deleted_at = NULL, updated_at = now()
`, username, schoolID); err != nil {
		t.Fatalf("bind PostgreSQL school administrator role scope: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit PostgreSQL school administrator binding: %v", err)
	}
}

func e2eActivatePostgresUsers(t *testing.T, db *sql.DB, tenantCode string, usernames []string) {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash synthetic demo user password: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, username := range usernames {
		result, err := db.ExecContext(ctx, `
UPDATE app_user u
SET password_hash = $2, status = 'active', updated_at = now()
FROM tenant t
WHERE t.id = u.tenant_id
  AND t.code = $1
  AND u.username = $3
  AND u.deleted_at IS NULL
`, tenantCode, hash, username)
		if err != nil {
			t.Fatalf("activate PostgreSQL user %s/%s: %v", tenantCode, username, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			t.Fatalf("check activated PostgreSQL demo user %s: %v", username, err)
		}
		if affected != 1 {
			t.Fatalf("expected to activate one PostgreSQL user %s/%s, affected %d", tenantCode, username, affected)
		}
	}
}

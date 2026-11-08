package server

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func TestRetiredAuthenticatorEndpointsAreNotRegistered(t *testing.T) {
	cfg := config.Config{Service: config.ServiceConfig{Name: "test", Environment: "test"}, Auth: config.AuthConfig{SessionTTL: time.Hour}}
	router := NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), nil)
	assertRetiredAuthenticatorEndpoints(t, router)
}

func assertRetiredAuthenticatorEndpoints(t *testing.T, router http.Handler) {
	t.Helper()
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/auth/mfa"},
		{http.MethodPost, "/api/v1/auth/mfa/totp/enroll"},
		{http.MethodPost, "/api/v1/auth/mfa/totp/confirm"},
		{http.MethodPost, "/api/v1/auth/mfa/totp/disable"},
		{http.MethodPost, "/api/v1/auth/mfa/recovery-codes/rotate"},
		{http.MethodPost, "/api/v1/auth/step-up/start"},
		{http.MethodPost, "/api/v1/auth/step-up/verify"},
	} {
		request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Errorf("retired endpoint %s %s returned %d, want 404", endpoint.method, endpoint.path, response.Code)
		}
	}
}

// 用旧版结构创建已绑定账号，再执行正式退役迁移，验证密钥清理不会破坏密码登录、会话或学校汇总。
func TestAuthenticatorRetirementWithPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	database := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, database, "000219_school_calibration_templates.sql")
	e2eActivatePostgresDemoUsers(t, database, []string{"teacher"})
	var tenantID, userID string
	if err := database.QueryRow(`SELECT tenant_id::text,id::text FROM app_user WHERE username='teacher' AND deleted_at IS NULL`).Scan(&tenantID, &userID); err != nil {
		t.Fatal(err)
	}
	credentialID, eventID := uuid.NewString(), uuid.NewString()
	if _, err := database.Exec(`INSERT INTO auth_totp(id,tenant_id,user_id,secret_ciphertext,secret_nonce,enrollment_session_hash,security_epoch,pending_expires_at,enabled_at)
SELECT $1::uuid,tenant_id,id,decode(repeat('01',16),'hex'),decode(repeat('02',12),'hex'),repeat('a',64),security_epoch,now()+interval '5 minutes',now() FROM app_user WHERE id=$2::uuid`, credentialID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO auth_mfa_recovery_code(tenant_id,user_id,credential_id,code_hash) VALUES($1::uuid,$2::uuid,$3::uuid,repeat('b',64));
`, tenantID, userID, credentialID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO auth_mfa_challenge(token_hash,tenant_id,user_id,credential_id,session_hash,security_epoch,operation,expires_at)
SELECT repeat('c',64),tenant_id,id,$1::uuid,repeat('d',64),security_epoch,'mfa.disable',now()+interval '5 minutes' FROM app_user WHERE id=$2::uuid`, credentialID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO event_outbox(id,tenant_id,aggregate_type,aggregate_id,event_type,payload) VALUES($1::uuid,$2::uuid,'auth_mfa',$3::uuid,'auth.mfa_enabled','{}')`, eventID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO auth_security_notification_intent(event_id,tenant_id,user_id,created_at) VALUES($1::uuid,$2::uuid,$3::uuid,now())`, eventID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO audit_log(tenant_id,actor_id,action,target_type,target_id) VALUES($1::uuid,$2::uuid,'auth.mfa_enabled','user',$2::uuid)`, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	store := auth.NewPostgresStore(database)
	session, err := store.CreateSession(context.Background(), auth.CreateSessionInput{
		TenantID: tenantID, UserID: userID, TokenHash: auth.HashToken("retirement-existing-session"), SessionType: auth.SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	e2eApplyPostgresMigrations(t, database)
	for _, table := range []string{"auth_totp", "auth_mfa_recovery_code", "auth_mfa_challenge", "auth_security_notification_intent"} {
		var name sql.NullString
		if err := database.QueryRow(`SELECT to_regclass($1)::text`, table).Scan(&name); err != nil || name.Valid {
			t.Fatalf("retired table %s still exists: %v, err=%v", table, name, err)
		}
	}
	var count int
	for _, query := range []string{
		`SELECT count(*) FROM event_outbox WHERE id=$1::uuid`,
		`SELECT count(*) FROM audit_log WHERE target_id=$1::uuid AND action='auth.mfa_enabled'`,
		`SELECT count(*) FROM auth_session WHERE id=$1::uuid AND revoke_reason='review_teacher_scope_changed'`,
	} {
		id := eventID
		if strings.Contains(query, "audit_log") {
			// 新审计表启用租户 RLS。用事务级租户设置核对历史行，避免将不可见误判为被删除。
			tx, err := database.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(t.Context(), `SELECT set_config('edugrade.tenant_id',$1,true)`, tenantID); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRowContext(t.Context(), query, userID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("retirement removed existing audit fact: count=%d err=%v", count, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if strings.Contains(query, "auth_session") {
			// 后续学科权限迁移独立撤销教师会话，但应保留原有会话记录。
			id = session.ID
		}
		if err := database.QueryRow(query, id).Scan(&count); err != nil || count != 1 {
			t.Fatalf("retirement removed existing account/audit fact: count=%d err=%v", count, err)
		}
	}
	// 旧表删除后，账号更新触发的学校指标刷新仍须正常；不能留下运行时才报错的 SQL 引用。
	if _, err := database.Exec(`UPDATE app_user SET display_name=display_name WHERE id=$1::uuid`, userID); err != nil {
		t.Fatalf("school metrics refresh after retirement: %v", err)
	}
	router := e2ePostgresRouter(database)
	e2eLoginWithTenant(t, router, "demo", "teacher", "ChangeMe123!")
	assertRetiredAuthenticatorEndpoints(t, router)
}

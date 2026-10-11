package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp/totp"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/outbox"
)

func TestTOTPManagementE2EWithPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	adminDB := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, adminDB)
	e2eActivatePostgresDemoUsers(t, adminDB, []string{"teacher"})
	scopedDB, closeScoped := e2eMFARuntimeDB(t, adminDB, dsn)
	defer closeScoped()
	key := base64.StdEncoding.EncodeToString([]byte("01234567890123456789012345678901"))
	router := e2ePostgresRouter(scopedDB, config.AuthConfig{SessionTTL: time.Hour, MFAEnabled: true, MFAMasterKey: key, RiskMode: "shadow"})
	session, device := e2eBrowserLogin(t, router, "demo", "teacher", "ChangeMe123!", nil)
	if session == nil || device == nil {
		t.Fatal("personal browser session/binding missing")
	}
	token := session.Value
	otherToken := e2eLoginWithTenant(t, router, "demo", "teacher", "ChangeMe123!")
	fixture := e2eEnrollTOTPManagement(t, adminDB, scopedDB, router, mfaBrowserIdentity{Token: token, OtherToken: otherToken, Session: session})
	newCodes := e2eRotateRecoveryWithNotificationRollback(t, adminDB, router, fixture)
	e2eAssertMFATenantIsolation(t, scopedDB, fixture.TenantID)
	challenge := e2eFinishTOTPRecoveryLifecycle(t, adminDB, router, fixture, newCodes)
	e2eAssertMFAAuditSecretIsolation(t, adminDB, fixture, challenge, newCodes)
	e2eAssertMFASecurityNotificationFacts(t, adminDB, fixture.TenantID, fixture.UserID, fixture.Secret, challenge, fixture.OldCodes[0].(string), newCodes[0].(string))
	e2eAssertMFALogPublicationKeepsNotificationPending(t, scopedDB, fixture.TenantID)
}

type mfaBrowserIdentity struct {
	Token, OtherToken string
	Session           *http.Cookie
}

type mfaEnrollmentFixture struct {
	Token, OtherToken, Secret, TenantID, UserID string
	OldCodes                                    []any
	Context                                     context.Context
	Store                                       *auth.PostgresStore
}

// 先证明 cookie CSRF 与会话绑定，再启用凭据并检查数据库只保存密文。
func e2eEnrollTOTPManagement(t *testing.T, adminDB, scopedDB *sql.DB, router http.Handler, browser mfaBrowserIdentity) mfaEnrollmentFixture {
	t.Helper()
	token, otherToken, session := browser.Token, browser.OtherToken, browser.Session
	status := e2eGetJSON(t, router, "/api/v1/auth/mfa", token, http.StatusOK)
	if status["available"] != true || status["enabled"] != false {
		t.Fatal("pilot status incorrect")
	}
	// 全局针对环境 cookie 的 CSRF 防护也必须覆盖新的 MFA 端点。
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/totp/enroll", strings.NewReader(`{"password":"ChangeMe123!"}`))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(session)
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, request)
	if denied.Code != http.StatusForbidden {
		t.Fatal("MFA enrollment bypassed cookie CSRF protection")
	}
	enrollment := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/totp/enroll", token, `{"password":"ChangeMe123!"}`, http.StatusOK)
	secret := e2eString(t, enrollment, "secret")
	id := e2eString(t, enrollment, "enrollment_id")
	if !strings.HasPrefix(e2eString(t, enrollment, "qr_code_data_url"), "data:image/png;base64,") {
		t.Fatal("QR must be generated locally")
	}
	code, err := totp.GenerateCode(secret, time.Now().UTC().Add(-30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/totp/confirm", otherToken, `{"enrollment_id":"`+id+`","code":"`+code+`"}`, http.StatusUnauthorized)
	confirmed := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/totp/confirm", token, `{"enrollment_id":"`+id+`","code":"`+code+`"}`, http.StatusOK)
	oldCodes := confirmed["recovery_codes"].([]any)
	if len(oldCodes) != 10 {
		t.Fatal("expected ten one-time recovery codes")
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/totp/enroll", token, `{"password":"ChangeMe123!"}`, http.StatusConflict)
	var tenantID string
	if err := adminDB.QueryRow(`SELECT id::text FROM tenant WHERE code='demo'`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	userID := e2eLookupUserID(t, adminDB, "demo", "teacher")
	ctx := auth.WithUser(context.Background(), auth.User{TenantID: tenantID, ID: userID})
	store := auth.NewPostgresStore(scopedDB)
	record, err := store.FindTOTP(ctx, tenantID, userID)
	if err != nil || record.EnabledAt == nil || record.RecoveryCodesRemaining != 10 || strings.Contains(string(record.Ciphertext), secret) {
		t.Fatal("encrypted enabled credential not persisted")
	}
	return mfaEnrollmentFixture{Token: token, OtherToken: otherToken, Secret: secret, TenantID: tenantID, UserID: userID, OldCodes: oldCodes, Context: ctx, Store: store}
}

// 临时拒绝通知意图插入，恢复码和挑战均须回滚；移除故障后同一证明只允许成功一次。
func e2eRotateRecoveryWithNotificationRollback(t *testing.T, adminDB *sql.DB, router http.Handler, fixture mfaEnrollmentFixture) []any {
	t.Helper()
	token, otherToken, secret, tenantID, userID := fixture.Token, fixture.OtherToken, fixture.Secret, fixture.TenantID, fixture.UserID
	ctx, store := fixture.Context, fixture.Store
	rotation := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/start", token, `{"operation":"mfa.recovery.rotate","password":"ChangeMe123!"}`, http.StatusOK)
	challenge := e2eString(t, rotation, "challenge_id")
	command := `{"challenge_id":"` + challenge + `"}`
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/recovery-codes/rotate", token, command, http.StatusUnauthorized)
	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	verify := `{"challenge_id":"` + challenge + `","method":"totp","code":"` + code + `"}`
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/verify", otherToken, verify, http.StatusUnauthorized)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/verify", token, verify, http.StatusOK)
	var recoveryBeforeFailure string
	if err = adminDB.QueryRow(`SELECT string_agg(code_hash,',' ORDER BY code_hash) FROM auth_mfa_recovery_code WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL`, tenantID, userID).Scan(&recoveryBeforeFailure); err != nil {
		t.Fatal(err)
	}
	if _, err = adminDB.Exec(`CREATE FUNCTION e2e_reject_mfa_notification_intent() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic notification intent failure'; END $$;
CREATE TRIGGER e2e_reject_mfa_notification_intent BEFORE INSERT ON auth_security_notification_intent FOR EACH ROW EXECUTE FUNCTION e2e_reject_mfa_notification_intent()`); err != nil {
		t.Fatal(err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/recovery-codes/rotate", token, command, http.StatusServiceUnavailable)
	var recoveryAfterFailure string
	var challengeConsumed bool
	if err = adminDB.QueryRow(`SELECT string_agg(code_hash,',' ORDER BY code_hash) FROM auth_mfa_recovery_code WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL`, tenantID, userID).Scan(&recoveryAfterFailure); err != nil {
		t.Fatal(err)
	}
	if err = adminDB.QueryRow(`SELECT consumed_at IS NOT NULL FROM auth_mfa_challenge WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND token_hash=$3`, tenantID, userID, auth.HashToken(challenge)).Scan(&challengeConsumed); err != nil {
		t.Fatal(err)
	}
	if recoveryAfterFailure != recoveryBeforeFailure || challengeConsumed {
		t.Fatal("MFA command committed without its notification intent")
	}
	if _, err = adminDB.Exec(`DROP TRIGGER e2e_reject_mfa_notification_intent ON auth_security_notification_intent; DROP FUNCTION e2e_reject_mfa_notification_intent()`); err != nil {
		t.Fatal(err)
	}
	rotated := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/recovery-codes/rotate", token, command, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/recovery-codes/rotate", token, command, http.StatusUnauthorized)
	newCodes := rotated["recovery_codes"].([]any)
	// 即使命令证明有效，也不提升全局认证保证级别或风险级别。
	live, err := store.FindUserBySession(ctx, auth.HashToken(token), time.Now().UTC())
	if err != nil || live.CurrentAuthLevel != 1 {
		t.Fatal("MFA command promoted whole session")
	}
	return newCodes
}

// 并发证明只能有一个赢家；禁用后两份会话、凭据和可信设备绑定都必须失效。
func e2eFinishTOTPRecoveryLifecycle(t *testing.T, adminDB *sql.DB, router http.Handler, fixture mfaEnrollmentFixture, newCodes []any) string {
	t.Helper()
	token, otherToken, tenantID, userID, oldCodes := fixture.Token, fixture.OtherToken, fixture.TenantID, fixture.UserID, fixture.OldCodes
	ctx, store := fixture.Context, fixture.Store
	// 无需等待时间步推进，即可测试事务重放与并发。
	record, err := store.FindTOTP(ctx, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	passwordHash, err := store.FindPasswordHash(ctx, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	_, concurrentHash, err := auth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bound := auth.MFAChallenge{TokenHash: concurrentHash, TenantID: tenantID, UserID: userID, CredentialID: record.ID, SessionHash: auth.HashToken(token), ExpectedPasswordHash: passwordHash, Operation: auth.MFAOperationRotateRecovery, ExpiresAt: now.Add(auth.MFAChallengeTTL)}
	if err = store.CreateMFAChallenge(ctx, bound, now); err != nil {
		t.Fatal(err)
	}
	proof := auth.MFAProof{TenantID: tenantID, UserID: userID, CredentialID: record.ID, SessionHash: bound.SessionHash, ChallengeHash: bound.TokenHash, Step: record.LastUsedStep + 1, Now: now}
	assertOneConcurrentCredentialWinner(t, auth.ErrMFAInvalid, func() error { return store.VerifyMFAChallenge(ctx, proof) })
	var hashes []string
	for _, value := range newCodes {
		hashes = append(hashes, auth.HashToken(strings.ReplaceAll(value.(string), "-", "")))
	}
	assertOneConcurrentCredentialWinner(t, auth.ErrMFAInvalid, func() error { return store.FinishMFACommand(ctx, proof, auth.MFAOperationRotateRecovery, hashes) })
	disable := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/start", token, `{"operation":"mfa.disable","password":"ChangeMe123!"}`, http.StatusOK)
	challenge := e2eString(t, disable, "challenge_id")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/verify", token, `{"challenge_id":"`+challenge+`","method":"recovery_code","code":"`+oldCodes[0].(string)+`"}`, http.StatusUnauthorized)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/step-up/verify", token, `{"challenge_id":"`+challenge+`","method":"recovery_code","code":"`+newCodes[0].(string)+`"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/mfa/totp/disable", token, `{"challenge_id":"`+challenge+`"}`, http.StatusOK)
	e2eGetJSON(t, router, "/api/v1/auth/me", token, http.StatusUnauthorized)
	e2eGetJSON(t, router, "/api/v1/auth/me", otherToken, http.StatusUnauthorized)
	for _, table := range []string{"auth_totp", "auth_mfa_recovery_code", "auth_mfa_challenge"} {
		var count int
		if err = adminDB.QueryRow(`SELECT count(*) FROM `+table+` WHERE tenant_id=$1::uuid AND user_id=$2::uuid`, tenantID, userID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("disabled credential state remains in %s: %d %v", table, count, err)
		}
	}
	var observed int
	if err = adminDB.QueryRow(`SELECT count(*) FROM auth_trusted_device WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, userID).Scan(&observed); err != nil || observed != 0 {
		t.Fatal("disable left device bindings active")
	}
	return challenge
}

// 审计逐条核对全部四种秘密，不能用数据库已加密替代审计载荷检查。
func e2eAssertMFAAuditSecretIsolation(t *testing.T, adminDB *sql.DB, fixture mfaEnrollmentFixture, challenge string, newCodes []any) {
	t.Helper()
	tenantID, userID, secret, oldCodes := fixture.TenantID, fixture.UserID, fixture.Secret, fixture.OldCodes
	// 秘密、挑战明文、OTP 或恢复码均不得进入审计 JSON。
	rows, err := adminDB.Query(`SELECT COALESCE(before_value,'{}'::jsonb)::text,COALESCE(after_value,'{}'::jsonb)::text FROM audit_log WHERE tenant_id=$1::uuid AND actor_id=$2::uuid`, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var before, after string
		if err := rows.Scan(&before, &after); err != nil {
			t.Fatal(err)
		}
		for _, sensitive := range []string{secret, challenge, oldCodes[0].(string), newCodes[0].(string)} {
			if strings.Contains(before+after, sensitive) {
				t.Fatal("MFA secret leaked into audit payload")
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func e2eAssertMFASecurityNotificationFacts(t *testing.T, db *sql.DB, tenantID, userID string, sensitive ...string) {
	t.Helper()
	rows, err := db.Query(`SELECT o.id::text,o.event_type,o.payload::text,i.status
FROM event_outbox o JOIN auth_security_notification_intent i ON i.tenant_id=o.tenant_id AND i.event_id=o.id
WHERE o.tenant_id=$1::uuid AND o.aggregate_type='auth_mfa' AND o.aggregate_id=$2::uuid
ORDER BY o.occurred_at,o.id`, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	eventIDs := map[string]bool{}
	total := 0
	for rows.Next() {
		var eventID, eventType, payload, status string
		if err = rows.Scan(&eventID, &eventType, &payload, &status); err != nil {
			t.Fatal(err)
		}
		if _, err = uuid.Parse(eventID); err != nil || eventIDs[eventID] || status != "awaiting_channel" {
			t.Fatal("invalid or duplicated pending notification fact")
		}
		eventIDs[eventID] = true
		counts[eventType]++
		total++
		for _, value := range append(sensitive, "secret_ciphertext", "code_hash", "session_hash", "token_hash", "user_agent", "ip_address") {
			if strings.Contains(payload, value) {
				t.Fatal("MFA secret or raw source metadata leaked into notification payload")
			}
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if total != 5 || counts["auth.mfa_enabled"] != 1 || counts["auth.mfa_recovery_rotated"] != 2 || counts["auth.mfa_recovery_used"] != 1 || counts["auth.mfa_disabled"] != 1 {
		t.Fatalf("MFA notification fact set is incomplete: total=%d counts=%v", total, counts)
	}
}

func e2eAssertMFATenantIsolation(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	for _, table := range []string{"auth_totp", "auth_mfa_recovery_code", "auth_mfa_challenge", "auth_security_notification_intent"} {
		e2eAssertMFACredentialTableScope(t, db, tenantID, table)
	}

	for _, scope := range []struct {
		ctx     context.Context
		visible bool
	}{{context.Background(), false}, {database.WithTenant(context.Background(), uuid.NewString()), false}, {database.WithTenant(context.Background(), tenantID), true}} {
		var count int
		if err := db.QueryRowContext(scope.ctx, `SELECT count(*) FROM event_outbox WHERE aggregate_type='auth_mfa'`).Scan(&count); err != nil || (count > 0) != scope.visible {
			t.Fatalf("MFA semantic outbox RLS isolation failed: count=%d err=%v", count, err)
		}
	}
	requestCtx := database.WithTenant(context.Background(), tenantID)
	if _, err := db.ExecContext(requestCtx, `UPDATE event_outbox SET event_type='auth.mfa_tampered' WHERE aggregate_type='auth_mfa'`); err == nil || !strings.Contains(err.Error(), "maintenance-only") {
		t.Fatalf("tenant runtime mutated an append-only MFA fact: %v", err)
	}
	var originalType string
	if err := db.QueryRowContext(requestCtx, `SELECT event_type FROM event_outbox WHERE aggregate_type='auth_mfa' ORDER BY occurred_at,id LIMIT 1`).Scan(&originalType); err != nil || originalType == "auth.mfa_tampered" {
		t.Fatalf("MFA fact changed after rejected mutation: event_type=%q err=%v", originalType, err)
	}
	if _, err := db.ExecContext(requestCtx, `UPDATE auth_security_notification_intent SET status='awaiting_channel'`); err == nil {
		t.Fatal("tenant runtime updated an append-only MFA notification intent")
	}
	result, err := db.ExecContext(database.WithTenantMaintenance(context.Background()), `UPDATE event_outbox SET next_attempt_at=next_attempt_at WHERE aggregate_type='auth_mfa'`)
	if err != nil {
		t.Fatalf("maintenance dispatcher update was blocked: %v", err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected == 0 {
		t.Fatalf("maintenance dispatcher did not reach MFA facts: affected=%d err=%v", affected, err)
	}
}

// 仅当前租户可见这四种 MFA 凭据或通知事实；没有租户与错误租户都必须读到空集。
func e2eAssertMFACredentialTableScope(t *testing.T, db *sql.DB, tenantID, table string) {
	t.Helper()
	for _, scope := range []struct {
		ctx     context.Context
		visible bool
	}{{context.Background(), false}, {database.WithTenant(context.Background(), uuid.NewString()), false}, {database.WithTenant(context.Background(), tenantID), true}} {
		var count int
		if err := db.QueryRowContext(scope.ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil || (count > 0) != scope.visible {
			t.Fatalf("MFA RLS isolation failed for %s: count=%d err=%v", table, count, err)
		}
	}
}

func e2eAssertMFALogPublicationKeepsNotificationPending(t *testing.T, db *sql.DB, tenantID string) {
	t.Helper()
	ctx := database.WithTenantMaintenance(context.Background())
	store := outbox.NewPostgresStore(db)
	events, err := store.Claim(ctx, "e2e-mfa-publication", 500, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var eventID string
	for _, event := range events {
		if event.AggregateType == "auth_mfa" && event.TenantID == tenantID {
			eventID = event.ID
			break
		}
	}
	if eventID == "" {
		t.Fatal("dispatcher did not claim an MFA security fact")
	}
	if err = store.MarkPublished(ctx, eventID, "e2e-mfa-publication"); err != nil {
		t.Fatal(err)
	}
	var status string
	err = db.QueryRowContext(database.WithTenant(context.Background(), tenantID), `SELECT i.status
FROM auth_security_notification_intent i JOIN event_outbox o ON o.tenant_id=i.tenant_id AND o.id=i.event_id
WHERE i.event_id=$1::uuid AND o.published_at IS NOT NULL`, eventID).Scan(&status)
	if err != nil || status != "awaiting_channel" {
		t.Fatalf("log publication acknowledged an independent notification intent: status=%q err=%v", status, err)
	}
}

func e2eMFARuntimeDB(t *testing.T, adminDB *sql.DB, dsn string) (*sql.DB, func()) {
	t.Helper()
	roleName := "e2e_mfa_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{roleName}.Sanitize()
	password := "Rls" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := adminDB.Exec(`CREATE ROLE ` + quoted + ` LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '` + password + `'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := adminDB.Exec(`DROP ROLE ` + quoted); err != nil {
			t.Errorf("drop isolated MFA runtime login: %v", err)
		}
	})
	if _, err := adminDB.Exec(`GRANT edugrade_tenant_runtime TO ` + quoted); err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := adminDB.QueryRow(`SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + databaseName
	parsed.User = url.UserPassword(roleName, password)
	db, closeDB, err := database.OpenPostgres(config.PostgresConfig{DSN: parsed.String(), TenantRLSEnabled: true, MaxOpenConns: 8, MaxIdleConns: 8, StatementTimeout: time.Minute, LockTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return db, func() {
		if err := closeDB(); err != nil {
			t.Error(err)
		}
	}
}

package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"

	"golang.org/x/crypto/bcrypt"
)

func TestTeacherAccountSecurityE2EWithPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping isolated PostgreSQL account security E2E")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrationsThrough(t, db, "000142_question_bank_metadata_acl_search.sql")
	// Exercise the actual upgrade with an ambiguous legacy phone and a unique one.
	if _, err := db.Exec(`
UPDATE app_user u SET phone=CASE u.username
  WHEN 'teacher' THEN '13800138000'
  WHEN 'grader' THEN '+86 13800138000'
  WHEN 'student' THEN '139-0013-9000' END
FROM tenant t WHERE t.id=u.tenant_id AND t.code='demo'
  AND u.username IN ('teacher', 'grader', 'student')`); err != nil {
		t.Fatal(err)
	}
	e2eApplyPostgresMigrations(t, db)
	var ambiguousBound int
	if err := db.QueryRow(`SELECT count(*) FROM app_user u JOIN tenant t ON t.id=u.tenant_id
WHERE t.code='demo' AND u.username IN ('teacher','grader') AND u.phone_normalized IS NOT NULL`).Scan(&ambiguousBound); err != nil || ambiguousBound != 0 {
		t.Fatalf("migration must not bind ambiguous phones: count=%d err=%v", ambiguousBound, err)
	}
	var uniquePhone string
	if err := db.QueryRow(`SELECT phone_normalized FROM app_user u JOIN tenant t ON t.id=u.tenant_id
WHERE t.code='demo' AND u.username='student'`).Scan(&uniquePhone); err != nil || uniquePhone != "+8613900139000" {
		t.Fatalf("migration must normalize an unambiguous legacy phone: phone=%q err=%v", uniquePhone, err)
	}
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "school_admin", "teacher"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	school := e2ePostJSON(t, router, http.MethodPost, "/api/v1/schools", adminToken,
		`{"name":"Account security test school","code":"auth-security"}`, http.StatusCreated)["school"].(map[string]any)
	schoolID := e2eString(t, school, "id")
	e2eBindPostgresSchoolAdmin(t, db, "school_admin", schoolID)
	schoolAdminToken := e2eLoginWithTenant(t, router, "demo", "school_admin", "ChangeMe123!")

	created := e2ePostJSON(t, router, http.MethodPost, "/api/v1/users", schoolAdminToken,
		`{"display_name":"Account security teacher","phone":"13700137000","employee_no":"AUTH-001","role_code":"teacher","school_id":"`+schoolID+`"}`, http.StatusCreated)
	user := created["user"].(map[string]any)
	userID := e2eString(t, user, "id")
	if user["status"] != "invited" || user["activated_at"] != nil || user["phone_masked"] != "137****7000" {
		t.Fatalf("new teacher must be invited with masked phone and no activation timestamp: %#v", user)
	}
	oldToken := e2eString(t, created["activation"].(map[string]any), "token")
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/users/"+userID+"/status", schoolAdminToken, `{"status":"active"}`, http.StatusForbidden)
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/users/"+userID+"/status", schoolAdminToken, `{"status":"disabled"}`, http.StatusForbidden)
	reissued := e2ePostJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/activation", schoolAdminToken, `{}`, http.StatusCreated)
	activationToken := e2eString(t, reissued["activation"].(map[string]any), "token")
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/activation/verify", "", `{"token":"`+oldToken+`"}`, http.StatusBadRequest)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/activation/verify", "", `{"token":"`+activationToken+`"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/activation/complete", "", `{"token":"`+activationToken+`","password":"TeacherPrivatePassphrase"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/activation/complete", "", `{"token":"`+activationToken+`","password":"TeacherPrivatePassphrase"}`, http.StatusBadRequest)
	phoneLogin := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/token", "",
		`{"tenant_hint":"demo","identifier":"+86 13700137000","password":"TeacherPrivatePassphrase","client_type":"desktop"}`, http.StatusOK)
	phoneSession := e2eString(t, phoneLogin, "access_token")
	employeeLogin := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/token", "",
		`{"tenant_hint":"demo","identifier":"AUTH-001","password":"TeacherPrivatePassphrase","client_type":"desktop"}`, http.StatusOK)
	employeeSession := e2eString(t, employeeLogin, "access_token")
	_, deviceCookie := e2eBrowserLogin(t, router, "demo", "AUTH-001", "TeacherPrivatePassphrase", nil)
	if deviceCookie == nil || deviceCookie.Value == "" || !deviceCookie.HttpOnly || deviceCookie.Path != "/api/v1/auth" {
		t.Fatalf("browser login must issue a scoped opaque device cookie: %#v", deviceCookie)
	}
	recognizedSession, _ := e2eBrowserLogin(t, router, "demo", "AUTH-001", "TeacherPrivatePassphrase", deviceCookie)
	if recognizedSession == nil || recognizedSession.Value == "" {
		t.Fatal("recognized browser login must create a session cookie")
	}
	e2eGetJSON(t, router, "/api/v1/auth/me", phoneSession, http.StatusOK)
	e2eGetJSON(t, router, "/api/v1/auth/sessions", phoneSession, http.StatusOK)
	e2eGetJSON(t, router, "/api/v1/exams", phoneSession, http.StatusForbidden)
	store := auth.NewPostgresStore(db)
	ctx := context.Background()
	oldUser, err := store.FindUserByLogin(ctx, "demo", "AUTH-001")
	if err != nil || oldUser.LastLoginAt.IsZero() || oldUser.ActivatedAt.IsZero() {
		t.Fatalf("activated teacher must have activation and last login timestamps: user=%#v err=%v", oldUser.User, err)
	}
	if _, err := store.LoadLoginRiskContext(auth.WithUser(ctx, oldUser.User), auth.LoginRiskContextRequest{
		TenantID: oldUser.TenantID, UserID: oldUser.ID, Now: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("load persisted login risk context: %v", err)
	}
	var method, risk, riskAction, riskPolicy, riskEvidence string
	var level, riskScore int
	var epoch int64
	if err := db.QueryRow(`SELECT auth_method, auth_level, risk_level, risk_action,
       risk_score, risk_policy_version, risk_evidence_quality, security_epoch
FROM auth_session WHERE token_hash=$1`, auth.HashToken(phoneSession)).Scan(
		&method, &level, &risk, &riskAction, &riskScore, &riskPolicy, &riskEvidence, &epoch,
	); err != nil || method != "password" || level != 1 || risk != "low" || riskAction != "allow" ||
		riskScore != 0 || riskPolicy != auth.RiskPolicyVersionV1 || riskEvidence != "cold_start" || epoch != oldUser.SecurityEpoch {
		t.Fatalf("session must persist its authentication and shadow risk metadata: method=%q level=%d risk=%q action=%q score=%d policy=%q evidence=%q epoch=%d err=%v",
			method, level, risk, riskAction, riskScore, riskPolicy, riskEvidence, epoch, err)
	}
	var riskEventCount int
	if err := db.QueryRow(`
SELECT count(*)
FROM auth_risk_event event
JOIN auth_session session ON session.id=event.session_id
WHERE session.token_hash=$1
  AND event.purpose='login'
  AND event.policy_version=$2
`, auth.HashToken(phoneSession), auth.RiskPolicyVersionV1).Scan(&riskEventCount); err != nil || riskEventCount != 1 {
		t.Fatalf("login must persist one versioned shadow risk event: count=%d err=%v", riskEventCount, err)
	}
	var storedDeviceHash, deviceTrustBasis string
	var deviceAssurance int
	var deviceTrustedAt *time.Time
	if err := db.QueryRow(`
SELECT token_hash, assurance_level, trust_basis, trusted_at
FROM auth_trusted_device
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, oldUser.TenantID, userID).Scan(&storedDeviceHash, &deviceAssurance, &deviceTrustBasis, &deviceTrustedAt); err != nil ||
		storedDeviceHash != auth.HashToken(deviceCookie.Value) || storedDeviceHash == deviceCookie.Value ||
		deviceAssurance != 1 || deviceTrustBasis != "password_observed" || deviceTrustedAt != nil {
		t.Fatalf("browser binding must store only an assurance-1 opaque hash: hash_matches=%t plaintext=%t assurance=%d basis=%q trusted_at=%v err=%v",
			storedDeviceHash == auth.HashToken(deviceCookie.Value), storedDeviceHash == deviceCookie.Value,
			deviceAssurance, deviceTrustBasis, deviceTrustedAt, err)
	}
	var recognized, trusted bool
	var recognizedSessionCount int
	if err := db.QueryRow(`SELECT count(*) FROM auth_session WHERE token_hash=$1`, auth.HashToken(recognizedSession.Value)).Scan(&recognizedSessionCount); err != nil || recognizedSessionCount != 1 {
		t.Fatalf("second browser login cookie must reference one persisted session: count=%d err=%v", recognizedSessionCount, err)
	}
	if err := db.QueryRow(`
SELECT device_recognized, device_trusted
FROM auth_risk_event
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND purpose='login'
ORDER BY occurred_at DESC, id DESC
LIMIT 1
`, oldUser.TenantID, userID).Scan(&recognized, &trusted); err != nil || !recognized || trusted {
		t.Fatalf("second browser login must recognize but never MFA-trust a password-observed device: recognized=%t trusted=%t err=%v", recognized, trusted, err)
	}
	publicToken := "public-computer-e2e-session"
	publicSession, err := store.CreateSession(ctx, auth.CreateSessionInput{
		TenantID: oldUser.TenantID, UserID: userID, TokenHash: auth.HashToken(publicToken),
		SessionType: auth.SessionTypePublicDevice, DeviceID: "public-browser",
		DeviceName: "School public browser", ExpiresAt: time.Now().UTC().Add(35 * time.Minute), SecurityEpoch: oldUser.SecurityEpoch,
	})
	if err != nil || publicSession.SessionType != auth.SessionTypePublicDevice {
		t.Fatalf("migration must accept and project public computer sessions: session=%#v err=%v", publicSession, err)
	}
	publicUser, err := store.FindUserBySession(ctx, auth.HashToken(publicToken), time.Now().UTC())
	if err != nil || publicUser.CurrentSessionType != auth.SessionTypePublicDevice {
		t.Fatalf("authenticated public session must retain its type: user=%#v err=%v", publicUser, err)
	}
	locked := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/lock", publicToken, `{}`, http.StatusOK)
	if locked["status"] != "locked" {
		t.Fatalf("public session lock response is incomplete: %#v", locked)
	}
	var lockedAt *time.Time
	if err := db.QueryRow(`SELECT locked_at FROM auth_session WHERE token_hash=$1`, auth.HashToken(publicToken)).Scan(&lockedAt); err != nil || lockedAt == nil {
		t.Fatalf("public session lock must be stored: at=%v err=%v", lockedAt, err)
	}
	e2eGetJSON(t, router, "/api/v1/auth/me", publicToken, http.StatusUnauthorized)
	reauthenticated := e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/reauthenticate", publicToken,
		`{"password":"TeacherPrivatePassphrase"}`, http.StatusOK)
	if reauthenticated["status"] != "reauthenticated" {
		t.Fatalf("public session reauthentication response is incomplete: %#v", reauthenticated)
	}
	var reauthenticatedAt, remainingLock *time.Time
	if err := db.QueryRow(`SELECT reauthenticated_at, locked_at FROM auth_session WHERE token_hash=$1`, auth.HashToken(publicToken)).Scan(&reauthenticatedAt, &remainingLock); err != nil || reauthenticatedAt == nil || remainingLock != nil {
		t.Fatalf("reauthentication must store its timestamp and clear the lock: at=%v lock=%v err=%v", reauthenticatedAt, remainingLock, err)
	}
	e2eGetJSON(t, router, "/api/v1/auth/me", publicToken, http.StatusOK)
	t.Run("in-flight verification cannot undo a newer public lock", func(t *testing.T) {
		startedAt := time.Now().UTC()
		lockedAt := startedAt.Add(time.Millisecond)
		if locked, err := store.LockSession(ctx, oldUser.TenantID, userID, auth.HashToken(publicToken), lockedAt); err != nil || !locked {
			t.Fatalf("public lock: locked=%v err=%v", locked, err)
		}
		if updated, err := store.MarkSessionReauthenticated(ctx, oldUser.TenantID, userID, auth.HashToken(publicToken), startedAt, lockedAt.Add(time.Millisecond)); err != nil || updated {
			t.Fatalf("in-flight verification undid a newer public lock: updated=%v err=%v", updated, err)
		}
		if updated, err := store.MarkSessionReauthenticated(ctx, oldUser.TenantID, userID, auth.HashToken(publicToken), lockedAt.Add(2*time.Millisecond), lockedAt.Add(3*time.Millisecond)); err != nil || !updated {
			t.Fatalf("verification after the lock: updated=%v err=%v", updated, err)
		}
	})
	if _, err := db.Exec(`UPDATE auth_session SET reauthenticated_at=now()-interval '2 hours' WHERE token_hash=$1`, auth.HashToken(schoolAdminToken)); err != nil {
		t.Fatal(err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/credential-reset", schoolAdminToken, `{}`, http.StatusPreconditionRequired)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/reauthenticate", schoolAdminToken, `{"password":"ChangeMe123!"}`, http.StatusOK)
	recovery := e2ePostJSON(t, router, http.MethodPost, "/api/v1/users/"+userID+"/credential-reset", schoolAdminToken, `{}`, http.StatusCreated)
	recoveryToken := e2eString(t, recovery["recovery"].(map[string]any), "token")
	var storedToken string
	if err := db.QueryRow(`SELECT token_hash FROM auth_recovery WHERE user_id=$1::uuid AND used_at IS NULL`, userID).Scan(&storedToken); err != nil || storedToken != auth.HashToken(recoveryToken) || storedToken == recoveryToken {
		t.Fatalf("recovery must persist only the token hash: err=%v", err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/recovery/verify", "", `{"token":"`+recoveryToken+`"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/recovery/complete", "", `{"token":"`+recoveryToken+`","password":"TeacherRecoveredPassphrase"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/auth/recovery/complete", "", `{"token":"`+recoveryToken+`","password":"TeacherRecoveredPassphrase"}`, http.StatusBadRequest)
	for _, token := range []string{phoneSession, employeeSession} {
		e2eGetJSON(t, router, "/api/v1/auth/me", token, http.StatusUnauthorized)
	}
	var activeDeviceBindings int
	if err := db.QueryRow(`SELECT count(*) FROM auth_trusted_device WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, oldUser.TenantID, userID).Scan(&activeDeviceBindings); err != nil || activeDeviceBindings != 0 {
		t.Fatalf("credential recovery must revoke browser device bindings: count=%d err=%v", activeDeviceBindings, err)
	}
	if _, err := store.CreateSession(ctx, auth.CreateSessionInput{
		TenantID: oldUser.TenantID, UserID: userID, TokenHash: auth.HashToken("stale-in-flight"),
		SessionType: auth.SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour), SecurityEpoch: oldUser.SecurityEpoch,
	}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("old epoch must not create a session after recovery: %v", err)
	}
	if err := store.RecordSuccessfulLogin(ctx, oldUser.TenantID, userID, oldUser.PasswordHash, ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("old verified password must fail after recovery: %v", err)
	}
	freshToken := e2eLoginWithTenant(t, router, "demo", e2eString(t, user, "username"), "TeacherRecoveredPassphrase")
	events := e2eGetJSON(t, router, "/api/v1/auth/security-events", freshToken, http.StatusOK)["events"].([]any)
	foundRecovery := false
	for _, raw := range events {
		event := raw.(map[string]any)
		if len(event) != 5 {
			t.Fatalf("self security event must contain only the five safe fields: %#v", event)
		}
		foundRecovery = foundRecovery || event["event_type"] == "auth.credential_recovery_completed"
	}
	if !foundRecovery {
		t.Fatalf("self security events must include credential recovery: %#v", events)
	}
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/users/"+userID+"/status", schoolAdminToken, `{"status":"disabled"}`, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPatch, "/api/v1/users/"+userID+"/status", schoolAdminToken, `{"status":"active"}`, http.StatusOK)
	e2eGetJSON(t, router, "/api/v1/auth/me", freshToken, http.StatusUnauthorized)

	t.Run("legacy bcrypt rehash", func(t *testing.T) {
		legacyHash, err := bcrypt.GenerateFromPassword([]byte("LegacyTeacherPassword!"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		teacherID := e2eLookupUserID(t, db, "demo", "teacher")
		if _, err := db.Exec(`UPDATE app_user SET password_hash=$2 WHERE id=$1::uuid`, teacherID, string(legacyHash)); err != nil {
			t.Fatal(err)
		}
		e2eLoginWithTenant(t, router, "demo", "teacher", "LegacyTeacherPassword!")
		teacher, err := store.FindUserByLogin(ctx, "demo", "teacher")
		if err != nil || !strings.HasPrefix(teacher.PasswordHash, "$argon2id$") {
			t.Fatalf("successful legacy login must upgrade the stored hash: %v", err)
		}
	})

	t.Run("concurrent one-time activation and recovery", func(t *testing.T) {
		actor, err := store.FindUserByLogin(ctx, "demo", "school_admin")
		if err != nil {
			t.Fatal(err)
		}
		scope := auth.AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, SchoolIDs: []string{schoolID}}
		now := time.Now().UTC()
		activationHash := auth.HashToken("concurrent-activation")
		invite, err := store.CreateManagedUser(ctx, actor.User, scope, auth.CreateManagedUserInput{
			Username: "concurrent-teacher", DisplayName: "Concurrent teacher", RoleCode: "teacher", SchoolID: schoolID,
			ActivationTokenHash: activationHash, ActivationExpiresAt: now.Add(time.Hour),
		}, "!activation-required")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.FindActivation(ctx, activationHash, now.Add(2*time.Hour)); !errors.Is(err, auth.ErrActivationInvalid) {
			t.Fatalf("expired activation must fail: %v", err)
		}
		assertOneConcurrentCredentialWinner(t, auth.ErrActivationInvalid, func() error {
			_, err := store.ActivateUser(ctx, activationHash, "new-test-hash", now)
			return err
		})
		recoveryHash := auth.HashToken("concurrent-recovery")
		if _, err := store.CreateRecovery(ctx, actor.User, scope, invite.ID, recoveryHash, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.FindRecovery(ctx, recoveryHash, now.Add(2*time.Hour)); !errors.Is(err, auth.ErrRecoveryInvalid) {
			t.Fatalf("expired recovery must fail: %v", err)
		}
		assertOneConcurrentCredentialWinner(t, auth.ErrRecoveryInvalid, func() error {
			_, err := store.CompleteRecovery(ctx, recoveryHash, "recovered-test-hash", now)
			return err
		})
		pending := auth.HashToken("pending-before-password-change")
		if _, err := store.CreateRecovery(ctx, actor.User, scope, invite.ID, pending, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if updated, _, err := store.UpdatePasswordAndRevokeSessions(ctx, actor.TenantID, invite.ID, "recovered-test-hash", "changed-test-hash"); err != nil || !updated {
			t.Fatalf("password change failed: updated=%v err=%v", updated, err)
		}
		if _, err := store.FindRecovery(ctx, pending, now); !errors.Is(err, auth.ErrRecoveryInvalid) {
			t.Fatalf("old recovery grant must fail after password change: %v", err)
		}
		if _, err := store.CompleteRecovery(ctx, pending, "unauthorized-test-hash", now); !errors.Is(err, auth.ErrRecoveryInvalid) {
			t.Fatalf("old recovery grant must not replace changed credentials: %v", err)
		}
		pending = auth.HashToken("pending-before-disable")
		if _, err := store.CreateRecovery(ctx, actor.User, scope, invite.ID, pending, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		for _, status := range []string{"disabled", "active"} {
			if _, _, err := store.UpdateManagedUserStatus(ctx, actor.User, scope, invite.ID, status); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := store.FindRecovery(ctx, pending, now); !errors.Is(err, auth.ErrRecoveryInvalid) {
			t.Fatalf("old recovery grant must fail after disabling and re-enabling: %v", err)
		}
		if _, err := store.CompleteRecovery(ctx, pending, "unauthorized-test-hash", now); !errors.Is(err, auth.ErrRecoveryInvalid) {
			t.Fatalf("old recovery grant must not replace re-enabled credentials: %v", err)
		}
	})
}

func e2eBrowserLogin(t *testing.T, router http.Handler, tenantCode, identifier, password string, deviceCookie *http.Cookie) (*http.Cookie, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(
		`{"tenant_code":"`+tenantCode+`","identifier":"`+identifier+`","password":"`+password+`"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "EduGrade-E2E-Browser/1.0")
	req.RemoteAddr = "203.0.113.41:54321"
	if deviceCookie != nil {
		req.AddCookie(deviceCookie)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("browser login %s expected 200, got %d %s", identifier, rec.Code, rec.Body.String())
	}
	var sessionCookie, returnedDeviceCookie *http.Cookie
	for _, cookie := range rec.Result().Cookies() {
		switch cookie.Name {
		case "edugrade_session":
			sessionCookie = cookie
		case "edugrade_device":
			returnedDeviceCookie = cookie
		}
	}
	return sessionCookie, returnedDeviceCookie
}

func assertOneConcurrentCredentialWinner(t *testing.T, expected error, consume func() error) {
	t.Helper()
	results := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() { results <- consume() })
	}
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, expected) {
			t.Fatalf("unexpected concurrent consumption error: %v", err)
		}
	}
	if winners != 1 {
		t.Fatalf("single-use credential token must have one concurrent winner, got %d", winners)
	}
}

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEvaluateLoginRiskDoesNotPunishColdStart(t *testing.T) {
	decision := EvaluateLoginRisk(LoginRiskContext{}, time.Now().UTC())
	if decision.Level != RiskLevelLow || decision.Action != RiskActionAllow || decision.Score != 0 {
		t.Fatalf("cold start should remain low-risk allow, got %#v", decision)
	}
	if decision.EvidenceQuality != RiskEvidenceColdStart {
		t.Fatalf("expected cold-start evidence, got %q", decision.EvidenceQuality)
	}
}

func TestEvaluateLoginRiskNetworkChangeAloneNeverChallenges(t *testing.T) {
	decision := EvaluateLoginRisk(LoginRiskContext{
		PriorSuccessfulLogins: 5,
		KnownDevice:           true,
		KnownUserAgent:        true,
		KnownNetwork:          false,
	}, time.Now().UTC())
	if decision.Level != RiskLevelLow || decision.Action != RiskActionAllow || decision.Score != 10 {
		t.Fatalf("network-only change should remain low-risk allow, got %#v", decision)
	}
}

func TestEvaluateLoginRiskUsesIndependentCappedFamilies(t *testing.T) {
	decision := EvaluateLoginRisk(LoginRiskContext{
		PriorSuccessfulLogins: 5,
		KnownDevice:           false,
		KnownUserAgent:        false,
		KnownNetwork:          false,
		RecentFailures:        100,
		RecentRecovery:        true,
	}, time.Now().UTC())
	if decision.FamilyScores["account_abuse"] != 40 || decision.FamilyScores["device_context"] != 20 || decision.FamilyScores["network_context"] != 10 {
		t.Fatalf("unexpected family caps: %#v", decision.FamilyScores)
	}
	if decision.Score != 100 || decision.Level != RiskLevelHigh || decision.Action != RiskActionStepUp || decision.RequiredAuthLevel != 2 {
		t.Fatalf("compound strong evidence should require step-up, got %#v", decision)
	}
}

func TestEvaluateLoginRiskNewEnvironmentIsRestrictedNotDenied(t *testing.T) {
	decision := EvaluateLoginRisk(LoginRiskContext{
		PriorSuccessfulLogins: 5,
		KnownDevice:           false,
		KnownUserAgent:        false,
		KnownNetwork:          false,
	}, time.Now().UTC())
	if decision.Score != 30 || decision.Level != RiskLevelMedium || decision.Action != RiskActionAllowRestricted {
		t.Fatalf("new environment should be observable without hard denial, got %#v", decision)
	}
}

func TestBrowserLoginCreatesOpaqueRecognizedDeviceBinding(t *testing.T) {
	store := newRiskTestStore(t)
	handler := NewHandler(store, time.Hour, HandlerOptions{RiskMode: "shadow"})

	first := riskLoginRequest(t, handler, `{"tenant_code":"demo","identifier":"teacher","password":"TeacherRiskPassphrase"}`, nil)
	deviceCookie := responseCookie(first.Result().Cookies(), "edugrade_device")
	if deviceCookie == nil || !deviceCookie.HttpOnly || deviceCookie.Path != "/api/v1/auth" || len(deviceCookie.Value) < 32 {
		t.Fatalf("missing secure opaque device binding cookie: %#v", deviceCookie)
	}

	second := riskLoginRequest(t, handler, `{"tenant_code":"demo","identifier":"teacher","password":"TeacherRiskPassphrase"}`, deviceCookie)
	if second.Code != http.StatusOK {
		t.Fatalf("second login failed: %d %s", second.Code, second.Body.String())
	}
	events := store.RiskEvents()
	if len(events) != 2 || events[0].DeviceRecognized || !events[1].DeviceRecognized {
		t.Fatalf("device should become recognized only after first successful login: %#v", events)
	}
	if events[1].DeviceTrusted {
		t.Fatal("password-observed device must not be treated as strong-authentication trusted")
	}
}

func TestPublicComputerLoginClearsDeviceBindingCookie(t *testing.T) {
	store := newRiskTestStore(t)
	handler := NewHandler(store, time.Hour, HandlerOptions{RiskMode: "shadow"})
	first := riskLoginRequest(t, handler, `{"tenant_code":"demo","identifier":"teacher","password":"TeacherRiskPassphrase"}`, nil)
	deviceCookie := responseCookie(first.Result().Cookies(), "edugrade_device")
	if deviceCookie == nil {
		t.Fatal("expected initial device cookie")
	}

	public := riskLoginRequest(t, handler, `{"tenant_code":"demo","identifier":"teacher","password":"TeacherRiskPassphrase","public_device":true}`, deviceCookie)
	cleared := responseCookie(public.Result().Cookies(), "edugrade_device")
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Fatalf("public computer mode must clear device binding cookie: %#v", cleared)
	}
	events := store.RiskEvents()
	if len(events) != 2 || events[1].DeviceRecognized {
		t.Fatalf("public computer login must not consume a private device binding: %#v", events)
	}
}

func TestPasswordReauthenticationDoesNotLowerSessionRisk(t *testing.T) {
	store := newRiskTestStore(t)
	_, tokenHash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateSession(context.Background(), CreateSessionInput{
		TenantID: "tenant-1", UserID: "user-1", TokenHash: tokenHash,
		SessionType: SessionTypeStandard, ExpiresAt: now.Add(time.Hour), SecurityEpoch: 1,
		RiskLevel: RiskLevelHigh, RiskAction: RiskActionStepUp, RiskPolicyVersion: RiskPolicyVersionV1,
		RiskEvidenceQuality: RiskEvidenceSufficient,
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := store.MarkSessionReauthenticated(context.Background(), "tenant-1", "user-1", tokenHash, now, now.Add(time.Second)); err != nil || !updated {
		t.Fatalf("reauthenticate session: updated=%v err=%v", updated, err)
	}
	user, err := store.FindUserBySession(context.Background(), tokenHash, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if user.CurrentRiskLevel != RiskLevelHigh || user.CurrentRiskAction != RiskActionStepUp {
		t.Fatalf("password freshness must not erase risk: %#v", user)
	}
}

func TestRiskHistoryRetentionKeepsOnlyLatestTwentyLoginProfiles(t *testing.T) {
	store := newRiskTestStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	for index := 0; index < 25; index++ {
		if err := store.RecordRiskEvent(context.Background(), RiskEvent{
			TenantID: "tenant-1", UserID: "user-1", Purpose: RiskPurposeLogin,
			Level: RiskLevelLow, Action: RiskActionAllow, PolicyVersion: RiskPolicyVersionV1,
			EvidenceQuality: RiskEvidenceSufficient, OccurredAt: base.Add(time.Duration(index) * time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(store.RiskEvents()); got != RiskHistoryLimit {
		t.Fatalf("expected %d retained events, got %d", RiskHistoryLimit, got)
	}
}

func newRiskTestStore(t *testing.T) *MemoryStore {
	t.Helper()
	hash, err := HashPassword("TeacherRiskPassphrase")
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	store.AddUser(UserWithPassword{
		User: User{
			ID: "user-1", TenantID: "tenant-1", TenantCode: "demo", Username: "teacher",
			DisplayName: "Teacher", Status: "active", Roles: []string{"teacher"},
			DataScope: map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash, SecurityEpoch: 1,
	})
	return store
}

func riskLoginRequest(t *testing.T, handler *Handler, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "RiskBrowser/1.0")
	req.RemoteAddr = "203.0.113.41:54321"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	return rec
}

func responseCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

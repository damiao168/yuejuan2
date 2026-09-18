package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const loginServicePassword = "TeacherPrivatePassphrase"

func newLoginServiceFixture(t *testing.T, roles ...string) (*MemoryStore, *LoginService) {
	t.Helper()
	if len(roles) == 0 {
		roles = []string{"teacher"}
	}
	hash, err := HashPassword(loginServicePassword)
	if err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	store.AddUser(UserWithPassword{
		User: User{
			ID: "user-1", TenantID: "tenant-1", TenantCode: "demo", Username: "teacher",
			DisplayName: "Teacher", Status: "active", Roles: roles,
			DataScope: map[string]any{"scope": "class", "class_ids": []any{"class-1"}},
		},
		PasswordHash: hash,
	})
	return store, newLoginServiceForTest(store, store, NewMemoryLoginAttemptGuard(5, time.Hour), NewStoreRiskEvaluator(store, "shadow"), false)
}

func newLoginServiceForTest(
	store *MemoryStore,
	sessions SessionRepository,
	guard LoginAttemptGuard,
	risk RiskEvaluator,
	failClosed bool,
) *LoginService {
	return NewLoginService(store, sessions, store, store, guard, risk, LoginServiceOptions{
		SessionTTL: time.Hour, RememberedSessionTTL: 30 * 24 * time.Hour,
		PublicSessionTTL: 4 * time.Hour, DeviceBindingTTL: DefaultDeviceBindingTTL,
		RiskMode: "shadow", LoginLimiterFailClosed: failClosed,
	})
}

func validLoginCommand() LoginCommand {
	return LoginCommand{
		TenantCode: "demo", Identifier: "teacher", Password: loginServicePassword,
		IPAddress: "203.0.113.1", UserAgent: "LoginServiceTest/1.0", RequestID: "request-1",
	}
}

func requireLoginFailure(t *testing.T, err error, kind LoginFailure) *LoginServiceError {
	t.Helper()
	var loginErr *LoginServiceError
	if !errors.As(err, &loginErr) || loginErr.Kind != kind {
		t.Fatalf("expected login failure %q, got %T %v", kind, err, err)
	}
	return loginErr
}

func TestLoginServiceSuccessfulBrowserLogin(t *testing.T) {
	store, service := newLoginServiceFixture(t)
	result, err := service.Login(context.Background(), validLoginCommand())
	if err != nil {
		t.Fatal(err)
	}
	if result.Token == "" || result.User.CurrentSessionType != SessionTypeStandard || result.User.OrganizationScope == nil {
		t.Fatalf("unexpected login result: %#v", result)
	}
	if sessions, err := store.ListSessions(context.Background(), "tenant-1", "user-1", "", time.Now().UTC()); err != nil || len(sessions) != 1 {
		t.Fatalf("expected one session, got %#v err=%v", sessions, err)
	}
}

func TestLoginServiceUnknownUserUsesGenericCredentialFailure(t *testing.T) {
	_, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.Identifier = "missing"
	_, err := service.Login(context.Background(), command)
	requireLoginFailure(t, err, LoginFailureInvalidCredentials)
}

func TestLoginServiceWrongPassword(t *testing.T) {
	store, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.Password = "incorrect"
	_, err := service.Login(context.Background(), command)
	requireLoginFailure(t, err, LoginFailureInvalidCredentials)
	if audits := store.Audits(); len(audits) != 1 || audits[0].Action != "auth.login_failed" {
		t.Fatalf("expected generic failure audit, got %#v", audits)
	}
}

type alwaysBlockedLoginGuard struct{}

func (alwaysBlockedLoginGuard) Check(context.Context, LoginAttempt, time.Time) (LoginLimit, bool) {
	return LoginLimit{Bucket: "source", RetryAfter: time.Minute}, true
}
func (alwaysBlockedLoginGuard) RegisterFailure(context.Context, LoginAttempt, time.Time) (LoginLimit, bool) {
	return LoginLimit{}, false
}
func (alwaysBlockedLoginGuard) RegisterSuccess(context.Context, LoginAttempt) {}

func TestLoginServiceLimiterBlocked(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	service := newLoginServiceForTest(store, store, alwaysBlockedLoginGuard{}, NewStoreRiskEvaluator(store, "shadow"), false)
	_, err := service.Login(context.Background(), validLoginCommand())
	loginErr := requireLoginFailure(t, err, LoginFailureRateLimited)
	if loginErr.RetryAfter != time.Minute {
		t.Fatalf("unexpected retry interval: %v", loginErr.RetryAfter)
	}
}

type degradedLoginServiceGuard struct{ alwaysBlockedLoginGuard }

func (degradedLoginServiceGuard) Check(context.Context, LoginAttempt, time.Time) (LoginLimit, bool) {
	return LoginLimit{}, false
}
func (degradedLoginServiceGuard) Degraded() bool { return true }

func TestLoginServiceFailsClosedWhenLimiterDependencyUnavailable(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	service := newLoginServiceForTest(store, store, degradedLoginServiceGuard{}, NewStoreRiskEvaluator(store, "shadow"), true)
	_, err := service.Login(context.Background(), validLoginCommand())
	requireLoginFailure(t, err, LoginFailureLimiterUnavailable)
}

func TestLoginServiceRehashesLegacyPassword(t *testing.T) {
	store, service := newLoginServiceFixture(t)
	legacyHash, err := bcrypt.GenerateFromPassword([]byte(loginServicePassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	user, err := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	user.PasswordHash = string(legacyHash)
	store.AddUser(user)
	if _, err = service.Login(context.Background(), validLoginCommand()); err != nil {
		t.Fatal(err)
	}
	updated, err := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if err != nil || !strings.HasPrefix(updated.PasswordHash, "$argon2id$") {
		t.Fatalf("expected transparent rehash, user=%#v err=%v", updated, err)
	}
}

func TestLoginServiceDesktopSession(t *testing.T) {
	_, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.TokenResponse = true
	command.ClientType = "desktop"
	result, err := service.Login(context.Background(), command)
	if err != nil || result.User.CurrentSessionType != SessionTypeDesktopDevice || result.PersistentCookie {
		t.Fatalf("unexpected desktop result: %#v err=%v", result, err)
	}
}

func TestLoginServiceServiceSession(t *testing.T) {
	_, service := newLoginServiceFixture(t, "page_processing_worker")
	command := validLoginCommand()
	command.TokenResponse = true
	command.ClientType = "service"
	result, err := service.Login(context.Background(), command)
	if err != nil || result.User.CurrentSessionType != SessionTypeService {
		t.Fatalf("unexpected service result: %#v err=%v", result, err)
	}
}

func TestLoginServiceRejectsBrowserLoginForServiceAccount(t *testing.T) {
	_, service := newLoginServiceFixture(t, "page_processing_worker")
	_, err := service.Login(context.Background(), validLoginCommand())
	loginErr := requireLoginFailure(t, err, LoginFailureClientTypeForbidden)
	if loginErr.Detail != "browser_service_account" {
		t.Fatalf("unexpected restriction: %#v", loginErr)
	}
}

func TestLoginServicePublicDevicePolicy(t *testing.T) {
	_, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.PublicDevice = true
	command.DeviceToken = strings.Repeat("a", 43)
	result, err := service.Login(context.Background(), command)
	if err != nil || result.User.CurrentSessionType != SessionTypePublicDevice || !result.ClearDeviceCookie || result.PersistentCookie {
		t.Fatalf("unexpected public-device result: %#v err=%v", result, err)
	}
	if ttl := time.Until(result.ExpiresAt); ttl < 3*time.Hour+59*time.Minute || ttl > 4*time.Hour+time.Minute {
		t.Fatalf("unexpected public-device TTL: %v", ttl)
	}
}

func TestLoginServiceRememberedDevicePolicy(t *testing.T) {
	_, service := newLoginServiceFixture(t)
	command := validLoginCommand()
	command.RememberDevice = true
	result, err := service.Login(context.Background(), command)
	if err != nil || result.User.CurrentSessionType != SessionTypeRememberedDevice || !result.PersistentCookie {
		t.Fatalf("unexpected remembered-device result: %#v err=%v", result, err)
	}
	if ttl := time.Until(result.ExpiresAt); ttl < 29*24*time.Hour || ttl > 31*24*time.Hour {
		t.Fatalf("unexpected remembered-device TTL: %v", ttl)
	}
}

type degradedRiskEvaluator struct{}

func (degradedRiskEvaluator) EvaluateLogin(_ context.Context, request LoginRiskContextRequest, _ string) (LoginRiskEvaluation, error) {
	return LoginRiskEvaluation{Decision: DefaultLoginRiskDecision(request.Now), Persistent: true}, errors.New("risk store unavailable")
}
func (degradedRiskEvaluator) RecordLogin(context.Context, RiskEvent) error { return nil }
func (degradedRiskEvaluator) ObserveDevice(context.Context, ObservedDeviceInput) error {
	return nil
}

func TestLoginServiceRiskDegradedUsesNeutralDecision(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	service := newLoginServiceForTest(store, store, NewMemoryLoginAttemptGuard(5, time.Hour), degradedRiskEvaluator{}, false)
	result, err := service.Login(context.Background(), validLoginCommand())
	if err != nil || result.User.CurrentRiskLevel != RiskLevelLow || result.User.CurrentRiskAction != RiskActionAllow {
		t.Fatalf("risk degradation must remain neutral: %#v err=%v", result, err)
	}
	found := false
	for _, event := range store.Audits() {
		found = found || event.Action == "auth.risk_evaluation_degraded"
	}
	if !found {
		t.Fatal("expected risk degradation audit")
	}
}

type failingSessionRepository struct {
	SessionRepository
	err error
}

func (r failingSessionRepository) CreateSession(context.Context, CreateSessionInput) (DeviceSession, error) {
	return DeviceSession{}, r.err
}

func TestLoginServiceSessionCreateConflict(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	sessions := failingSessionRepository{SessionRepository: store, err: errors.New("session conflict")}
	service := newLoginServiceForTest(store, sessions, NewMemoryLoginAttemptGuard(5, time.Hour), NewStoreRiskEvaluator(store, "shadow"), false)
	_, err := service.Login(context.Background(), validLoginCommand())
	requireLoginFailure(t, err, LoginFailureSessionCreate)
}

func TestLoginServiceSecurityEpochFailureIsCredentialFailure(t *testing.T) {
	store, _ := newLoginServiceFixture(t)
	sessions := failingSessionRepository{SessionRepository: store, err: ErrInvalidCredentials}
	service := newLoginServiceForTest(store, sessions, NewMemoryLoginAttemptGuard(5, time.Hour), NewStoreRiskEvaluator(store, "shadow"), false)
	_, err := service.Login(context.Background(), validLoginCommand())
	requireLoginFailure(t, err, LoginFailureInvalidCredentials)
}

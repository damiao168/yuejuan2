package auth

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type loginBatch21Repository struct {
	*MemoryStore
	events     *[]string
	confirmErr error
}

func (r loginBatch21Repository) FindUserByLogin(ctx context.Context, tenant, identifier string) (UserWithPassword, error) {
	*r.events = append(*r.events, "find")
	return r.MemoryStore.FindUserByLogin(ctx, tenant, identifier)
}

func (r loginBatch21Repository) RecordSuccessfulLogin(ctx context.Context, tenant, user, expected, replacement string) error {
	*r.events = append(*r.events, "credentials")
	if r.confirmErr != nil {
		return r.confirmErr
	}
	return r.MemoryStore.RecordSuccessfulLogin(ctx, tenant, user, expected, replacement)
}

func (r loginBatch21Repository) CreateSession(ctx context.Context, input CreateSessionInput) (DeviceSession, error) {
	*r.events = append(*r.events, "session")
	return r.MemoryStore.CreateSession(ctx, input)
}

func (r loginBatch21Repository) Audit(ctx context.Context, event AuditEvent) error {
	*r.events = append(*r.events, event.Action)
	return r.MemoryStore.Audit(ctx, event)
}

type loginBatch21Guard struct {
	LoginAttemptGuard
	events *[]string
}

func (g loginBatch21Guard) Check(ctx context.Context, attempt LoginAttempt, now time.Time) (LoginLimit, bool) {
	bucket := "source"
	if attempt.AccountID != "" {
		bucket = "account"
	}
	*g.events = append(*g.events, bucket)
	return g.LoginAttemptGuard.Check(ctx, attempt, now)
}

func (g loginBatch21Guard) RegisterSuccess(ctx context.Context, attempt LoginAttempt) {
	*g.events = append(*g.events, "success")
	g.LoginAttemptGuard.RegisterSuccess(ctx, attempt)
}

type loginBatch21Risk struct {
	t          *testing.T
	events     *[]string
	recordErr  error
	observeErr error
}

func (r loginBatch21Risk) requireUser(ctx context.Context) {
	r.t.Helper()
	user, ok := UserFromContext(ctx)
	if !ok || user.ID != "user-1" {
		r.t.Fatal("risk calls must retain the authenticated user context")
	}
}

func (r loginBatch21Risk) EvaluateLogin(ctx context.Context, request LoginRiskContextRequest, _ string) (LoginRiskEvaluation, error) {
	r.requireUser(ctx)
	*r.events = append(*r.events, "evaluate")
	return LoginRiskEvaluation{Decision: DefaultLoginRiskDecision(request.Now), Persistent: true}, nil
}

func (r loginBatch21Risk) RecordLogin(ctx context.Context, event RiskEvent) error {
	r.requireUser(ctx)
	*r.events = append(*r.events, "risk-event")
	if event.SessionID == "" {
		r.t.Fatal("risk event must use the created session")
	}
	return r.recordErr
}

func (r loginBatch21Risk) ObserveDevice(ctx context.Context, _ ObservedDeviceInput) error {
	r.requireUser(ctx)
	*r.events = append(*r.events, "observe")
	return r.observeErr
}

func newLoginBatch21Service(t *testing.T, events *[]string, confirmErr, recordErr, observeErr error) (*MemoryStore, *LoginService) {
	t.Helper()
	store, base := newLoginServiceFixture(t)
	repository := loginBatch21Repository{MemoryStore: store, events: events, confirmErr: confirmErr}
	guard := loginBatch21Guard{LoginAttemptGuard: base.guard, events: events}
	risk := loginBatch21Risk{t: t, events: events, recordErr: recordErr, observeErr: observeErr}
	service := NewLoginService(repository, repository, store, repository, guard, risk, base.options)
	calls := 0
	service.newToken = func() (string, string, error) {
		calls++
		*events = append(*events, fmt.Sprintf("token%d", calls))
		return fmt.Sprintf("token-%d", calls), fmt.Sprintf("hash-%d", calls), nil
	}
	return store, service
}

func TestLoginServiceBatch21StageOrderAndCredentialCASFailure(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			assertLoginCredentialCASStageOrder(t, conflict)
		})
	}
}

func TestLoginServiceBatch21DeviceBindingFailuresKeepCreatedSession(t *testing.T) {
	for _, failure := range []string{"generate", "observe", "event"} {
		t.Run(failure, func(t *testing.T) {
			assertLoginDeviceBindingFailureRetainsSession(t, failure)
		})
	}
}

func TestLoginServiceBatch21InputErrorsPrecedeDependencies(t *testing.T) {
	cases := []struct {
		name, password, detail    string
		remembered, public, token bool
	}{
		{"missing-first", "", "missing_required", true, true, true},
		{"remembered-before-token", "x", "public_remembered", true, true, true},
		{"public-token", "x", "public_token", false, true, true},
	}
	service := &LoginService{}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			command := validLoginCommand()
			command.Password, command.RememberDevice, command.PublicDevice, command.TokenResponse = test.password, test.remembered, test.public, test.token
			_, err := service.Login(context.Background(), command)
			var loginErr *LoginServiceError
			if !errors.As(err, &loginErr) || loginErr.Detail != test.detail {
				t.Fatalf("unexpected first input error: %v", err)
			}
		})
	}
}

func TestLoginServiceBatch21TokenClientPolicy(t *testing.T) {
	service := &LoginService{options: LoginServiceOptions{SessionTTL: time.Hour}}
	cases := []struct {
		name, client, role, session, detail string
		kind                                LoginFailure
	}{
		{"desktop-default", "", "teacher", SessionTypeDesktopDevice, "", ""},
		{"service-default-forbidden", "", "page_processing_worker", "", "account_client_type", LoginFailureClientTypeForbidden},
		{"service-client", "service", "page_processing_worker", SessionTypeService, "", ""},
		{"teacher-service-forbidden", "service", "teacher", "", "account_client_type", LoginFailureClientTypeForbidden},
		{"unknown-client", "unknown", "teacher", "", "", LoginFailureClientTypeRequired},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			command := LoginCommand{TokenResponse: true, ClientType: test.client}
			session, ttl, err := service.sessionPolicy(User{Roles: []string{test.role}}, &command)
			if test.kind != "" {
				loginErr := requireLoginFailure(t, err, test.kind)
				if loginErr.Detail != test.detail || session != "" || ttl != 0 {
					t.Fatalf("unexpected forbidden policy: %q %v %#v", session, ttl, loginErr)
				}
				return
			}
			if err != nil || session != test.session || ttl != time.Hour || command.ClientType == "" {
				t.Fatalf("unexpected token policy: %q %v %v", session, ttl, err)
			}
		})
	}
}

func assertLoginCredentialCASStageOrder(t *testing.T, conflict bool) {
	t.Helper()
	var events []string
	var confirmErr error
	if conflict {
		confirmErr = ErrInvalidCredentials
	}
	store, service := newLoginBatch21Service(t, &events, confirmErr, nil, nil)
	result, err := service.Login(context.Background(), validLoginCommand())
	want := []string{"source", "find", "account", "evaluate", "credentials"}
	if conflict {
		requireLoginFailure(t, err, LoginFailureInvalidCredentials)
		if sessions, err := store.ListSessions(context.Background(), "tenant-1", "user-1", "", time.Now()); err != nil || len(sessions) != 0 {
			t.Fatalf("credential conflict must not create a session: %v %v", sessions, err)
		}
	} else {
		if err != nil || result.DeviceToken != "token-3" {
			t.Fatalf("unexpected success: %#v %v", result, err)
		}
		want = append(want, "token1", "token2", "session", "risk-event", "token3", "observe", "success", "auth.login_succeeded")
	}
	// 提取阶段后仍先确认旧凭据，再建会话，最后记录成功；不能只断言最终 token。
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("order = %v, want %v", events, want)
	}
}

func assertLoginDeviceBindingFailureRetainsSession(t *testing.T, failure string) {
	t.Helper()
	var events []string
	var recordErr, observeErr error
	if failure == "event" {
		recordErr = errors.New("risk event unavailable")
	}
	if failure == "observe" {
		observeErr = errors.New("device storage unavailable")
	}
	store, service := newLoginBatch21Service(t, &events, nil, recordErr, observeErr)
	if failure == "generate" {
		original := service.newToken
		calls := 0
		service.newToken = func() (string, string, error) {
			calls++
			if calls == 3 {
				return "", "", errors.New("device token unavailable")
			}
			return original()
		}
	}
	result, err := service.Login(context.Background(), validLoginCommand())
	if err != nil || result.Token != "token-1" {
		t.Fatalf("post-session degradation must retain login: %#v %v", result, err)
	}
	wantDevice := ""
	if failure == "event" {
		wantDevice = "token-3"
	}
	if result.DeviceToken != wantDevice {
		t.Fatalf("device token = %q, want %q", result.DeviceToken, wantDevice)
	}
	if sessions, err := store.ListSessions(context.Background(), "tenant-1", "user-1", "", time.Now()); err != nil || len(sessions) != 1 {
		t.Fatalf("expected retained session: %v %v", sessions, err)
	}
	if events[len(events)-1] != "auth.login_succeeded" {
		t.Fatalf("missing success audit after degradation: %v", events)
	}
}

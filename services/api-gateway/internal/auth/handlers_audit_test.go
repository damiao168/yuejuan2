package auth_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginFailureAudited(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)

	body := bytes.NewBufferString(`{"tenant_code":"demo","username":"teacher","password":"bad"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	audits := store.Audits()
	if len(audits) != 1 || audits[0].Action != "auth.login_failed" {
		t.Fatalf("expected login failure audit, got %#v", audits)
	}
	if audits[0].IPAddress == "203.0.113.99" {
		t.Fatalf("login audit must not trust unverified X-Forwarded-For: %#v", audits[0])
	}
}

func TestLoginFailureRateLimited(t *testing.T) {
	store := newTestStore(t)
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth: config.AuthConfig{
			SessionTTL:         time.Hour,
			LoginFailureLimit:  2,
			LoginFailureWindow: time.Hour,
		},
	}
	router := newTestRouterWithConfig(store, cfg)

	for i, expected := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusTooManyRequests} {
		body := bytes.NewBufferString(`{"tenant_code":"demo","username":"teacher","password":"bad"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != expected {
			t.Fatalf("attempt %d expected %d, got %d %s", i+1, expected, rec.Code, rec.Body.String())
		}
		if expected == http.StatusTooManyRequests && rec.Header().Get("Retry-After") == "" {
			t.Fatalf("rate limited response must include Retry-After")
		}
	}
	audits := store.Audits()
	found := false
	for _, audit := range audits {
		if audit.Action == "auth.login_rate_limited" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected auth.login_rate_limited audit, got %#v", audits)
	}
}

func TestLoginStoreFailureReturnsUnavailableWithoutRateLimiting(t *testing.T) {
	base := newTestStore(t)
	store := &loginErrorStore{Store: base, err: errors.New("postgres is starting up")}
	handler := auth.NewHandler(store, time.Hour, auth.HandlerOptions{
		LoginFailureLimit:  1,
		LoginFailureWindow: time.Hour,
	})

	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
		rec := httptest.NewRecorder()
		handler.Login(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("attempt %d expected 503, got %d: %s", attempt+1, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Fatalf("dependency failure must not set login limiter Retry-After: %s", rec.Header().Get("Retry-After"))
		}
	}
	if audits := base.Audits(); len(audits) != 0 {
		t.Fatalf("dependency failure must not create invalid-login audits: %#v", audits)
	}

	store.err = nil
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
	rec := httptest.NewRecorder()
	handler.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid login after dependency recovery expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginFailsClosedWhenDistributedLimiterIsDegraded(t *testing.T) {
	handler := auth.NewHandler(newTestStore(t), time.Hour, auth.HandlerOptions{
		LoginGuard:             degradedLoginGuard{},
		LoginLimiterFailClosed: true,
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"ChangeMe123!"}`))
	response := httptest.NewRecorder()

	handler.Login(response, request)

	if response.Code != http.StatusServiceUnavailable || response.Header().Get("X-EduGrade-Error-Code") != "auth_rate_limiter_unavailable" {
		t.Fatalf("degraded limiter must fail closed, got %d: %s", response.Code, response.Body.String())
	}
}

type degradedLoginGuard struct{}

func (degradedLoginGuard) Check(context.Context, auth.LoginAttempt, time.Time) (auth.LoginLimit, bool) {
	return auth.LoginLimit{}, false
}

func (degradedLoginGuard) RegisterFailure(context.Context, auth.LoginAttempt, time.Time) (auth.LoginLimit, bool) {
	return auth.LoginLimit{}, false
}

func (degradedLoginGuard) RegisterSuccess(context.Context, auth.LoginAttempt) {}

func (degradedLoginGuard) Degraded() bool { return true }

type loginErrorStore struct {
	auth.Store
	err error
}

func (s *loginErrorStore) FindUserByLogin(ctx context.Context, tenantCode string, username string) (auth.UserWithPassword, error) {
	if s.err != nil {
		return auth.UserWithPassword{}, s.err
	}
	return s.Store.FindUserByLogin(ctx, tenantCode, username)
}

func TestListAuditsFiltersByTarget(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "arbitration.submitted",
		TargetType: "arbitration_task",
		TargetID:   "task-1",
		Reason:     "submit arbitration final score",
	})
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "final_grade.created",
		TargetType: "final_grade",
		TargetID:   "grade-1",
		Reason:     "create final grade from arbitration",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?target_type=arbitration_task&target_id=task-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AuditLogs []auth.AuditRecord `json:"audit_logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(response.AuditLogs) != 1 {
		t.Fatalf("expected one filtered audit log, got %#v", response.AuditLogs)
	}
	if response.AuditLogs[0].Action != "arbitration.submitted" || response.AuditLogs[0].TargetID != "task-1" {
		t.Fatalf("unexpected audit log: %#v", response.AuditLogs[0])
	}
}

func TestListAuditsFiltersByIPAndIncludesValues(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:    "t-1",
		ActorID:     "u-1",
		Action:      "exam.updated",
		TargetType:  "exam",
		TargetID:    "exam-1",
		BeforeValue: map[string]any{"status": "draft"},
		AfterValue:  map[string]any{"status": "configured"},
		Reason:      "update exam status",
		IPAddress:   "10.0.0.8",
	})
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "exam.updated",
		TargetType: "exam",
		TargetID:   "exam-2",
		Reason:     "update another exam",
		IPAddress:  "10.0.0.9",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?action=exam.updated&exam_id=exam-1&ip_address=10.0.0.8", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AuditLogs []auth.AuditRecord `json:"audit_logs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if len(response.AuditLogs) != 1 {
		t.Fatalf("expected one filtered audit log, got %#v", response.AuditLogs)
	}
	if response.AuditLogs[0].BeforeValue["status"] != "draft" || response.AuditLogs[0].AfterValue["status"] != "configured" {
		t.Fatalf("expected before/after values, got %#v", response.AuditLogs[0])
	}
}

func TestAuditListAndExportRedactSensitiveFields(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "user.updated",
		TargetType: "user",
		TargetID:   "u-1",
		BeforeValue: map[string]any{
			"password": "plain-text-should-not-leak",
			"profile":  map[string]any{"api_token": "token-should-not-leak", "display_name": "Teacher"},
		},
		AfterValue: map[string]any{"secret": "secret-should-not-leak", "status": "active"},
		Reason:     "update user",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?action=user.updated", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit list expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"plain-text-should-not-leak", "token-should-not-leak", "secret-should-not-leak"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit list leaked %s: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, "Teacher") {
		t.Fatalf("audit list should redact sensitive fields and preserve non-sensitive context: %s", body)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export?action=user.updated", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit export expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, forbidden := range []string{"plain-text-should-not-leak", "token-should-not-leak", "secret-should-not-leak"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit export leaked %s: %s", forbidden, body)
		}
	}
}

func TestExportAuditsWritesAuditAndWatermark(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "score.published",
		TargetType: "exam",
		TargetID:   "exam-1",
		Reason:     "publish scores",
		IPAddress:  "10.0.0.8",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export?action=score.published&limit=20", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("audit export expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-EduGrade-Watermark") == "" {
		t.Fatalf("expected watermark header")
	}
	if !strings.Contains(rec.Body.String(), "score.published") || !strings.Contains(rec.Body.String(), "EduGrade audit export") {
		t.Fatalf("expected CSV body with audit row and watermark, got %s", rec.Body.String())
	}
	audits := store.Audits()
	if len(audits) == 0 || audits[len(audits)-1].Action != "audit.exported" {
		t.Fatalf("expected audit.exported event, got %#v", audits)
	}
}

func TestAuditLogsCannotBeDeletedThroughOrdinaryAPI(t *testing.T) {
	store := newTestStore(t)
	addAuditUser(t, store)
	router := newTestRouter(store)
	_ = store.Audit(context.Background(), auth.AuditEvent{
		TenantID:   "t-1",
		ActorID:    "u-1",
		Action:     "score.exported",
		TargetType: "exam",
		TargetID:   "exam-1",
		Reason:     "export grades",
	})

	token := login(t, router, "demo", "auditor", "ChangeMe123!")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/audit-logs/memory-audit-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("audit delete route must not exist, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestListAuditsRequiresPermission(t *testing.T) {
	store := newTestStore(t)
	router := newTestRouter(store)
	token := login(t, router, "demo", "teacher", "ChangeMe123!")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

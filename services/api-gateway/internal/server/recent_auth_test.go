package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type recentAuthTestStore struct {
	*auth.MemoryStore
	stale           bool
	denyPermissions bool
	platformRole    bool
}

func (s *recentAuthTestStore) FindUserBySession(ctx context.Context, tokenHash string, now time.Time) (auth.User, error) {
	user, err := s.MemoryStore.FindUserBySession(ctx, tokenHash, now)
	if s.stale {
		at := now.Add(-61 * time.Minute)
		user.ReauthenticatedAt = &at
	}
	if s.denyPermissions {
		user.Permissions = nil
	}
	if s.platformRole {
		user.Roles = []string{"platform_admin"}
	}
	return user, err
}

func TestRoutineReceiptReplayChecksCurrentPermissions(t *testing.T) {
	store := &recentAuthTestStore{MemoryStore: testAuthStoreWithPermissions(t, []string{"audit:export"})}
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, store, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverLogin(t, router)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "audit-export-current-auth")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("fresh export: %d %s", first.Code, first.Body.String())
	}
	store.stale = true
	stale := request()
	if stale.Code != http.StatusOK || stale.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("routine export unexpectedly required recent authentication: %d %s", stale.Code, stale.Body.String())
	}
	store.denyPermissions = true
	forbidden := request()
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("cached export bypassed current permission: %d %s", forbidden.Code, forbidden.Body.String())
	}
	store.denyPermissions = false
	replay := request()
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Body.String() != first.Body.String() {
		t.Fatalf("authorized fresh replay must retain the immutable result: %d %s", replay.Code, replay.Body.String())
	}
}

func TestCriticalReceiptReplayChecksRecentAuthAndPermissions(t *testing.T) {
	store := &recentAuthTestStore{MemoryStore: testAuthStoreWithPermissions(t, []string{"session:revoke"}), platformRole: true}
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, store, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverLogin(t, router)
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-2/sessions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "credential-reset-current-auth")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	first := request()
	if first.Code != http.StatusOK {
		t.Fatalf("fresh session revocation: %d %s", first.Code, first.Body.String())
	}
	store.stale = true
	stale := request()
	if stale.Code != http.StatusPreconditionRequired || !strings.Contains(stale.Body.String(), "recent_auth_required") {
		t.Fatalf("cached critical result bypassed recent authentication: %d %s", stale.Code, stale.Body.String())
	}
	store.stale = false
	store.denyPermissions = true
	forbidden := request()
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("cached critical result bypassed current permission: %d %s", forbidden.Code, forbidden.Body.String())
	}
	store.denyPermissions = false
	replay := request()
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Body.String() != first.Body.String() {
		t.Fatalf("authorized fresh replay must retain the immutable result: %d %s", replay.Code, replay.Body.String())
	}
}

func TestCriticalRoutesRejectStaleAuthentication(t *testing.T) {
	store := &recentAuthTestStore{
		MemoryStore: testAuthStoreWithPermissions(t, []string{"org:manage", "tenant:manage", "session:revoke", "score:manage"}),
		stale:       true, platformRole: true,
	}
	stores := NewMemoryApplicationStores()
	stores.Identity.Auth = store
	stores.AIFoundation.Eligibility = aieligibility.NewMemoryStore()
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	token := serverLogin(t, router)
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/tenants"},
		{http.MethodPatch, "/api/v1/tenants/tenant-2"},
		{http.MethodDelete, "/api/v1/users/user-2/sessions"},
		{http.MethodPost, "/api/v1/users/user-2/credential-reset"},
		{http.MethodPost, "/api/v1/exams/exam-1/publish"},
		{http.MethodPost, "/api/v1/score-releases/release-1/publish"},
		{http.MethodPost, "/api/v1/exams/exam-1/score-releases/rollback"},
	} {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			req := httptest.NewRequest(target.method, target.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", "stale-command")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusPreconditionRequired || !strings.Contains(rec.Body.String(), "recent_auth_required") {
				t.Fatalf("sensitive route lacks recent-auth protection: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestRoutineAdministrativeRoutesDoNotRequireRecentAuth(t *testing.T) {
	store := &recentAuthTestStore{
		MemoryStore: testAuthStoreWithPermissions(t, []string{"org:manage", "audit:export", "score:manage", "report:export", "model:provider:manage", "model:policy:manage"}),
		stale:       true, platformRole: true,
	}
	stores := NewMemoryApplicationStores()
	stores.Identity.Auth = store
	stores.AIFoundation.Eligibility = aieligibility.NewMemoryStore()
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	token := serverLogin(t, router)
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/users"},
		{http.MethodPost, "/api/v1/users/user-2/activation"},
		{http.MethodPatch, "/api/v1/users/user-2/status"},
		{http.MethodPost, "/api/v1/audit-logs/export"},
		{http.MethodPost, "/api/v1/exams/exam-1/finalize"},
		{http.MethodPost, "/api/v1/exams/exam-1/confirm-grades"},
		{http.MethodGet, "/api/v1/exams/exam-1/grades/export"},
		{http.MethodPost, "/api/v1/exams/exam-1/score-releases"},
		{http.MethodGet, "/api/v1/score-releases/release-1"},
		{http.MethodGet, "/api/v1/score-releases/release-1/diff"},
		{http.MethodPost, "/api/v1/exams/exam-1/reports/export"},
		{http.MethodGet, "/api/v1/report-commands/command-1"},
		{http.MethodPost, "/api/v1/regrade-jobs/job-1/score-release"},
		{http.MethodPost, "/api/v1/exams/exam-1/release-gate/policies"},
		{http.MethodPost, "/api/v1/exams/exam-1/release-gate/preview"},
		{http.MethodPost, "/api/v1/exams/exam-1/release-gate/waivers"},
		{http.MethodPost, "/api/v1/release-gate-waivers/waiver-1/decision"},
		{http.MethodPost, "/api/v1/model-providers"},
		{http.MethodPatch, "/api/v1/model-providers/provider-1/status"},
		{http.MethodPost, "/api/v1/model-deployments"},
		{http.MethodPut, "/api/v1/model-policy"},
		{http.MethodPost, "/api/v1/model-secrets/probe"},
		{http.MethodPost, "/api/v1/model-sandbox-approvals"},
		{http.MethodPost, "/api/v1/model-sandbox-approvals/approval-1/revoke"},
		{http.MethodPut, "/api/v1/ai-eligibility/policy"},
	} {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			req := httptest.NewRequest(target.method, target.path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", "routine-stale-command")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusPreconditionRequired && strings.Contains(rec.Body.String(), "recent_auth_required") {
				t.Fatalf("routine route unexpectedly requires recent authentication: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPlatformModelConfigDoesNotRepeatPasswordChallenge(t *testing.T) {
	store := &recentAuthTestStore{
		MemoryStore:  testAuthStoreWithPermissions(t, []string{"model:provider:manage"}),
		stale:        true,
		platformRole: true,
	}
	stores := NewMemoryApplicationStores()
	stores.Identity.Auth = store
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	token := serverLogin(t, router)

	for _, target := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/platform/model-api-configs", `{}`},
		{http.MethodPatch, "/api/v1/platform/model-api-configs/config-1", `{}`},
		{http.MethodDelete, "/api/v1/platform/model-api-configs/config-1", ""},
	} {
		t.Run(target.method+" "+target.path, func(t *testing.T) {
			req := httptest.NewRequest(target.method, target.path, strings.NewReader(target.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", "platform-model-config-no-password")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusPreconditionRequired || strings.Contains(rec.Body.String(), "recent_auth_required") {
				t.Fatalf("platform model config unexpectedly requested the login password again: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

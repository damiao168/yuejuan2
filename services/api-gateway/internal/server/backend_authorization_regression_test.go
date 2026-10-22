package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func backendRegressionConfig() config.Config {
	return config.Config{Service: config.ServiceConfig{Name: "backend-regression", Environment: "test"},
		Security:      config.SecurityConfig{MaxRequestBodyBytes: 2 << 20},
		Observability: config.ObservabilityConfig{SlowRequestThreshold: time.Hour}}
}

func backendRegressionSession(t *testing.T, identities *auth.MemoryStore, user auth.UserWithPassword, token string) {
	t.Helper()
	identities.AddUser(user)
	_, err := identities.CreateSession(context.Background(), auth.CreateSessionInput{TenantID: user.TenantID, UserID: user.ID,
		TokenHash: auth.HashToken(token), ExpiresAt: time.Now().Add(time.Hour), SecurityEpoch: user.SecurityEpoch})
	if err != nil {
		t.Fatal(err)
	}
}

func backendRegressionRequest(handler http.Handler, method, path, token, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestUsersReceiptCannotSurvivePermissionRevocationAndNewSession(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	user := auth.UserWithPassword{User: auth.User{ID: "actor", TenantID: "tenant", Username: "admin", DisplayName: "private user",
		Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"org:manage"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}},
		SchoolID: "school-a", SecurityEpoch: 1}
	backendRegressionSession(t, identities, user, "old-session")
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	first := backendRegressionRequest(router, http.MethodGet, "/api/v1/users", "old-session", "users-command")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "private user") {
		t.Fatalf("initial users=%d %s", first.Code, first.Body.String())
	}
	// 安全版本递增撤销旧会话，再用同一主体的新会话和旧键重试。
	user.Roles, user.Permissions = []string{"teacher"}, []string{"exam:manage"}
	user.DataScope = map[string]any{"teacher": map[string]any{"scope": "class", "class_id": "class-a"}}
	user.SecurityEpoch = 2
	backendRegressionSession(t, identities, user, "new-session")
	for _, tc := range []struct {
		token, key string
		status     int
	}{
		{"old-session", "users-command", http.StatusUnauthorized},
		{"new-session", "", http.StatusForbidden},
		{"new-session", "users-command", http.StatusForbidden},
	} {
		response := backendRegressionRequest(router, http.MethodGet, "/api/v1/users", tc.token, tc.key)
		if response.Code != tc.status || response.Header().Get("Idempotency-Replayed") == "true" {
			t.Fatalf("revoked read=%d replay=%q body=%s", response.Code, response.Header().Get("Idempotency-Replayed"), response.Body.String())
		}
	}
}

func TestCommandReceiptStillChecksCurrentPermissionAndObjectBoundary(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	user := auth.UserWithPassword{User: auth.User{ID: "actor", TenantID: "tenant", Username: "admin", Status: "active",
		Roles: []string{"school_admin"}, Permissions: []string{"org:manage"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}},
		SchoolID: "school-a", SecurityEpoch: 1}
	backendRegressionSession(t, identities, user, "session")
	identities.AddResourceBoundary("exam", "exam", auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: "exam"})
	cfg := backendRegressionConfig()
	modules := NewApplicationModules(ApplicationDependencies{Config: cfg, ObjectStore: files.NewMemoryObjectStorage()}, stores)
	guards := buildRouterGuards(cfg, modules)
	calls := 0
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams/{examId}/regression", guards.requireOrgManage(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
	}))
	path := "/api/v1/exams/exam/regression"
	first := backendRegressionRequest(mux, http.MethodPost, path, "session", "command")
	replay := backendRegressionRequest(mux, http.MethodPost, path, "session", "command")
	if first.Code != http.StatusCreated || replay.Header().Get("Idempotency-Replayed") != "true" || calls != 1 {
		t.Fatalf("initial receipt=%d replay=%q calls=%d", first.Code, replay.Header().Get("Idempotency-Replayed"), calls)
	}
	identities.AddResourceBoundary("exam", "exam", auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-b", ExamID: "exam"})
	denied := backendRegressionRequest(mux, http.MethodPost, path, "session", "command")
	if denied.Code != http.StatusForbidden || calls != 1 || denied.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("moved resource bypassed boundary: %d %s", denied.Code, denied.Body.String())
	}
	identities.AddResourceBoundary("exam", "exam", auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: "exam"})
	user.Permissions, user.SecurityEpoch = []string{"exam:manage"}, 2
	backendRegressionSession(t, identities, user, "new-session")
	denied = backendRegressionRequest(mux, http.MethodPost, path, "new-session", "command")
	if denied.Code != http.StatusForbidden || calls != 1 || denied.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("revoked command permission bypassed: %d %s", denied.Code, denied.Body.String())
	}
}

func TestSchoolAdminAppealCollectionAndStatisticsStayInsideSchool(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	user := auth.UserWithPassword{User: auth.User{ID: "admin", TenantID: "tenant", Username: "admin", Status: "active",
		Roles: []string{"school_admin"}, Permissions: []string{"appeal:read", "appeal:manage"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}, SchoolID: "school-a", SecurityEpoch: 1}
	backendRegressionSession(t, identities, user, "session")
	appeals := stores.Release.Appeal.(*appeal.MemoryStore)
	var foreignID string
	for _, seed := range []struct{ id, school string }{{"own", "school-a"}, {"foreign", "school-b"}} {
		appeals.AddSubmissionGrade(appeal.SubmissionGradeSeed{ID: seed.id + "-grade", SchoolID: seed.school,
			ExamID: seed.id + "-exam", SubmissionID: seed.id + "-submission", StudentID: seed.id + "-student", Status: "published", Locked: true})
		item, err := appeals.CreateAppeal(context.Background(), user.TenantID, seed.id+"-user", appeal.CreateAppealInput{
			ExamID: seed.id + "-exam", StudentID: seed.id + "-student", TargetType: "exam", Reason: seed.id + " private reason"})
		if err != nil {
			t.Fatal(err)
		}
		identities.AddResourceBoundary("appeal", item.ID, auth.ResourceBoundary{TenantID: user.TenantID, SchoolID: seed.school, ExamID: item.ExamID})
		if seed.id == "foreign" {
			foreignID = item.ID
		}
	}
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	direct := backendRegressionRequest(router, http.MethodGet, "/api/v1/appeals/"+foreignID, "session", "")
	if direct.Code != http.StatusForbidden {
		t.Fatalf("foreign direct=%d %s", direct.Code, direct.Body.String())
	}
	page := backendRegressionRequest(router, http.MethodGet, "/api/v1/appeals?limit=1", "session", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "own private reason") || strings.Contains(page.Body.String(), "foreign") {
		t.Fatalf("school collection=%d %s", page.Code, page.Body.String())
	}
	for _, tc := range []struct {
		query string
		total int
	}{{"", 1}, {"?exam_id=foreign-exam", 0}} {
		response := backendRegressionRequest(router, http.MethodGet, "/api/v1/appeals/statistics"+tc.query, "session", "")
		var body struct {
			Statistics appeal.AppealStatistics `json:"statistics"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Code != http.StatusOK || body.Statistics.Total != tc.total {
			t.Fatalf("school statistics=%d %s", response.Code, response.Body.String())
		}
	}
}

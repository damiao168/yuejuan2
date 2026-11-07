package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func testWorkerAuthStore(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatal(err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{User: auth.User{ID: "user-1", TenantID: "tenant-1", TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"page_processing_worker"}, Permissions: permissions, DataScope: map[string]any{"scope": "service"}}, PasswordHash: hash})
	return store
}

func serverWorkerLogin(t *testing.T, router http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewBufferString(`{"tenant_code":"demo","username":"teacher","password":"secret123","client_type":"service"}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
		t.Fatalf("worker login: %d %s", rec.Code, rec.Body.String())
	}
	return response.AccessToken
}

func TestAuditWorkerRoutesRejectSchoolAdminWithWorkerPermission(t *testing.T) {
	identities := testAuthStoreWithPermissions(t, []string{"ocr:manage", "orchestrator:manage", "system:read"})
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, identities, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverLogin(t, router)
	for _, entry := range []struct{ method, path string }{{"POST", "/api/v1/internal/worker/tasks"}, {"POST", "/api/v1/internal/worker/tasks/claim"}, {"GET", "/api/v1/internal/worker/metrics"}, {"POST", "/api/v1/internal/image-quality/jobs/claim"}, {"GET", "/api/v1/ocr-tasks/pending"}, {"POST", "/api/v1/ocr-tasks/unknown/start"}, {"GET", "/api/v1/internal/answer-segments/unknown/image"}} {
		req := httptest.NewRequest(entry.method, entry.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s returned %d: %s", entry.method, entry.path, rec.Code, rec.Body.String())
		}
	}
}

func TestAuditServiceIdentityRetainsQueueClaim(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testWorkerAuthStore(t, []string{"ocr:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverWorkerLogin(t, router)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", strings.NewReader(`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"synthetic-worker","limit":1,"lease_seconds":60}`))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("service claim returned %d: %s", rec.Code, rec.Body.String())
	}
}

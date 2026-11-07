package server

import (
	"bytes"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkerRuntimeSeparatesReadAndExecutePermissions(t *testing.T) {
	readOnlyRouter := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"system:read"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	readOnlyToken := serverLogin(t, readOnlyRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/worker/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	rec := httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("human system reader must not read worker metrics, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", strings.NewReader(`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"worker-1","limit":1,"lease_seconds":60}`))
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	rec = httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("system reader must not claim worker tasks, got %d %s", rec.Code, rec.Body.String())
	}

	executeRouter := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testWorkerAuthStore(t, []string{"ocr:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	executeToken := serverWorkerLogin(t, executeRouter)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", strings.NewReader(`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"worker-1","limit":1,"lease_seconds":60}`))
	req.Header.Set("Authorization", "Bearer "+executeToken)
	rec = httptest.NewRecorder()
	executeRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("OCR worker permission should claim tasks, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCORSAllowlistAndBodyLimit(t *testing.T) {
	cfg := testConfig()
	cfg.Security.MaxRequestBodyBytes = 16
	router := NewRouter(cfg, logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://evil.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unknown origin expected 403 without allow header, got %d headers=%#v", rec.Code, rec.Header())
	}

	req = httptest.NewRequest(http.MethodOptions, "/api/v1/auth/login", nil)
	req.Header.Set("Origin", "http://127.0.0.1:5174")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:5174" {
		t.Fatalf("allowed origin expected 204 with allow header, got %d headers=%#v", rec.Code, rec.Header())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"tenant_code":"demo","username":"teacher","password":"secret123","extra":"too-large"}`))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized json expected 413, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestNotFoundUsesUnifiedError(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/missing", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, `"code":"route_not_found"`) {
		t.Fatalf("expected unified error response, got %s", body)
	}
}

func TestTemplateLibraryRoutesRequireAuthentication(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	// 路由必须先进入身份校验；旧服务未注册这些接口时会直接落入通用 404。
	for _, path := range []string{
		"/api/v1/exams/11111111-1111-4111-8111-111111111111/answer-sheet-template-library",
		"/api/v1/exams/11111111-1111-4111-8111-111111111111/answer-sheet-template-library/resolve",
	} {
		method := http.MethodGet
		if strings.HasSuffix(path, "/resolve") {
			method = http.MethodPost
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s: expected authentication guard, got %d %s", method, path, rec.Code, rec.Body.String())
		}
	}
}

func serverLogin(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "teacher", "password": "secret123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return response.AccessToken
}

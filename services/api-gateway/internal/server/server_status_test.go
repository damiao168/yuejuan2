package server

import (
	"edugrade-enterprise/services/api-gateway/internal/deps"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicHealthProbesAreAnonymousAndReadinessIsRedacted(t *testing.T) {
	secret := "postgres://probe-user:probe-password@database.internal/edugrade"
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), []deps.Checker{
		deps.StaticChecker{CheckerName: "postgres", Err: errors.New(secret)},
	}, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())

	liveRequest := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	liveResponse := httptest.NewRecorder()
	router.ServeHTTP(liveResponse, liveRequest)
	if liveResponse.Code != http.StatusOK {
		t.Fatalf("expected anonymous liveness status 200, got %d: %s", liveResponse.Code, liveResponse.Body.String())
	}

	readyRequest := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	readyResponse := httptest.NewRecorder()
	router.ServeHTTP(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected anonymous readiness status 503, got %d: %s", readyResponse.Code, readyResponse.Body.String())
	}
	body := readyResponse.Body.String()
	if !strings.Contains(body, `"status":"not_ready"`) || !strings.Contains(body, `"name":"postgres"`) {
		t.Fatalf("unexpected readiness body: %s", body)
	}
	for _, sensitive := range []string{secret, "probe-password", `"error":`, `"detail":`} {
		if strings.Contains(body, sensitive) {
			t.Fatalf("public readiness leaked sensitive dependency detail %q: %s", sensitive, body)
		}
	}
}

func TestSystemStatusIncludesDependencyAndObservabilityState(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), []deps.Checker{
		deps.StaticChecker{CheckerName: "postgres"},
		deps.NotConfiguredChecker{CheckerName: "qdrant", DetailText: "EDUGRADE_QDRANT_URL 未配置/待接入"},
		deps.StaticChecker{CheckerName: "ai_service", Err: errors.New("down")},
	}, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, expected := range []string{
		`"status":"degraded"`,
		`"name":"postgres"`,
		`"name":"qdrant"`,
		`"status":"not_configured"`,
		`"name":"ai_service"`,
		`"name":"ocr_worker"`,
		`"availability":"not_configured"`,
		`"automation_available":false`,
		`"impact_code":"ocr_automation_not_configured"`,
		`"slow_query_log":"enabled_without_sql_text_or_parameters"`,
		`"request_id_header":"X-Request-ID"`,
		`"trace_id_header":"X-Trace-ID"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("system status missing %s: %s", expected, body)
		}
	}
}

func TestSystemStatusReportsIdleOCRWorkerAsOnline(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testWorkerAuthStore(t, []string{"system:read", "ocr:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverWorkerLogin(t, router)

	claimReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", strings.NewReader(`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"ocr-idle-1","limit":1,"lease_seconds":300}`))
	claimReq.Header.Set("Authorization", "Bearer "+token)
	claimRec := httptest.NewRecorder()
	router.ServeHTTP(claimRec, claimReq)
	if claimRec.Code != http.StatusOK || !strings.Contains(claimRec.Body.String(), `"tasks":[]`) {
		t.Fatalf("idle OCR claim expected 200 with no tasks, got %d %s", claimRec.Code, claimRec.Body.String())
	}

	statusReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	statusReq.Header.Set("Authorization", "Bearer "+token)
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, statusReq)
	if statusRec.Code != http.StatusOK {
		t.Fatalf("system status expected 200, got %d %s", statusRec.Code, statusRec.Body.String())
	}
	var response struct {
		Status         string `json:"status"`
		WorkerServices []struct {
			Name                string `json:"name"`
			Status              string `json:"status"`
			Availability        string `json:"availability"`
			AutomationAvailable bool   `json:"automation_available"`
			FreshInstances      int    `json:"fresh_instances"`
		} `json:"worker_services"`
	}
	if err := json.NewDecoder(statusRec.Body).Decode(&response); err != nil {
		t.Fatalf("decode system status: %v", err)
	}
	if response.Status != "healthy" || len(response.WorkerServices) != 1 {
		t.Fatalf("unexpected overall worker status: %#v", response)
	}
	worker := response.WorkerServices[0]
	if worker.Name != "ocr_worker" || worker.Status != "ok" || worker.Availability != "online" || !worker.AutomationAvailable || worker.FreshInstances != 1 {
		t.Fatalf("idle OCR worker should be online: %#v", worker)
	}
}

func TestSystemStatusRequiresAuthenticationAndSystemRead(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"org:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("system status without auth expected 401, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("system status without system:read expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestOCRAvailabilityUsesScopedPermissionsAndResponse(t *testing.T) {
	allowedPermissions := []string{
		"review:work",
		"review:manage",
		"submission:manage",
		"capture:manage",
		"ocr:manage",
		"grading:manage",
		"system:read",
	}
	for _, permission := range allowedPermissions {
		t.Run(permission, func(t *testing.T) {
			assertOCRAvailabilityForPermission(t, permission)
		})
	}
}

// OCR 消费权限只返回生成时间和 worker 字段，不得扩展到完整系统状态。
func assertOCRAvailabilityForPermission(t *testing.T, permission string) {
	t.Helper()
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{permission}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocr/availability", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("%s should read OCR availability, got %d %s", permission, rec.Code, rec.Body.String())
	}
	var response map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode OCR availability: %v", err)
	}
	if len(response) != 2 || response["generated_at"] == nil || response["worker"] == nil {
		t.Fatalf("OCR availability must only expose generated_at and worker: %#v", response)
	}
	var worker map[string]any
	if err := json.Unmarshal(response["worker"], &worker); err != nil {
		t.Fatalf("decode OCR worker status: %v", err)
	}
	for _, field := range []string{"name", "availability", "automation_available", "queued_tasks", "in_flight_tasks", "dead_letter_tasks", "impact_code", "impact", "action"} {
		if _, ok := worker[field]; !ok {
			t.Fatalf("OCR availability missing %s: %#v", field, worker)
		}
	}
	for _, forbidden := range []string{"service", "environment", "dependencies", "observability"} {
		if _, ok := response[forbidden]; ok {
			t.Fatalf("OCR availability exposed system field %s: %#v", forbidden, response)
		}
	}
}

func TestOCRAvailabilityRequiresAuthenticationAndConsumerPermission(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"org:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocr/availability", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("OCR availability without auth expected 401, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/ocr/availability", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("OCR availability without a consumer permission expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestOCRConsumerStillCannotReadFullSystemStatus(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"review:work"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverLogin(t, router)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ocr/availability", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("review worker should read OCR availability, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker must not read full system status, got %d %s", rec.Code, rec.Body.String())
	}
}

package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/deps"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func testConfig() config.Config {
	return config.Config{
		Service: config.ServiceConfig{
			Name:             "api-gateway-test",
			Environment:      "test",
			Host:             "127.0.0.1",
			Port:             0,
			LogLevel:         "error",
			ReadinessTimeout: 100 * time.Millisecond,
			ShutdownTimeout:  time.Second,
		},
		Auth: config.AuthConfig{SessionTTL: time.Hour},
		Security: config.SecurityConfig{
			MaxHeaderBytes:      1 << 20,
			MaxRequestBodyBytes: 2 * 1024 * 1024,
			CORSAllowedOrigins:  []string{"http://127.0.0.1:5174"},
			CORSAllowedMethods:  []string{"GET", "POST", "OPTIONS"},
			CORSAllowedHeaders:  []string{"Authorization", "Content-Type", "X-Request-ID", "X-Trace-ID"},
		},
		Observability: config.ObservabilityConfig{
			SlowRequestThreshold: time.Second,
		},
		AIService: config.AIServiceConfig{
			ModelVersion:      "Qwen/Qwen3-4B-GGUF:Q4_K_M",
			ProviderKey:       "local",
			DeploymentKey:     "local-qwen3-4b-q4-k-m",
			AdapterType:       "local_llama_cpp",
			DeploymentRegion:  "on_premise",
			CapabilityProfile: "local-pilot-v1",
		},
	}
}

func testAuthStore(t *testing.T) *auth.MemoryStore {
	t.Helper()
	return testAuthStoreWithPermissions(t, []string{"system:read", "org:manage", "student:import", "exam:manage"})
}

func testAuthStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("secret123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "user-1",
			TenantID:    "tenant-1",
			TenantCode:  "demo",
			Username:    "teacher",
			DisplayName: "Teacher",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func TestHealth(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID header")
	}
	if rec.Header().Get("X-Trace-ID") == "" {
		t.Fatal("missing X-Trace-ID header")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("missing security headers: %#v", rec.Header())
	}
}

func TestModelGovernanceRoutesEnforceSeparateReadAndManagePermissions(t *testing.T) {
	authStore := testAuthStoreWithPermissions(t, []string{"model:read", "model:policy:manage"})
	router := NewRouter(
		testConfig(),
		logger.New(io.Discard, "error"),
		nil,
		authStore,
		org.NewMemoryStore(),
		exam.NewMemoryStore(),
		paper.NewMemoryStore(),
	)
	token := serverLogin(t, router)

	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/model-providers", nil)
	listRequest.Header.Set("Authorization", "Bearer "+token)
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK ||
		!strings.Contains(listResponse.Body.String(), `"providers":[]`) ||
		strings.Contains(listResponse.Body.String(), `"credential_ref":`) {
		t.Fatalf("governed provider read returned %d: %s", listResponse.Code, listResponse.Body.String())
	}

	approvalListRequest := httptest.NewRequest(http.MethodGet, "/api/v1/model-sandbox-approvals", nil)
	approvalListRequest.Header.Set("Authorization", "Bearer "+token)
	approvalListResponse := httptest.NewRecorder()
	router.ServeHTTP(approvalListResponse, approvalListRequest)
	if approvalListResponse.Code != http.StatusOK {
		t.Fatalf("model read permission did not allow sandbox approval history: %d %s",
			approvalListResponse.Code, approvalListResponse.Body.String())
	}

	evaluationListRequest := httptest.NewRequest(http.MethodGet, "/api/v1/model-evaluation-runs", nil)
	evaluationListRequest.Header.Set("Authorization", "Bearer "+token)
	evaluationListResponse := httptest.NewRecorder()
	router.ServeHTTP(evaluationListResponse, evaluationListRequest)
	if evaluationListResponse.Code != http.StatusOK {
		t.Fatalf("model read permission did not allow evaluation history: %d %s",
			evaluationListResponse.Code, evaluationListResponse.Body.String())
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/model-providers", strings.NewReader(`{}`))
	createRequest.Header.Set("Authorization", "Bearer "+token)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("model read permission unexpectedly allowed provider management: %d %s", createResponse.Code, createResponse.Body.String())
	}

	approvalCreateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/model-sandbox-approvals", strings.NewReader(`{}`))
	approvalCreateRequest.Header.Set("Authorization", "Bearer "+token)
	approvalCreateRequest.Header.Set("Content-Type", "application/json")
	approvalCreateResponse := httptest.NewRecorder()
	router.ServeHTTP(approvalCreateResponse, approvalCreateRequest)
	if approvalCreateResponse.Code != http.StatusForbidden {
		t.Fatalf("model read permission unexpectedly allowed sandbox approval management: %d %s",
			approvalCreateResponse.Code, approvalCreateResponse.Body.String())
	}

	evaluationCreateRequest := httptest.NewRequest(http.MethodPost, "/api/v1/model-evaluation-runs", strings.NewReader(`{}`))
	evaluationCreateRequest.Header.Set("Authorization", "Bearer "+token)
	evaluationCreateRequest.Header.Set("Content-Type", "application/json")
	evaluationCreateResponse := httptest.NewRecorder()
	router.ServeHTTP(evaluationCreateResponse, evaluationCreateRequest)
	if evaluationCreateResponse.Code != http.StatusForbidden {
		t.Fatalf("policy management permission unexpectedly allowed model evaluation management: %d %s",
			evaluationCreateResponse.Code, evaluationCreateResponse.Body.String())
	}

	policyRequest := httptest.NewRequest(http.MethodPut, "/api/v1/model-policy", strings.NewReader(`{
	  "display_name":"Default local-only policy",
	  "mode":"local_only",
	  "external_enabled":false,
	  "text_export_enabled":false,
	  "image_export_enabled":false,
	  "allowed_deployments":[],
	  "max_cost_micros_per_question":0,
	  "max_cost_micros_per_exam":0,
	  "fallback_mode":"manual_only",
	  "expected_version":1,
	  "reason":"confirm tenant local-only policy"
	}`))
	policyRequest.Header.Set("Authorization", "Bearer "+token)
	policyRequest.Header.Set("Content-Type", "application/json")
	policyResponse := httptest.NewRecorder()
	router.ServeHTTP(policyResponse, policyRequest)
	if policyResponse.Code != http.StatusOK || !strings.Contains(policyResponse.Body.String(), `"version":2`) {
		t.Fatalf("model policy permission returned %d: %s", policyResponse.Code, policyResponse.Body.String())
	}
}

func TestModelEvaluationManagePermissionAllowsMutationRoute(t *testing.T) {
	authStore := testAuthStoreWithPermissions(t, []string{"model:evaluation:manage"})
	router := NewRouter(
		testConfig(),
		logger.New(io.Discard, "error"),
		nil,
		authStore,
		org.NewMemoryStore(),
		exam.NewMemoryStore(),
		paper.NewMemoryStore(),
	)
	token := serverLogin(t, router)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/model-evaluation-runs", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("evaluation management permission did not reach strict request validation: %d %s",
			response.Code, response.Body.String())
	}
}

func TestRequestTraceHeadersAndAccessLog(t *testing.T) {
	var logs bytes.Buffer
	router := NewRouter(testConfig(), logger.New(&logs, "info"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("X-Request-ID", "018f47ad-9d77-7c71-b1f6-7d5d171ac342")
	req.Header.Set("X-Trace-ID", "4bf92f3577b34da6a3ce929d0e0e4736")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Request-ID"); got != "018f47ad-9d77-7c71-b1f6-7d5d171ac342" {
		t.Fatalf("expected propagated request id, got %q", got)
	}
	if got := rec.Header().Get("X-Trace-ID"); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("expected propagated trace id, got %q", got)
	}
	var payload map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &payload); err != nil {
		t.Fatalf("decode access log: %v; raw=%s", err, logs.String())
	}
	if payload["request_id"] != "018f47ad-9d77-7c71-b1f6-7d5d171ac342" || payload["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("access log missing correlation ids: %#v", payload)
	}
	if payload["event"] != "api_request" || payload["log_stream"] != "system" {
		t.Fatalf("access log must identify system API request event: %#v", payload)
	}
}

func TestSystemInfo(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/info", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, capability := range []string{"exam_management", "paper_metadata", "question_config", "rubric_versioning", "paper_config_validation", "file_upload", "object_storage", "private_file_download", "submission_collection", "submission_pages", "submission_quality_gate", "ocr_task_management", "ocr_result_ingestion", "answer_segmentation_metadata", "answer_segment_manual_review", "agent_orchestration_control_plane", "agent_task_management", "agent_task_retry", "agent_human_review_trigger", "answer_segment_answer_capture", "rule_based_objective_grading", "ai_grade_recording", "grading_low_confidence_review_trigger", "subjective_ai_grading_interface", "mock_llm_grading_adapter", "subjective_ai_grade_failure_recording", "model_governance_api", "model_secret_reference_probe", "local_model_baseline_registry", "offline_model_evaluation", "rule_based_evidence_verification", "evidence_agent_job_recording", "evidence_failure_review_trigger", "human_review_task_management", "human_grade_recording", "review_assignment_workflow", "double_mark_policy_config", "double_mark_review_sessions", "arbitration_task_management", "final_grade_recording", "submission_grade_aggregation", "grade_confirmation_workflow", "grade_publish_quality_gate", "published_student_grade_lookup", "grade_csv_export_with_watermark", "student_appeal_submission", "appeal_review_workflow", "score_adjustment_audit_trail", "appeal_statistics", "student_learning_report", "exam_report_overview", "class_learning_report", "question_item_analysis", "grading_quality_report", "report_csv_export"} {
		if !strings.Contains(body, `"`+capability+`"`) {
			t.Fatalf("system info must disclose %s capability: %s", capability, body)
		}
	}
	if !strings.Contains(body, `"not_implemented"`) || !strings.Contains(body, `"ocr_engine_inference"`) || !strings.Contains(body, `"agent_worker_runtime"`) || !strings.Contains(body, `"real_subjective_model_inference"`) {
		t.Fatalf("system info must disclose remaining unimplemented capabilities: %s", body)
	}
	implementedIndex := strings.Index(body, `"implemented"`)
	notImplementedIndex := strings.Index(body, `"not_implemented"`)
	workerIndex := strings.Index(body, `"agent_worker_runtime"`)
	if workerIndex < implementedIndex || workerIndex > notImplementedIndex {
		t.Fatalf("agent_worker_runtime must be disclosed as implemented: %s", body)
	}
}

func TestReadyHealthy(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), []deps.Checker{
		deps.StaticChecker{CheckerName: "postgres"},
		deps.StaticChecker{CheckerName: "redis"},
	}, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"ready"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestReadyUnhealthy(t *testing.T) {
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), []deps.Checker{
		deps.StaticChecker{CheckerName: "postgres", Err: errors.New("down")},
	}, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	req.Header.Set("Authorization", "Bearer "+serverLogin(t, router))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"status":"not_ready"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

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
	router := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"system:read", "ocr:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	token := serverLogin(t, router)

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
		})
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

func TestWorkerRuntimeSeparatesReadAndExecutePermissions(t *testing.T) {
	readOnlyRouter := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"system:read"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	readOnlyToken := serverLogin(t, readOnlyRouter)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/worker/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	rec := httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("system reader should read worker metrics, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", strings.NewReader(`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"worker-1","limit":1,"lease_seconds":60}`))
	req.Header.Set("Authorization", "Bearer "+readOnlyToken)
	rec = httptest.NewRecorder()
	readOnlyRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("system reader must not claim worker tasks, got %d %s", rec.Code, rec.Body.String())
	}

	executeRouter := NewRouter(testConfig(), logger.New(io.Discard, "error"), nil, testAuthStoreWithPermissions(t, []string{"ocr:manage"}), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	executeToken := serverLogin(t, executeRouter)
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
	if !strings.Contains(body, `"error"`) || !strings.Contains(body, `"code":"not_found"`) {
		t.Fatalf("expected unified error response, got %s", body)
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

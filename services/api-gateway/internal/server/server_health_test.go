package server

import (
	"bytes"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
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
	"time"
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
		ModelSecrets: config.ModelSecretConfig{
			MasterKey:         "memory-e2e-model-credential-master-key",
			ReviewEvidenceKey: "memory-e2e-independent-review-evidence-key",
		},
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

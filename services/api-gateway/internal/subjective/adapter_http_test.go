package subjective

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

const adapterTestToken = "test-service-token-with-at-least-32-characters"

func TestHTTPAdapterSendsGovernedIdentityFreeContract(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+adapterTestToken {
			t.Fatalf("missing service auth: %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Idempotency-Key") != "sg-test-request-0001" {
			t.Fatalf("unexpected idempotency key: %q", r.Header.Get("Idempotency-Key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeAgentSuccess(t, w, received["request_id"].(string))
	}))
	defer server.Close()

	adapter := testHTTPAdapter(server.URL, 0)
	input := validHTTPAdapterInput()
	output, err := adapter.Grade(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"tenant_id", "student_id", "student_name", "student_no", "final_score", "published_score", "answer_image_ref"} {
		if _, exists := received[forbidden]; exists {
			t.Fatalf("grading request leaked forbidden field %s", forbidden)
		}
	}
	if received["subject"] != "chinese" || received["grade_level"] != "junior" || received["agent_role"] != "single" {
		t.Fatalf("missing capability context: %#v", received)
	}
	constraint, ok := received["output_constraint"].(map[string]any)
	if !ok || constraint["criteria_evidence_only"] != true || constraint["allow_model_final_score"] != false || constraint["final_score_authority"] != "server_rubric_or_human_confirmation" {
		t.Fatalf("grading request must carry the server-owned score boundary: %#v", received["output_constraint"])
	}
	if output.SuggestedScore != 4 || output.Confidence != 0 || !output.NeedsHumanReview || output.Mock {
		t.Fatalf("unexpected output: %#v", output)
	}
	if output.ModelVersion != adapter.Policy().ModelVersion || output.PromptVersion != adapter.Policy().PromptVersion || output.RubricVersion != "rubric-v3" {
		t.Fatalf("version mapping failed: %#v", output)
	}
	if len(output.Evidence) != 2 || output.Evidence[0].EvidenceID != "e1" || output.Evidence[0].AnswerText == "" {
		t.Fatalf("evidence mapping failed: %#v", output.Evidence)
	}
	if raw, _ := json.Marshal(output.RawOutput); strings.Contains(string(raw), input.AnswerText) {
		t.Fatal("raw output must not duplicate complete answer text")
	}
}

func TestHTTPAdapterMapsPanelRolesWithoutExposingPrimaryIdentity(t *testing.T) {
	for _, test := range []struct{ inputRole, contractRole string }{
		{AgentRolePrimaryA, "primary"}, {AgentRolePrimaryB, "primary"}, {AgentRoleArbiter, "arbiter"},
	} {
		var received map[string]any
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&received)
			writeAgentSuccess(t, w, received["request_id"].(string))
		}))
		input := validHTTPAdapterInput()
		input.AgentRole = test.inputRole
		_, err := testHTTPAdapter(server.URL, 0).Grade(context.Background(), input)
		server.Close()
		if err != nil || received["agent_role"] != test.contractRole {
			t.Fatalf("role %s mapped to %#v with error %v", test.inputRole, received["agent_role"], err)
		}
	}
}

func TestHTTPAdapterRetriesWithSameIdempotencyKey(t *testing.T) {
	var calls atomic.Int32
	var firstKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 1 {
			firstKey = r.Header.Get("Idempotency-Key")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"schema_version":"grading-agent-v1","request_id":"sg-test-request-0001","error":{"code":"model_unavailable","message":"not ready","retryable":true}}`))
			return
		}
		if r.Header.Get("Idempotency-Key") != firstKey {
			t.Fatalf("retry changed idempotency key: first=%q next=%q", firstKey, r.Header.Get("Idempotency-Key"))
		}
		writeAgentSuccess(t, w, firstKey)
	}))
	defer server.Close()

	adapter := testHTTPAdapter(server.URL, 1)
	if _, err := adapter.Grade(context.Background(), validHTTPAdapterInput()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one retry, got %d calls", calls.Load())
	}
}

func TestHTTPAdapterDoesNotRetryModelTimeout(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"schema_version":"grading-agent-v1","request_id":"sg-test-request-0001","error":{"code":"model_timeout","message":"model exceeded request budget","retryable":true}}`))
	}))
	defer server.Close()

	if _, err := testHTTPAdapter(server.URL, 1).Grade(context.Background(), validHTTPAdapterInput()); err == nil {
		t.Fatal("model timeout must be returned as a failed grade")
	}
	if calls.Load() != 1 {
		t.Fatalf("model timeout must not trigger a second expensive inference, got %d calls", calls.Load())
	}
}

func TestHTTPAdapterReturnsSanitizedAgentFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"schema_version":"grading-agent-v1","request_id":"sg-test-request-0001","error":{"code":"capability_not_supported","message":"answer text must never be copied here","retryable":false}}`))
	}))
	defer server.Close()

	output, err := testHTTPAdapter(server.URL, 0).Grade(context.Background(), validHTTPAdapterInput())
	if err == nil || !strings.Contains(err.Error(), "capability_not_supported") || strings.Contains(err.Error(), "answer text") {
		t.Fatalf("failure was not sanitized: %v", err)
	}
	if output.RawOutput["error_code"] != "capability_not_supported" || output.NeedsHumanReview != true {
		t.Fatalf("failure metadata missing: %#v", output)
	}
}

func TestHTTPAdapterRejectsOversizedSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAgentSuccess(t, w, "sg-test-request-0001")
		_, _ = w.Write([]byte(strings.Repeat(" ", int(maxGradingAgentResponseBytes))))
	}))
	defer server.Close()

	_, err := testHTTPAdapter(server.URL, 0).Grade(context.Background(), validHTTPAdapterInput())
	if err == nil || !strings.Contains(err.Error(), "agent_response_too_large") {
		t.Fatalf("expected oversized response rejection, got %v", err)
	}
}

func TestHTTPAdapterFailsBeforeNetworkWhenGradeLevelMissing(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	input := validHTTPAdapterInput()
	input.GradeLevel = ""
	_, err := testHTTPAdapter(server.URL, 0).Grade(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "grading_context_incomplete") {
		t.Fatalf("expected incomplete context failure, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("model service was called with incomplete context")
	}
}

func TestHTTPAdapterMapsCanonicalAssessmentVocabularyToAgentContract(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeAgentSuccess(t, w, received["request_id"].(string))
	}))
	defer server.Close()

	input := validHTTPAdapterInput()
	input.Subject = "mathematics"
	input.GradeLevel = "junior"
	if _, err := testHTTPAdapter(server.URL, 0).Grade(context.Background(), input); err != nil {
		t.Fatalf("grade through canonical vocabulary bridge: %v", err)
	}
	if received["subject"] != "mathematics" || received["grade_level"] != "junior" {
		t.Fatalf("agent request did not receive mapped protocol values: %#v", received)
	}
}

func testHTTPAdapter(baseURL string, retries int) *HTTPAdapter {
	return NewHTTPAdapter(HTTPAdapterConfig{
		BaseURL:       baseURL,
		Token:         adapterTestToken,
		Timeout:       2 * time.Second,
		MaxRetries:    retries,
		ModelVersion:  "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		PromptVersion: "subjective-governed-cn-subject-routing-v6",
		MinConfidence: 0.8,
	})
}

func validHTTPAdapterInput() AdapterInput {
	confidence := 0.96
	return AdapterInput{
		RequestID:  "sg-test-request-0001",
		SegmentID:  "segment-001",
		Subject:    "chinese",
		GradeLevel: "junior",
		Question: paper.Question{
			ID:           "question-001",
			TenantID:     "tenant-must-not-leak",
			QuestionNo:   "Q1",
			QuestionType: "short_answer",
			Score:        4,
			Stem:         "请概括文中主人公选择留下的原因。",
		},
		Rubric: paper.Rubric{
			ID:         "rubric-001",
			QuestionID: "question-001",
			Version:    "rubric-v3",
			MaxScore:   4,
			Points: []paper.RubricPoint{
				{ID: "p1", Description: "指出主人公对家乡有责任感", Score: 2, Required: true},
				{ID: "p2", Description: "指出主人公希望帮助孩子继续读书", Score: 2, Required: true},
			},
		},
		AnswerText:     "因为他对家乡有责任感，也希望帮助村里的孩子继续读书。",
		AnswerImageRef: map[string]any{"student_name": "must not leak"},
		OCRConfidence:  &confidence,
		PromptGuard:    PromptGuard{StudentAnswerIsUntrusted: true, Signals: []string{}},
		OutputConstraint: aieligibility.OutputConstraint{
			CriteriaEvidenceOnly: true, AllowModelFinalScore: false, FinalScoreAuthority: "server_rubric_or_human_confirmation",
		},
	}
}

func writeAgentSuccess(t *testing.T, w http.ResponseWriter, requestID string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"schema_version":  "grading-agent-v1",
		"request_id":      requestID,
		"status":          "suggestion",
		"delivery":        "teacher_suggestion",
		"suggested_score": 4,
		"max_score":       4,
		"confidence":      0,
		"matched_points": []any{
			map[string]any{"rubric_point_id": "p1", "label": "指出主人公对家乡有责任感", "score": 2, "evidence_ids": []string{"e1"}},
			map[string]any{"rubric_point_id": "p2", "label": "指出主人公希望帮助孩子继续读书", "score": 2, "evidence_ids": []string{"e2"}},
		},
		"missing_points": []any{},
		"deductions":     []any{},
		"evidence": []any{
			map[string]any{"evidence_id": "e1", "rubric_point_id": "p1", "text_excerpt": "对家乡有责任感", "location": "answer_text", "confidence": 0.9},
			map[string]any{"evidence_id": "e2", "rubric_point_id": "p2", "text_excerpt": "帮助村里的孩子继续读书", "location": "answer_text", "confidence": 0.9},
		},
		"risk_flags":         []string{"score_needs_review", "human_review_required"},
		"needs_human_review": true,
		"student_feedback":   "待教师复核。",
		"teacher_note":       "本地模型置信度尚未校准。",
		"model_version":      "Qwen/Qwen3-4B-GGUF:Q4_K_M",
		"prompt_version":     "subjective-governed-cn-subject-routing-v6",
		"rubric_version":     "rubric-v3",
		"capability_profile": "local-pilot-v1",
		"mock":               false,
		"telemetry": map[string]any{
			"adapter": "local_llama_cpp", "provider": "local",
			"deployment": "local-qwen3-4b-q4-k-m", "region": "on_premise",
			"attempts": 1, "repair_attempted": false, "prior_error_codes": []string{}, "elapsed_ms": 100,
		},
	}
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

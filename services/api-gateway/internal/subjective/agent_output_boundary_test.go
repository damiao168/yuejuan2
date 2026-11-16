package subjective

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func TestAgentSuccessResponseStillRequiresExactScoringContract(t *testing.T) {
	for _, tc := range []struct {
		name             string
		changes          map[string]any
		questionType     string
		appendSecondJSON bool
		errorCode        string
	}{
		{name: "short answer at maximum score"},
		{name: "essay in shadow mode", questionType: "essay", changes: map[string]any{"delivery": "shadow_only"}},
		{name: "stale rubric version", changes: map[string]any{"rubric_version": "rubric-v2"}, errorCode: "agent_response_contract_mismatch"},
		{name: "human review disabled", changes: map[string]any{"needs_human_review": false}, errorCode: "agent_response_contract_mismatch"},
		{name: "unrecognized delivery", changes: map[string]any{"delivery": "auto_publish"}, errorCode: "agent_delivery_invalid"},
		{name: "essay in teacher suggestion mode", questionType: "essay", errorCode: "agent_delivery_invalid"},
		{name: "deduction outside frozen rubric", changes: map[string]any{"deductions": []any{map[string]any{"score": 1}}}, errorCode: "agent_deductions_not_allowed"},
		{name: "score above question maximum", changes: map[string]any{"suggested_score": 4.01}, errorCode: "agent_score_invalid"},
		{name: "unauthorized confidence", changes: map[string]any{"confidence": .99}, errorCode: "agent_score_invalid"},
		{name: "unrecognized risk flag", changes: map[string]any{"risk_flags": []string{"auto_confirm"}}, errorCode: "agent_risk_flag_invalid"},
		{name: "second JSON object", appendSecondJSON: true, errorCode: "agent_response_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := validHTTPAdapterInput()
			if tc.questionType != "" {
				input.Question.QuestionType = tc.questionType
			}
			fixture := httptest.NewRecorder()
			writeAgentSuccess(t, fixture, input.RequestID)
			var response map[string]any
			if err := json.Unmarshal(fixture.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			for key, value := range tc.changes {
				response[key] = value
			}
			body, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if tc.appendSecondJSON {
				body = append(body, ` {}`...)
			}
			var calls atomic.Int32
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			defer agent.Close()

			// HTTP 成功不能覆盖量规、题型与人工复核约束。已收到的越界结果也不能自动再发一次。
			output, err := testHTTPAdapter(agent.URL, 1).Grade(t.Context(), input)
			if calls.Load() != 1 {
				t.Fatalf("unexpected Agent calls: %d", calls.Load())
			}
			if tc.errorCode == "" {
				if err != nil || output.SuggestedScore != input.Question.Score || !output.NeedsHumanReview ||
					output.DeliveryMode != response["delivery"] {
					t.Fatalf("valid suggestion was not preserved for review: score=%v mode=%q err=%v",
						output.SuggestedScore, output.DeliveryMode, err)
				}
				return
			}
			var agentErr *GradingAgentError
			if !errors.As(err, &agentErr) || agentErr.Code != tc.errorCode || agentErr.Retryable {
				t.Fatalf("unexpected failure: %v, want %q and no retry", err, tc.errorCode)
			}
			if output.RequestID != input.RequestID || output.SuggestedScore != 0 || !output.NeedsHumanReview ||
				len(output.MatchedPoints) != 0 || len(output.Evidence) != 0 || output.RawOutput["error_code"] != tc.errorCode {
				t.Fatalf("invalid agent suggestion escaped as score or evidence: %#v", output)
			}
		})
	}
}

func TestDirectGradePersistsAgentFailureAndDoesNotRepeatPaidRequest(t *testing.T) {
	adapterInput := validHTTPAdapterInput()
	value := Context{
		SegmentID: adapterInput.SegmentID, AnswerVersion: "frozen-answer", Subject: adapterInput.Subject,
		GradeLevel: adapterInput.GradeLevel, Question: adapterInput.Question, Rubric: adapterInput.Rubric,
		AnswerText: adapterInput.AnswerText, OCRConfidence: adapterInput.OCRConfidence,
	}
	var calls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"schema_version": gradingAgentSchemaVersion,
			"request_id":     r.Header.Get("Idempotency-Key"),
			"error":          map[string]any{"code": "provider_unavailable", "message": "synthetic private provider message", "retryable": false},
		})
	}))
	defer agent.Close()
	adapter := testHTTPAdapter(agent.URL, 0)
	store := NewMemoryStore()
	store.AddContext(workerServiceTenant, value.SegmentID, value)
	handler := NewHandler(store, adapter, nil)
	body := GradeRequest{IdempotencyKey: "teacher-retry-same-request"}
	user := auth.User{ID: "teacher-1", TenantID: workerServiceTenant}
	for range 2 {
		response := httptest.NewRecorder()
		handler.Grade(response, subjectiveTestRequest(t, http.MethodPost, user, body, map[string]string{"id": value.SegmentID}))
		if response.Code != http.StatusServiceUnavailable || response.Header().Get(httpx.ErrorCodeHeader) != "ai_service_unavailable" ||
			!strings.Contains(response.Body.String(), "请继续人工评分") || strings.Contains(response.Body.String(), "synthetic private provider message") {
			t.Fatalf("unexpected teacher-facing failure: %d %s", response.Code, response.Body)
		}
	}
	requestID, err := stableRequestID(workerServiceTenant, value, adapter.Policy(), body.IdempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetOrCreateRun(t.Context(), workerServiceTenant, user.ID, runInputFor(value, "", adapter.Policy(), requestID))
	if err != nil || run.Status != RunFailed || run.ErrorCode != "ai_service_unavailable" || run.GradeID != "" || calls.Load() != 1 {
		t.Fatalf("failed request was not persisted or called Agent twice: run=%+v calls=%d err=%v", run, calls.Load(), err)
	}
	if _, err := store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, requestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed Agent request created a grade: %v", err)
	}
}

func TestMathAgentRetryPreservesFrozenEvidenceRequest(t *testing.T) {
	input := validMathV2AdapterInput()
	input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
	success := httptest.NewRecorder()
	writeMathAgentV2Success(t, success, nil)
	type attempt struct{ path, idempotencyKey, body string }
	requests := make(chan attempt, 2)
	requestID := input.RequestID
	var calls atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "could not read request", http.StatusBadRequest)
			return
		}
		select {
		case requests <- attempt{r.URL.Path, r.Header.Get("Idempotency-Key"), string(body)}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, `{"schema_version":"grading-agent-v2","request_id":%q,"error":{"code":"model_unavailable","message":"not ready","retryable":true}}`, requestID)
			return
		}
		_, _ = w.Write(success.Body.Bytes())
	}))
	defer agent.Close()

	adapter := &HTTPAdapterV2{base: testHTTPAdapter(agent.URL, 1)}
	output, err := adapter.Grade(t.Context(), input)
	if err != nil || calls.Load() != 2 {
		t.Fatalf("retryable outage did not recover: calls=%d err=%v", calls.Load(), err)
	}
	first, second := <-requests, <-requests
	if first != second || first.path != "/grading/grade-v2" || first.idempotencyKey != requestID ||
		!strings.Contains(first.body, `"math_evidence"`) {
		t.Fatalf("retry did not preserve the frozen evidence request: path=%q key=%q", first.path, first.idempotencyKey)
	}
	if len(output.MathCandidates) != 1 || output.SuggestedScore != 0 || output.MathScore != nil || !output.NeedsHumanReview {
		t.Fatalf("recovered response crossed the candidate-only boundary: %#v", output)
	}
}

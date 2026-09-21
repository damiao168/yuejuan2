package subjective

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPAdapterV2SendsSanitizedEvidenceAndReturnsOnlyCandidates(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/grading/grade-v2" {
			t.Fatalf("unexpected route: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+adapterTestToken || r.Header.Get("Idempotency-Key") != "sg-test-request-0001" {
			t.Fatalf("missing governed request headers: %#v", r.Header)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		writeMathAgentV2Success(t, w, nil)
	}))
	defer server.Close()

	input := validMathV2AdapterInput()
	input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
	output, err := testHTTPAdapterV2(server.URL).Grade(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(received)
	if !strings.Contains(string(body), `"math_evidence"`) || strings.Contains(string(body), `"ast"`) || strings.Contains(string(body), `"details"`) {
		t.Fatalf("v2 request did not preserve the sanitized math boundary: %s", body)
	}
	if _, exists := received["suggested_score"]; exists {
		t.Fatal("request exposed a model-owned suggested score")
	}
	if output.SuggestedScore != 0 || output.MathScore != nil || len(output.MathCandidates) != 1 || output.MathCandidates[0].RubricPointID != "p1" {
		t.Fatalf("model response escaped candidate-only mapping: %#v", output)
	}
}

func TestHTTPAdapterV2RejectsScoreInjectionAndInconsistentAlternativeRisk(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra map[string]any
	}{
		{name: "score injection", extra: map[string]any{"suggested_score": 999}},
		{name: "alternative risk missing", extra: map[string]any{"alternative_solution_candidate": true}},
		{name: "missing candidate array", extra: map[string]any{"criterion_candidates": nil}},
		{name: "missing alternative assertion", extra: map[string]any{"alternative_solution_candidate": nil}},
		{name: "null mock assertion", extra: map[string]any{"mock": json.RawMessage("null")}},
		{name: "duplicate risk", extra: map[string]any{"risk_flags": []string{"human_review_required", "human_review_required"}}},
		{name: "missing confidence", extra: map[string]any{"criterion_candidates": []any{map[string]any{"rubric_point_id": "p1", "status": "uncertain", "evidence_ids": []string{}, "reason_code": "unsure"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeMathAgentV2Success(t, w, tc.extra)
			}))
			defer server.Close()
			input := validMathV2AdapterInput()
			input.ActiveCrop = ptrResolvedActiveCrop(validResolvedActiveCrop(t))
			_, err := testHTTPAdapterV2(server.URL).Grade(context.Background(), input)
			if err == nil {
				t.Fatal("unsafe v2 response was accepted")
			}
		})
	}
}

func writeMathAgentV2Success(t *testing.T, w http.ResponseWriter, extra map[string]any) {
	t.Helper()
	payload := map[string]any{
		"schema_version": "grading-agent-v2", "request_id": "sg-test-request-0001", "status": "candidate_mapping", "delivery": "teacher_suggestion",
		"criterion_candidates":           []any{map[string]any{"rubric_point_id": "p1", "status": "supported", "evidence_ids": []string{"s1", "f1"}, "confidence": .91, "reason_code": "semantic_alignment"}},
		"alternative_solution_candidate": false, "risk_flags": []string{"human_review_required"}, "needs_human_review": true,
		"model_version": "Qwen/Qwen3-4B-GGUF:Q4_K_M", "prompt_version": "subjective-governed-cn-subject-routing-v6", "rubric_version": "rubric-v3",
		"capability_profile": "local-pilot-v1", "mock": false,
		"telemetry": map[string]any{"adapter": "local_llama_cpp", "provider": "local", "deployment": "local-qwen3-4b-q4-k-m", "region": "on_premise", "attempts": 1, "repair_attempted": false, "prior_error_codes": []string{}, "elapsed_ms": 1},
	}
	for key, value := range extra {
		if value == nil {
			delete(payload, key)
			continue
		}
		payload[key] = value
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		t.Fatal(err)
	}
}

func ptrResolvedActiveCrop(value ResolvedActiveCrop) *ResolvedActiveCrop { return &value }

func testHTTPAdapterV2(baseURL string) *HTTPAdapterV2 {
	return &HTTPAdapterV2{base: testHTTPAdapter(baseURL, 0)}
}

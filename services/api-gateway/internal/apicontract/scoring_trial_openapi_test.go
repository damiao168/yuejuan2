package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoringTrialOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document, "paths")
	base := "/api/v1/scoring-runs/{runId}/ai-trials"
	operations := []struct {
		path, method, id, status, response string
	}{
		{base, "get", "listScoringRunAITrials", "200", "ScoringTrialListResponse"},
		{base, "post", "createScoringRunAITrial", "201", "ScoringTrialResponse"},
		{base + "/{trialId}/enqueue", "post", "enqueueScoringRunAITrial", "200", "BatchEnqueueResponse"},
		{base + "/{trialId}/decision", "post", "decideScoringRunAITrial", "200", "ScoringTrialResponse"},
	}
	for _, expected := range operations {
		assertAuthenticatedOperationContract(t, paths, expected.path, expected.method, expected.id)
		operation := object(t, object(t, paths, expected.path), expected.method)
		if !strings.Contains(operation["description"].(string), "grading:manage") {
			t.Fatalf("%s must document the existing grading management permission", expected.id)
		}
		response := object(t, object(t, operation, "responses"), expected.status)
		schema := object(t, object(t, object(t, response, "content"), "application/json"), "schema")
		if schema["$ref"] != "#/components/schemas/"+expected.response {
			t.Fatalf("%s success response = %#v", expected.id, schema)
		}
		for _, parameter := range operation["parameters"].([]any) {
			if object(t, parameter.(map[string]any), "schema")["format"] != "uuid" {
				t.Fatalf("%s must use UUID resource identities", expected.id)
			}
		}
	}
	if _, exists := object(t, object(t, paths, base+"/{trialId}/enqueue"), "post")["requestBody"]; exists {
		t.Fatal("trial enqueue must recover the server-owned batch without client input")
	}

	schemas := object(t, object(t, document, "components"), "schemas")
	create := object(t, schemas, "CreateScoringTrialRequest")
	assertRequired(t, create, []string{"command_id", "question_id", "samples"})
	samples := object(t, object(t, create, "properties"), "samples")
	if samples["minItems"] != float64(5) || samples["maxItems"] != float64(10) {
		t.Fatal("trial sample count must be bounded to 5–10")
	}
	assertStringEnum(t, schemas, "ScoringTrialSampleRole", []string{"representative", "boundary", "incomplete_evidence"})
	decision := object(t, schemas, "DecideScoringTrialRequest")
	assertRequired(t, decision, []string{"decision", "reason", "expected_revision"})
	revision := object(t, object(t, decision, "properties"), "expected_revision")
	if revision["type"] != "integer" || revision["format"] != "int64" {
		t.Fatal("trial decisions must carry the persisted integer revision")
	}

	trial := object(t, schemas, "ScoringTrial")
	assertRequired(t, trial, []string{"id", "run_id", "exam_id", "question_id", "revision", "fingerprint", "sample_batch_id", "samples", "remaining_segment_ids"})
	for _, hidden := range []string{"tenant_id", "request_hash"} {
		if _, exists := object(t, trial, "properties")[hidden]; exists {
			t.Fatalf("trial response must not expose %s", hidden)
		}
	}
	result := object(t, schemas, "ScoringTrialResult")
	assertRequired(t, result, []string{"status", "evidence", "risk_flags"})
	// 失败或待处理的样本不保证有分数，生成客户端不能要求调用方把缺失建议补成零分。
	for _, required := range result["required"].([]any) {
		if required == "suggested_score" {
			t.Fatal("unavailable trial suggestions must remain optional")
		}
	}
	evidence := object(t, object(t, object(t, result, "properties"), "evidence"), "items")
	if evidence["$ref"] != "#/components/schemas/SubjectiveGradeEvidence" {
		t.Fatal("trial results must retain the governed grading evidence contract")
	}
}

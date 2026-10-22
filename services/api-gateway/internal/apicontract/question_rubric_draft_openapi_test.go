package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestQuestionRubricDraftOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	operation := object(t, object(t, object(t, document, "paths"), "/api/v1/exams/{examId}/questions/{questionId}/rubric-draft"), "post")
	if operation["operationId"] != "suggestQuestionMathRubricDraft" {
		t.Fatalf("formal question operation ID = %#v", operation["operationId"])
	}
	parameters, ok := operation["parameters"].([]any)
	if !ok || !hasRef(parameters, "#/components/parameters/ExamId") || !hasRef(parameters, "#/components/parameters/QuestionId") {
		t.Fatal("formal question suggestions must bind both exam and question path parameters")
	}
	body := object(t, operation, "requestBody")
	if body["required"] != true {
		t.Fatal("current question snapshot must be required")
	}
	requestSchema := object(t, object(t, object(t, body, "content"), "application/json"), "schema")
	if requestSchema["$ref"] != "#/components/schemas/QuestionMathRubricDraftRequest" {
		t.Fatalf("request schema = %#v", requestSchema)
	}
	responses := object(t, operation, "responses")
	for _, status := range []string{"200", "400", "401", "403", "404", "409", "500", "502", "503"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("formal question suggestion response %s is missing", status)
		}
	}
	responseSchema := object(t, object(t, object(t, object(t, responses, "200"), "content"), "application/json"), "schema")
	if responseSchema["$ref"] != "#/components/schemas/MathRubricDraftResponse" {
		t.Fatalf("response schema must reuse review-only math suggestions: %#v", responseSchema)
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	request := object(t, schemas, "QuestionMathRubricDraftRequest")
	assertRequired(t, request, []string{"expected_question"})
	assertPropertyRef(t, request, "expected_question", "#/components/schemas/QuestionMathRubricDraftExpectedQuestion")
	segments := assertProperty(t, request, "solution_segments")
	segmentBranches, ok := segments["anyOf"].([]any)
	if !ok || len(segmentBranches) != 2 || segmentBranches[1].(map[string]any)["type"] != "null" {
		t.Fatalf("null partition must preserve the original solution steps: %#v", segments)
	}
	segmentArray, ok := segmentBranches[0].(map[string]any)
	if !ok || segmentArray["type"] != "array" || segmentArray["minItems"] != float64(1) || segmentArray["maxItems"] != float64(500) || object(t, segmentArray, "items")["$ref"] != "#/components/schemas/QuestionRubricSolutionSegment" {
		t.Fatalf("teacher partition limits or item binding missing: %#v", segments)
	}
	assertRequired(t, object(t, schemas, "QuestionRubricSolutionSegment"), []string{"spans"})
	assertRequired(t, object(t, schemas, "QuestionRubricSolutionSpan"), []string{"source_id", "start", "end"})

	// 浏览器原样回传完整题目；严格 JSON 解码不能被只列少数字段的 SDK 契约截断。
	for _, binding := range []struct {
		name string
		kind reflect.Type
	}{
		{"QuestionMathRubricDraftRequest", reflect.TypeFor[paper.QuestionMathRubricDraftInput]()},
		{"QuestionRubricSolutionSegment", reflect.TypeFor[paper.QuestionRubricSolutionSegment]()},
		{"QuestionRubricSolutionSpan", reflect.TypeFor[paper.QuestionRubricSolutionSpan]()},
		{"QuestionMathRubricDraftExpectedQuestion", reflect.TypeFor[paper.Question]()},
		{"QuestionMathRubricDraftAnswerKey", reflect.TypeFor[paper.AnswerKey]()},
		{"QuestionMathRubricDraftSolution", reflect.TypeFor[paper.QuestionSolution]()},
		{"QuestionMathRubricDraftRubric", reflect.TypeFor[paper.Rubric]()},
		{"QuestionRubricDraftPoint", reflect.TypeFor[paper.RubricPoint]()},
		{"QuestionRubricDraftEvidenceRequirement", reflect.TypeFor[paper.EvidenceRequirement]()},
	} {
		t.Run(binding.name, func(t *testing.T) {
			schema := object(t, schemas, binding.name)
			if schema["additionalProperties"] != false {
				t.Fatal("snapshot objects must reject unknown fields like the Go decoder")
			}
			properties := object(t, schema, "properties")
			expected := map[string]bool{}
			for index := range binding.kind.NumField() {
				field := binding.kind.Field(index)
				name := strings.Split(field.Tag.Get("json"), ",")[0]
				if name == "" || name == "-" {
					continue
				}
				expected[name] = true
				if _, ok := properties[name]; !ok {
					t.Errorf("missing current JSON field %s.%s", binding.name, name)
				}
			}
			for name := range properties {
				if !expected[name] {
					t.Errorf("contract field %s.%s is rejected by the current Go decoder", binding.name, name)
				}
			}
		})
	}
	for _, field := range []string{"options", "knowledge_points"} {
		property := assertProperty(t, object(t, schemas, "QuestionMathRubricDraftExpectedQuestion"), field)
		branches, ok := property["anyOf"].([]any)
		if !ok || len(branches) != 2 || branches[1].(map[string]any)["type"] != "null" {
			t.Fatalf("%s must preserve the stored nil slice in a snapshot", field)
		}
	}
}

package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPaperImportOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("OpenAPI contract must be valid JSON: %v", err)
	}
	paths := object(t, document, "paths")
	operations := []struct{ path, method, id string }{
		{"/api/v1/exams/{examId}/paper-imports", "get", "listPaperImports"},
		{"/api/v1/exams/{examId}/paper-imports", "post", "createPaperImport"},
		{"/api/v1/paper-imports/{id}", "get", "getPaperImport"},
		{"/api/v1/paper-imports/{id}/sources", "post", "addPaperImportSources"},
		{"/api/v1/paper-imports/{id}/sources", "put", "replacePaperImportSources"},
		{"/api/v1/paper-imports/{id}/review", "put", "savePaperImportReview"},
		{"/api/v1/paper-imports/{id}/apply", "post", "applyPaperImport"},
		{"/api/v1/paper-imports/{id}/fill-content", "post", "fillPaperImportContent"},
		{"/api/v1/paper-imports/{id}/cancel", "post", "cancelPaperImport"},
	}
	for _, expected := range operations {
		operation := object(t, object(t, paths, expected.path), expected.method)
		if operation["operationId"] != expected.id {
			t.Fatalf("%s %s operationId = %#v, want %q", expected.method, expected.path, operation["operationId"], expected.id)
		}
	}

	schemas := object(t, object(t, document, "components"), "schemas")
	createRequest := object(t, schemas, "CreatePaperImportRequest")
	assertRequired(t, createRequest, []string{"subject"})
	createProperties := object(t, createRequest, "properties")
	for _, legacyOrCurrent := range []string{"sources", "paper_file_asset_id", "answer_file_asset_id"} {
		if _, ok := createProperties[legacyOrCurrent]; !ok {
			t.Fatalf("CreatePaperImportRequest is missing compatibility field %q", legacyOrCurrent)
		}
	}
	if alternatives, ok := createRequest["anyOf"].([]any); !ok || len(alternatives) != 3 {
		t.Fatalf("CreatePaperImportRequest must accept sources or either legacy file field: %#v", createRequest["anyOf"])
	}
	assertRequired(t, object(t, schemas, "PaperImportSourceRef"), []string{"source_id", "file_asset_id", "document_index"})
	assertRequired(t, object(t, schemas, "PaperImportQuestionCandidate"), []string{"candidate_id", "source_refs", "confidence"})
	assertRequired(t, object(t, schemas, "PaperImportAnswerCandidate"), []string{"candidate_id", "source_refs", "confidence"})
	assertRequired(t, object(t, schemas, "PaperImportSolutionCandidate"), []string{"candidate_id", "raw_text", "steps", "source_refs", "confidence"})
	assertEnumContains(t, object(t, schemas, "PaperImportRole"), "rubric")
	assertEnumContains(t, object(t, object(t, object(t, schemas, "PaperImportSource"), "properties"), "detected_role"), "rubric")
	assertRequired(t, object(t, schemas, "PaperImportRubricEvidenceRequirement"), []string{"type"})
	assertRequired(t, object(t, schemas, "PaperImportRubricCandidatePoint"), []string{"id", "description"})
	assertRequired(t, object(t, schemas, "PaperImportRubricCandidate"), []string{"candidate_id", "points", "source_refs", "confidence"})
	assertPropertyRef(t, object(t, schemas, "PaperImportRubricCandidatePoint"), "evidence_requirements", "#/components/schemas/PaperImportRubricEvidenceRequirement")
	assertProperty(t, object(t, schemas, "PaperImportDraftQuestion"), "rubric_candidate_id")
	assertRequired(t, object(t, schemas, "ReviewPaperImportRequest"), []string{"expected_generation", "expected_updated_at", "questions"})
	assertRequired(t, object(t, schemas, "FillPaperImportContentRequest"), []string{"expected_generation"})
	assertRequired(t, object(t, schemas, "PaperImportContentResult"), []string{"filled_questions", "missing_answers", "skipped"})
	job := object(t, schemas, "PaperImportJob")
	assertRequired(t, job, []string{"sources", "question_candidates", "answer_candidates", "solution_candidates", "rubric_candidates", "structured_issues", "questions"})
	assertPropertyRef(t, job, "rubric_candidates", "#/components/schemas/PaperImportRubricCandidate")
}

func assertProperty(t *testing.T, schema map[string]any, property string) map[string]any {
	t.Helper()
	properties := object(t, schema, "properties")
	value, ok := properties[property].(map[string]any)
	if !ok {
		t.Fatalf("schema property %q must be an object", property)
	}
	return value
}

func assertPropertyRef(t *testing.T, schema map[string]any, property, expected string) {
	t.Helper()
	propertySchema := assertProperty(t, schema, property)
	if items, ok := propertySchema["items"].(map[string]any); ok {
		propertySchema = items
	}
	if propertySchema["$ref"] != expected {
		t.Fatalf("schema property %q ref = %#v, want %q", property, propertySchema["$ref"], expected)
	}
}

func assertEnumContains(t *testing.T, schema map[string]any, expected string) {
	t.Helper()
	values, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("schema enum must be an array: %#v", schema["enum"])
	}
	for _, value := range values {
		if value == expected {
			return
		}
	}
	t.Fatalf("schema enum %#v is missing %q", values, expected)
}

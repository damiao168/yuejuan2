package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStoryA06ReviewAnnotationOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("OpenAPI contract must be valid JSON: %v", err)
	}

	paths := object(t, document, "paths")
	operations := []struct {
		path   string
		method string
		id     string
	}{
		{"/api/v1/review-tasks/{taskId}/annotations", "get", "listReviewAnnotations"},
		{"/api/v1/review-tasks/{taskId}/annotations", "post", "createReviewAnnotation"},
		{"/api/v1/review/annotations/{annotationId}", "put", "updateReviewAnnotation"},
		{"/api/v1/review/annotations/{annotationId}", "delete", "deleteReviewAnnotation"},
	}
	// 评语模板已经退役，契约不能继续生成可调用的 SDK 入口。
	for _, removed := range []string{
		"/api/v1/review/comment-templates",
		"/api/v1/review/comment-templates/{templateId}",
		"/api/v1/review/comment-templates/{shortcut}/use",
	} {
		if _, exists := paths[removed]; exists {
			t.Fatalf("retired review comment template path remains: %s", removed)
		}
	}
	for _, expected := range operations {
		operation := object(t, object(t, paths, expected.path), expected.method)
		if operation["operationId"] != expected.id {
			t.Fatalf("%s %s operationId = %#v, want %q", expected.method, expected.path, operation["operationId"], expected.id)
		}
	}

	studentOperation := object(t, object(t, paths, "/api/v1/student/exams/{examId}/questions/{questionId}/annotations"), "get")
	if studentOperation["operationId"] != "listStudentQuestionReviewAnnotations" {
		t.Fatalf("student annotation operationId = %#v", studentOperation["operationId"])
	}
	if _, exposed := paths["/api/v1/student/submissions/{submissionId}/annotations"]; exposed {
		t.Fatal("student annotation route must not expose caller-selected submissions")
	}

	schemas := object(t, object(t, document, "components"), "schemas")
	assertRequired(t, object(t, schemas, "CanonicalImageGeometry"), []string{"coordinate_space", "x", "y", "width", "height"})
	assertRequired(t, object(t, schemas, "UpdateReviewAnnotationRequest"), []string{"type", "geometry", "content", "visibility", "expected_revision"})
	for _, removed := range []string{
		"ReviewCommentTemplate", "CreateReviewCommentTemplateRequest", "UpdateReviewCommentTemplateRequest",
		"ReviewCommentTemplateResponse", "ReviewCommentTemplateListResponse",
	} {
		if _, exists := schemas[removed]; exists {
			t.Fatalf("retired review comment template schema remains: %s", removed)
		}
	}

	studentProperties := object(t, object(t, schemas, "StudentReviewAnnotation"), "properties")
	for _, forbidden := range []string{"tenant_id", "review_task_id", "payload", "visibility", "revision", "created_by", "updated_by"} {
		if _, exists := studentProperties[forbidden]; exists {
			t.Fatalf("student annotation schema must structurally omit %q", forbidden)
		}
	}
	assertRequired(t, object(t, schemas, "StudentQuestionReviewAnnotationListResponse"), []string{"annotations"})
}

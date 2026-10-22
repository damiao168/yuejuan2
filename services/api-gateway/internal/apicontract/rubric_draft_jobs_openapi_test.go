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

func TestQuestionRubricDraftJobsOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	paths := object(t, document, "paths")
	for _, expected := range []struct {
		path, method, operation, success, response string
	}{
		{"/api/v1/exams/{examId}/questions/{questionId}/rubric-draft-jobs", "post", "createQuestionRubricDraftJob", "202", "QuestionRubricDraftJobResponse"},
		{"/api/v1/exams/{examId}/rubric-draft-jobs", "get", "listQuestionRubricDraftJobs", "200", "QuestionRubricDraftJobListResponse"},
		{"/api/v1/exams/{examId}/rubric-draft-jobs/{jobId}", "get", "getQuestionRubricDraftJob", "200", "QuestionRubricDraftJobResponse"},
		{"/api/v1/exams/{examId}/rubric-draft-jobs/{jobId}/cancel", "post", "cancelQuestionRubricDraftJob", "200", "QuestionRubricDraftJobResponse"},
	} {
		t.Run(expected.operation, func(t *testing.T) {
			operation := object(t, object(t, paths, expected.path), expected.method)
			if operation["operationId"] != expected.operation {
				t.Fatalf("operation ID = %#v", operation["operationId"])
			}
			parameters := operation["parameters"].([]any)
			if !hasRef(parameters, "#/components/parameters/ExamId") {
				t.Fatal("all job operations require an authorized exam path")
			}
			if strings.Contains(expected.path, "{questionId}") && !hasRef(parameters, "#/components/parameters/QuestionId") {
				t.Fatal("creation must bind the current formal question")
			}
			if strings.Contains(expected.path, "{jobId}") && !hasRef(parameters, "#/components/parameters/RubricDraftJobId") {
				t.Fatal("detail and cancellation must bind a job path")
			}
			if expected.method == "post" {
				found := false
				for _, value := range parameters {
					parameter := value.(map[string]any)
					if parameter["name"] == "Idempotency-Key" {
						found = true
						if parameter["in"] != "header" || parameter["required"] != false {
							t.Fatal("transport receipts are optional; task request_id remains the business identity")
						}
					}
				}
				if !found {
					t.Fatal("optional transport idempotency header must be contracted")
				}
			}
			responses := object(t, operation, "responses")
			for _, status := range []string{expected.success, "401", "403", "404", "503"} {
				if _, ok := responses[status]; !ok {
					t.Fatalf("response %s is missing", status)
				}
			}
			schema := object(t, object(t, object(t, object(t, responses, expected.success), "content"), "application/json"), "schema")
			if schema["$ref"] != "#/components/schemas/"+expected.response {
				t.Fatalf("unexpected response shape: %#v", schema)
			}
		})
	}
	create := object(t, object(t, paths, "/api/v1/exams/{examId}/questions/{questionId}/rubric-draft-jobs"), "post")
	for _, status := range []string{"400", "409", "429"} {
		if _, ok := object(t, create, "responses")[status]; !ok {
			t.Fatalf("creation response %s is missing", status)
		}
	}
	request := object(t, object(t, object(t, object(t, create, "requestBody"), "content"), "application/json"), "schema")
	if request["$ref"] != "#/components/schemas/CreateQuestionRubricDraftJobRequest" {
		t.Fatalf("creation request schema = %#v", request)
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	creation := object(t, schemas, "CreateQuestionRubricDraftJobRequest")
	assertRequired(t, creation, []string{"expected_question", "request_id"})
	for name, property := range object(t, object(t, schemas, "QuestionMathRubricDraftRequest"), "properties") {
		if !reflect.DeepEqual(object(t, creation, "properties")[name], property) {
			t.Errorf("creation must preserve the synchronous snapshot/partition rule for %s", name)
		}
	}
	if assertProperty(t, creation, "request_id")["format"] != "uuid" {
		t.Fatal("task request identity must be a UUID")
	}
	assertStringEnum(t, schemas, "QuestionRubricDraftJobStatus", []string{"queued", "running", "cancelling", "cancelled", "succeeded", "failed", "stale"})
	job := object(t, schemas, "QuestionRubricDraftJob")
	assertPropertyRef(t, job, "input", "#/components/schemas/QuestionMathRubricDraftRequest")
	assertPropertyRef(t, job, "result", "#/components/schemas/MathRubricDraftResponse")
	assertRequired(t, job, []string{"id", "exam_id", "question_id", "question_no", "status", "request_id", "input", "model_name", "attempt", "created_at", "updated_at"})
	list := object(t, schemas, "QuestionRubricDraftJobListResponse")
	if assertProperty(t, list, "concurrency")["const"] != float64(paper.RubricDraftConcurrency) {
		t.Fatal("browser-visible concurrency must match the shared backend ceiling")
	}
	if assertProperty(t, list, "jobs")["maxItems"] != float64(100) {
		t.Fatal("list contract must disclose the bounded recent-job history")
	}
	// Go 内嵌输入会展平成 JSON 字段；契约不能漏掉分段，也不能泄漏未导出的后台身份记录。
	for _, binding := range []struct {
		name string
		kind reflect.Type
	}{
		{"CreateQuestionRubricDraftJobRequest", reflect.TypeFor[paper.CreateRubricDraftJobInput]()},
		{"QuestionRubricDraftJob", reflect.TypeFor[paper.RubricDraftJob]()},
	} {
		schema := object(t, schemas, binding.name)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s must have a strict public JSON shape", binding.name)
		}
		fields := rubricJobJSONFields(binding.kind)
		properties := object(t, schema, "properties")
		for field := range fields {
			if _, ok := properties[field]; !ok {
				t.Errorf("missing %s.%s", binding.name, field)
			}
		}
		for field := range properties {
			if !fields[field] {
				t.Errorf("unknown or private contract field %s.%s", binding.name, field)
			}
		}
	}
}

func rubricJobJSONFields(kind reflect.Type) map[string]bool {
	fields := map[string]bool{}
	for index := range kind.NumField() {
		field := kind.Field(index)
		if field.Anonymous {
			for name := range rubricJobJSONFields(field.Type) {
				fields[name] = true
			}
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}

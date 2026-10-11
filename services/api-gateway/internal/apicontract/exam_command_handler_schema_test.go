package apicontract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func TestExamCommandHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	document := readOpenAPIDocument(t)
	store := exam.NewMemoryStore()
	handler := exam.NewHandler(store, auth.NewMemoryStore())
	commandID := "handler-contract-command"
	input := exam.CreateSessionInput{
		SchoolID: "school-1", GradeID: "grade-1", Name: "Contract exam", ExamType: "formal_exam",
		GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval", CommandID: commandID,
		ClassIDs: []string{"class-1"}, Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 100, DurationMinutes: 90,
			Sections: []exam.BlueprintSectionInput{{Title: "Questions", QuestionType: "single_choice", QuestionCount: 20, ScorePerQuestion: 5}}}},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := scopedContractRequest(httptest.NewRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewReader(raw)))
	request.Header.Set("Idempotency-Key", commandID)
	created := httptest.NewRecorder()
	handler.CreateExamSession(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("create handler returned %d: %s", created.Code, created.Body.String())
	}
	validateResponseBody(t, document, "/api/v1/exam-sessions", "post", "201", created.Body.Bytes())

	recoveryRequest := scopedContractRequest(httptest.NewRequest(http.MethodGet, "/api/v1/exam-sessions/commands/"+commandID, nil))
	recoveryRequest.SetPathValue("commandId", commandID)
	recovered := httptest.NewRecorder()
	handler.RecoverExamSessionCommand(recovered, recoveryRequest)
	if recovered.Code != http.StatusOK {
		t.Fatalf("recovery handler returned %d: %s", recovered.Code, recovered.Body.String())
	}
	validateResponseBody(t, document, "/api/v1/exam-sessions/commands/{commandId}", "get", "200", recovered.Body.Bytes())

	invalid := scopedContractRequest(httptest.NewRequest(http.MethodPost, "/api/v1/exam-sessions", strings.NewReader(`{}`)))
	invalidResponse := httptest.NewRecorder()
	handler.CreateExamSession(invalidResponse, invalid)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("invalid handler returned %d: %s", invalidResponse.Code, invalidResponse.Body.String())
	}
	validateResponseBody(t, document, "/api/v1/exam-sessions", "post", "400", invalidResponse.Body.Bytes())
	var envelope map[string]any
	if err := json.Unmarshal(invalidResponse.Body.Bytes(), &envelope); err != nil || envelope["request_id"] != "contract-request" || envelope["trace_id"] != "contract-trace" {
		t.Fatalf("error envelope lost correlation identifiers: %#v err=%v", envelope, err)
	}
}

func scopedContractRequest(request *http.Request) *http.Request {
	ctx := logger.WithTraceID(logger.WithRequestID(context.Background(), "contract-request"), "contract-trace")
	ctx = auth.WithUser(ctx, auth.User{ID: "actor-1", TenantID: "tenant-1"})
	ctx = auth.WithAccessScope(ctx, auth.AccessScope{TenantID: "tenant-1", TenantWide: true})
	return request.WithContext(ctx)
}

func readOpenAPIDocument(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func validateResponseBody(t *testing.T, document map[string]any, routePath, method, status string, body []byte) {
	t.Helper()
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	paths := document["paths"].(map[string]any)
	operation := paths[routePath].(map[string]any)[method].(map[string]any)
	response, ok := operation["responses"].(map[string]any)[status].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI is missing %s %s response %s", method, routePath, status)
	}
	if ref, ok := response["$ref"].(string); ok {
		response = document["components"].(map[string]any)["responses"].(map[string]any)[strings.TrimPrefix(ref, "#/components/responses/")].(map[string]any)
	}
	content := response["content"].(map[string]any)["application/json"].(map[string]any)
	if err := validateOpenAPIValue(document, content["schema"].(map[string]any), value, "response"); err != nil {
		t.Fatal(err)
	}
}

func validateOpenAPIValue(document map[string]any, schema map[string]any, value any, location string) error {
	if ref, ok := schema["$ref"].(string); ok {
		const prefix = "#/components/schemas/"
		if !strings.HasPrefix(ref, prefix) {
			return fmt.Errorf("%s uses unsupported ref %s", location, ref)
		}
		schema = document["components"].(map[string]any)["schemas"].(map[string]any)[strings.TrimPrefix(ref, prefix)].(map[string]any)
	}
	if value == nil && schema["nullable"] == true {
		return nil
	}
	if values, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range values {
			matched = matched || candidate == value
		}
		if !matched {
			return fmt.Errorf("%s value %#v is outside enum %#v", location, value, values)
		}
	}
	switch schema["type"] {
	case "object":
		return validateOpenAPIObject(document, schema, value, location)
	case "array":
		return validateOpenAPIArray(document, schema, value, location)
	default:
		return validateOpenAPIPrimitive(schema, value, location)
	}
}

// 字段存在性和额外字段限制先检查，再递归核验各已提供字段的值。
func validateOpenAPIObject(document map[string]any, schema map[string]any, value any, location string) error {
	objectValue, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("%s must be object, got %T", location, value)
	}
	properties, _ := schema["properties"].(map[string]any)
	if err := validateOpenAPIObjectFields(schema, objectValue, properties, location); err != nil {
		return err
	}
	for name, property := range properties {
		if propertyValue, exists := objectValue[name]; exists {
			if err := validateOpenAPIValue(document, property.(map[string]any), propertyValue, location+"."+name); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOpenAPIObjectFields(schema map[string]any, objectValue map[string]any, properties map[string]any, location string) error {
	requiredFields, _ := schema["required"].([]any)
	for _, required := range requiredFields {
		if _, exists := objectValue[required.(string)]; !exists {
			return fmt.Errorf("%s is missing required field %s", location, required)
		}
	}
	if schema["additionalProperties"] == false {
		for name := range objectValue {
			if _, exists := properties[name]; !exists {
				return fmt.Errorf("%s contains undeclared field %s", location, name)
			}
		}
	}
	return nil
}

func validateOpenAPIArray(document map[string]any, schema map[string]any, value any, location string) error {
	items, ok := value.([]any)
	if !ok {
		return fmt.Errorf("%s must be array, got %T", location, value)
	}
	for index, item := range items {
		if err := validateOpenAPIValue(document, schema["items"].(map[string]any), item, fmt.Sprintf("%s[%d]", location, index)); err != nil {
			return err
		}
	}
	return nil
}

func validateOpenAPIPrimitive(schema map[string]any, value any, location string) error {
	switch schema["type"] {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be string, got %T", location, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be boolean, got %T", location, value)
		}
	case "integer":
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number {
			return fmt.Errorf("%s must be integer, got %#v", location, value)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be number, got %T", location, value)
		}
	}
	return nil
}

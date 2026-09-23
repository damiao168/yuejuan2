package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformSchoolsOpenAPIContract(t *testing.T) {
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
		path      string
		operation string
		responses []string
	}{
		{"/api/v1/platform/schools", "listPlatformSchools", []string{"200", "400", "401", "403"}},
		{"/api/v1/platform/schools/{tenant_id}", "getPlatformSchool", []string{"200", "401", "403", "404"}},
		{"/api/v1/platform/schools/{tenant_id}/members", "listPlatformSchoolMembers", []string{"200", "400", "401", "403", "404"}},
		{"/api/v1/platform/schools/{tenant_id}/usage", "getPlatformSchoolUsage", []string{"200", "400", "401", "403", "404"}},
		{"/api/v1/platform/schools/{tenant_id}/model-health", "getPlatformSchoolModelHealth", []string{"200", "401", "403", "404"}},
		{"/api/v1/platform/schools/{tenant_id}/activity", "getPlatformSchoolActivity", []string{"200", "400", "401", "403", "404"}},
	}
	seenOperationIDs := map[string]bool{}
	for _, expected := range operations {
		operation := object(t, object(t, paths, expected.path), "get")
		if operation["operationId"] != expected.operation {
			t.Fatalf("GET %s operationId = %#v, want %q", expected.path, operation["operationId"], expected.operation)
		}
		if seenOperationIDs[expected.operation] {
			t.Fatalf("duplicate platform-school operationId %q", expected.operation)
		}
		seenOperationIDs[expected.operation] = true
		responses := object(t, operation, "responses")
		for _, status := range expected.responses {
			if _, ok := responses[status]; !ok {
				t.Errorf("GET %s is missing response %s", expected.path, status)
			}
		}
	}

	components := object(t, document, "components")
	parameters := object(t, components, "parameters")
	tenantID := object(t, parameters, "PlatformSchoolTenantId")
	if tenantID["in"] != "path" || tenantID["required"] != true {
		t.Fatalf("platform school tenant id must be a required path parameter: %#v", tenantID)
	}

	schemas := object(t, components, "schemas")
	assertRequired(t, object(t, schemas, "PlatformSchoolSummary"), []string{
		"tenant_id", "school_id", "name", "code", "status", "created_at", "administrator", "members", "usage", "model_health", "exam_count", "attention_reasons",
	})
	assertRequired(t, object(t, schemas, "PlatformSchoolMembersResponse"), []string{"members", "summary"})
	assertRequired(t, object(t, schemas, "PlatformSchoolUsageSummary"), []string{"requests", "arbitration_requests", "total_tokens", "window_days"})
	assertRequired(t, object(t, schemas, "PlatformSchoolUsageResponse"), []string{"summary", "trend", "by_feature", "by_model", "start_date", "end_date"})
	assertRequired(t, object(t, schemas, "PlatformSchoolModelHealthResponse"), []string{"default_model", "roles"})
	assertRequired(t, object(t, schemas, "PlatformSchoolActivityResponse"), []string{"activities", "security"})

	modelHealthProperties := object(t, object(t, schemas, "PlatformSchoolModelHealth"), "properties")
	if _, ok := modelHealthProperties["credential_hint"]; !ok {
		t.Fatal("model health contract must expose only the redacted credential hint")
	}
	for _, forbidden := range []string{"api_key", "credential_ciphertext", "password", "totp_secret"} {
		if _, ok := modelHealthProperties[forbidden]; ok {
			t.Fatalf("sensitive field %q must not appear in model health contract", forbidden)
		}
	}
}

func TestPlatformSchoolRoutesAreOpenAPICovered(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "route-coverage.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document routeCoverageDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("route coverage must be valid JSON: %v", err)
	}

	want := map[string]string{
		"GET /api/v1/platform/schools":                          "listPlatformSchools",
		"GET /api/v1/platform/schools/{tenant_id}":              "getPlatformSchool",
		"GET /api/v1/platform/schools/{tenant_id}/members":      "listPlatformSchoolMembers",
		"GET /api/v1/platform/schools/{tenant_id}/usage":        "getPlatformSchoolUsage",
		"GET /api/v1/platform/schools/{tenant_id}/model-health": "getPlatformSchoolModelHealth",
		"GET /api/v1/platform/schools/{tenant_id}/activity":     "getPlatformSchoolActivity",
	}
	for _, route := range document.Routes {
		key := route.Method + " " + route.Path
		expectedOperationID, ok := want[key]
		if !ok {
			continue
		}
		if route.Coverage != "openapi" || route.OperationID != expectedOperationID {
			t.Errorf("%s coverage = %q/%q, want openapi/%q", key, route.Coverage, route.OperationID, expectedOperationID)
		}
		delete(want, key)
	}
	for key := range want {
		t.Errorf("route coverage is missing %s", key)
	}
}

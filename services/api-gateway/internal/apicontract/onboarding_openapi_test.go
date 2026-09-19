package apicontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOnboardingReadinessOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "openapi", "edugrade-api.openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("OpenAPI contract must be valid JSON: %v", err)
	}
	paths := object(t, document, "paths")
	operation := object(t, object(t, paths, "/api/v1/onboarding/readiness"), "get")
	if operation["operationId"] != "getOnboardingReadiness" {
		t.Fatalf("unexpected onboarding operationId: %#v", operation["operationId"])
	}
	schemas := object(t, object(t, document, "components"), "schemas")
	assertRequired(t, object(t, schemas, "OnboardingReadiness"), []string{"scope", "ready_for_use", "completed_count", "total_required", "checks"})
	assertRequired(t, object(t, schemas, "OnboardingCheck"), []string{"key", "state", "severity", "title"})
}

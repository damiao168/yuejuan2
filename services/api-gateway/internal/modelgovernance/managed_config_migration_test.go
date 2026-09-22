package modelgovernance

import (
	"os"
	"strings"
	"testing"
)

func TestManagedModelMultiModelMigrationKeepsTenantAndDefaultGuards(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000159_managed_model_api_multi_model.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"DROP CONSTRAINT uq_managed_model_api_provider",
		"CREATE UNIQUE INDEX uq_managed_model_api_provider_model",
		"(tenant_id, provider_key, model_name)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("multi-model migration missing %q", required)
		}
	}
	if strings.Contains(sql, "DROP INDEX uq_managed_model_api_default") {
		t.Fatal("multi-model migration must preserve one default model per school")
	}
}

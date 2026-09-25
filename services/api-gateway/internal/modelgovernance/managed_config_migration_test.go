package modelgovernance

import (
	"crypto/sha256"
	"fmt"
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

func TestManagedModelMultipleCredentialsMigrationKeepsDefaultGuard(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000176_managed_model_api_multiple_credentials.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "DROP INDEX uq_managed_model_api_provider_model") {
		t.Fatal("multiple credentials migration must remove provider/model uniqueness")
	}
	if strings.Contains(sql, "uq_managed_model_api_default") {
		t.Fatal("multiple credentials migration must preserve one default model per school")
	}
}

func TestLegacyPanelMigrationKeepsRecordedChecksumAndRestoresGuards(t *testing.T) {
	panel, err := os.ReadFile("../../migrations/000155_subjective_multi_agent_panel.sql")
	if err != nil {
		t.Fatal(err)
	}
	currentChecksum := fmt.Sprintf("%x", sha256.Sum256(panel))
	compose, err := os.ReadFile("../../../../infra/docker-compose/docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"dfca807d099fe84d1f12d522c232c4120a617d31e2b659cc9e4426ae15c60c23",
		currentChecksum,
		"000162_subjective_panel_legacy_hardening.sql",
	} {
		if !strings.Contains(string(compose), required) {
			t.Fatalf("migration compatibility allowlist missing %q", required)
		}
	}
	hardening, err := os.ReadFile("../../migrations/000162_subjective_panel_legacy_hardening.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"uq_subjective_grading_panel_answer_version",
		"trg_subjective_panel_run_binding_guard",
		"trg_subjective_panel_role_link_guard",
		"trg_subjective_panel_status_transition_guard",
		"trg_subjective_panel_completed_roles_guard",
		"chk_review_task_subjective_panel_source",
		"uq_review_task_active_subjective_panel",
	} {
		if !strings.Contains(string(hardening), required) {
			t.Fatalf("legacy panel hardening missing %q", required)
		}
	}
}

func TestManagedModelTransientProbeMigrationPreservesDefinitiveFailures(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000166_managed_model_probe_transient_status.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"temporary_unavailable",
		"ADD COLUMN IF NOT EXISTS last_successful_tested_at TIMESTAMPTZ",
		"VALIDATE CONSTRAINT chk_managed_model_api_test_status",
		"last_test_message IN ('模型服务连接超时，请稍后重试', '无法连接模型供应商')",
		"last_capability_status = 'success'",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("transient probe migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"LIKE '%超时%'", "credential_invalid", "API Key 无效"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("transient probe migration may reclassify a definitive failure via %q", forbidden)
		}
	}
}

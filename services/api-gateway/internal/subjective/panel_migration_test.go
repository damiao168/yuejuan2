package subjective

import (
	"os"
	"strings"
	"testing"
)

func TestPanelMigrationPreservesOneRunPerAgentAndTenantIsolation(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000155_subjective_multi_agent_panel.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"CREATE TABLE subjective_grading_panel",
		"ADD COLUMN panel_id UUID",
		"agent_role IN ('single','primary_a','primary_b','arbiter')",
		"uq_subjective_grading_run_panel_role",
		"ai_panel_disagreement",
		"ENABLE ROW LEVEL SECURITY",
		"edugrade_tenant_matches(tenant_id)",
		"decision_config JSONB NOT NULL",
		"answer_version TEXT NOT NULL",
		"uq_subjective_grading_panel_answer_version",
		"trg_subjective_panel_run_binding_guard",
		"trg_subjective_panel_role_link_guard",
		"trg_subjective_panel_status_transition_guard",
		"trg_subjective_panel_completed_roles_guard",
		"subjective_panel_id UUID",
		"uq_review_task_active_subjective_panel",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("panel migration is missing %q", required)
		}
	}
	if strings.Contains(sql, "model_final_score") {
		t.Fatal("panel migration must not grant models final-score authority")
	}
}

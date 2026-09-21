package modelgovernance

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestResolvePanelRoleBindingsUsesCanonicalScopeAndStrongerDistinctArbiter(t *testing.T) {
	store := NewMemoryStore()
	for _, id := range []string{"config-a", "config-b", "config-c"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "tenant-1", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}
	inputs := []SaveModelRoleBindingInput{
		{EducationStage: "senior", SubjectCode: "math", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p1", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p-arbiter", StrengthRank: 2},
	}
	for _, input := range inputs {
		if _, err := store.SaveModelRoleBinding(context.Background(), "tenant-1", "actor-1", input); err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "mathematics", "structured_steps")
	if err != nil {
		t.Fatal(err)
	}
	if bindings.PrimaryA.SubjectCode != "mathematics" || bindings.Arbiter.StrengthRank != 2 || bindings.Arbiter.ManagedModelAPIConfigID != "config-c" {
		t.Fatalf("unexpected role resolution: %#v", bindings)
	}
}

func TestResolvePanelRoleBindingsRejectsWeakOrReusedArbiter(t *testing.T) {
	store := NewMemoryStore()
	for _, id := range []string{"config-a", "config-b"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "tenant-1", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}
	for _, input := range []SaveModelRoleBindingInput{
		{EducationStage: "senior", SubjectCode: "physics", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p1", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "physics", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "physics", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-a", PromptVersion: "p2", StrengthRank: 1},
	} {
		if _, err := store.SaveModelRoleBinding(context.Background(), "tenant-1", "actor-1", input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "physics", "structured_steps"); err != ErrManagedCapabilityRequired {
		t.Fatalf("expected governed arbiter rejection, got %v", err)
	}
}

func TestModelRoleBindingMigrationIsScopedAndGoverned(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000156_model_role_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{
		"CREATE TABLE model_role_binding", "education_stage IN ('junior','senior')",
		"'mathematics'", "'ethics_politics'", "'primary_a','primary_b','arbiter'",
		"managed_model_api_config", "ENABLE ROW LEVEL SECURITY", "edugrade_tenant_matches(tenant_id)",
	} {
		if !strings.Contains(sql, required) {
			t.Fatalf("role binding migration missing %q", required)
		}
	}
}

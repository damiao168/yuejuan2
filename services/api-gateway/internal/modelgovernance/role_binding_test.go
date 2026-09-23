package modelgovernance

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type panelRoleBindingStoreStub struct {
	items      []ModelRoleBinding
	err        error
	listCalled bool
}

func (s *panelRoleBindingStoreStub) SaveModelRoleBinding(context.Context, string, string, SaveModelRoleBindingInput) (ModelRoleBinding, error) {
	return ModelRoleBinding{}, nil
}

func (s *panelRoleBindingStoreStub) ListModelRoleBindings(context.Context, string, string, string, string) ([]ModelRoleBinding, error) {
	s.listCalled = true
	return s.items, s.err
}

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
	if bindings.PrimaryA.AgentRole != ModelRolePrimaryA || bindings.PrimaryA.SubjectCode != "mathematics" ||
		bindings.PrimaryB.AgentRole != ModelRolePrimaryB || bindings.PrimaryB.ManagedModelAPIConfigID != "config-b" ||
		bindings.Arbiter.AgentRole != ModelRoleArbiter || bindings.Arbiter.StrengthRank != 2 || bindings.Arbiter.ManagedModelAPIConfigID != "config-c" {
		t.Fatalf("unexpected role resolution: %#v", bindings)
	}
}

func TestResolvePanelRoleBindingsRequiresCompleteGovernedRolesAndIndependentArbiter(t *testing.T) {
	newInputs := func() []SaveModelRoleBindingInput {
		return []SaveModelRoleBindingInput{
			{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p1", StrengthRank: 1},
			{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 2},
			{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p2", StrengthRank: 3},
		}
	}

	tests := []struct {
		name             string
		omitRole         string
		arbiterModel     string
		primaryARank     int
		primaryBRank     int
		arbiterRank      int
		arbiterStatus    string
		unverifiedConfig string
	}{
		{name: "missing primary A", omitRole: ModelRolePrimaryA},
		{name: "missing primary B", omitRole: ModelRolePrimaryB},
		{name: "missing arbiter", omitRole: ModelRoleArbiter},
		{name: "arbiter reuses primary A model", arbiterModel: "config-a"},
		{name: "arbiter reuses primary B model", arbiterModel: "config-b"},
		{name: "arbiter strength equals primary A", primaryARank: 2, primaryBRank: 1, arbiterRank: 2},
		{name: "arbiter strength equals primary B", primaryARank: 1, primaryBRank: 2, arbiterRank: 2},
		{name: "arbiter strength below both primaries", primaryARank: 3, primaryBRank: 2, arbiterRank: 1},
		{name: "disabled arbiter", arbiterStatus: "disabled"},
		{name: "unverified primary B model", unverifiedConfig: "config-b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewMemoryStore()
			for _, id := range []string{"config-a", "config-b", "config-c"} {
				store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "tenant-1", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
			}

			inputs := newInputs()
			for i := range inputs {
				switch inputs[i].AgentRole {
				case ModelRolePrimaryA:
					if tt.primaryARank != 0 {
						inputs[i].StrengthRank = tt.primaryARank
					}
				case ModelRolePrimaryB:
					if tt.primaryBRank != 0 {
						inputs[i].StrengthRank = tt.primaryBRank
					}
				case ModelRoleArbiter:
					if tt.arbiterModel != "" {
						inputs[i].ManagedModelAPIConfigID = tt.arbiterModel
					}
					if tt.arbiterRank != 0 {
						inputs[i].StrengthRank = tt.arbiterRank
					}
					if tt.arbiterStatus != "" {
						inputs[i].Status = tt.arbiterStatus
					}
				}
			}

			for _, input := range inputs {
				if input.AgentRole == tt.omitRole {
					continue
				}
				if _, err := store.SaveModelRoleBinding(context.Background(), "tenant-1", "actor-1", input); err != nil {
					t.Fatalf("save %s binding: %v", input.AgentRole, err)
				}
			}
			if tt.unverifiedConfig != "" {
				config := store.managedConfigs[tt.unverifiedConfig]
				config.LastCapabilityStatus = "failed"
				store.managedConfigs[tt.unverifiedConfig] = config
			}

			if _, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "physics", "structured_steps"); err != ErrManagedCapabilityRequired {
				t.Fatalf("expected ErrManagedCapabilityRequired, got %v", err)
			}
		})
	}
}

func TestResolvePanelRoleBindingsFailsClosedBeforeListingInvalidRequests(t *testing.T) {
	tests := []struct {
		name      string
		store     ModelRoleBindingStore
		tenantID  string
		stage     string
		subject   string
		archetype string
	}{
		{name: "nil store", tenantID: "tenant-1", stage: "senior", subject: "physics", archetype: "structured_steps"},
		{name: "blank tenant", store: &panelRoleBindingStoreStub{}, tenantID: " \t", stage: "senior", subject: "physics", archetype: "structured_steps"},
		{name: "invalid stage", store: &panelRoleBindingStoreStub{}, tenantID: "tenant-1", stage: "college", subject: "physics", archetype: "structured_steps"},
		{name: "invalid subject", store: &panelRoleBindingStoreStub{}, tenantID: "tenant-1", stage: "senior", subject: "unknown-subject", archetype: "structured_steps"},
		{name: "oversized archetype", store: &panelRoleBindingStoreStub{}, tenantID: "tenant-1", stage: "senior", subject: "physics", archetype: strings.Repeat("x", 129)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bindings, err := ResolvePanelRoleBindings(context.Background(), tt.store, tt.tenantID, tt.stage, tt.subject, tt.archetype)
			if err != ErrInvalidManagedConfig {
				t.Fatalf("expected ErrInvalidManagedConfig, got %v", err)
			}
			if bindings != (PanelRoleBindings{}) {
				t.Fatalf("expected empty bindings on invalid request, got %#v", bindings)
			}
			if stub, ok := tt.store.(*panelRoleBindingStoreStub); ok && stub.listCalled {
				t.Fatal("invalid request reached the binding store")
			}
		})
	}
}

func TestResolvePanelRoleBindingsPropagatesStoreErrors(t *testing.T) {
	listErr := errors.New("role binding list failed")
	store := &panelRoleBindingStoreStub{err: listErr}

	bindings, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "physics", "structured_steps")
	if err != listErr {
		t.Fatalf("expected underlying list error %v, got %v", listErr, err)
	}
	if bindings != (PanelRoleBindings{}) {
		t.Fatalf("expected empty bindings on store error, got %#v", bindings)
	}
	if !store.listCalled {
		t.Fatal("expected binding store to be queried")
	}
}

func TestResolvePanelRoleBindingsPrefersExactArchetypeBeforeWildcard(t *testing.T) {
	store := NewMemoryStore()
	for _, id := range []string{"config-a-exact", "config-a-wildcard", "config-b", "config-c"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "tenant-1", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}

	inputs := []SaveModelRoleBindingInput{
		{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a-wildcard", PromptVersion: "fallback", StrengthRank: 5},
		{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a-exact", PromptVersion: "exact", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 1},
		{EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p2", StrengthRank: 2},
	}
	var exactPrimaryA ModelRoleBinding
	for _, input := range inputs {
		binding, err := store.SaveModelRoleBinding(context.Background(), "tenant-1", "actor-1", input)
		if err != nil {
			t.Fatal(err)
		}
		if input.PromptVersion == "exact" {
			exactPrimaryA = binding
		}
	}

	bindings, err := ResolvePanelRoleBindings(context.Background(), store, "tenant-1", "senior", "physics", "structured_steps")
	if err != nil {
		t.Fatal(err)
	}
	if bindings.PrimaryA.ID != exactPrimaryA.ID || bindings.PrimaryA.PromptVersion != "exact" {
		t.Fatalf("expected exact archetype binding to win over wildcard fallback, got %#v", bindings.PrimaryA)
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

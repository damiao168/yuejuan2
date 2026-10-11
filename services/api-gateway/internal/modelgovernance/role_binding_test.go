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
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "math", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p1", StrengthRank: 1},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 1},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "mathematics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p-arbiter", StrengthRank: 2},
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

type panelRoleFailureCase struct {
	name             string
	omitRole         string
	arbiterModel     string
	primaryARank     int
	primaryBRank     int
	arbiterRank      int
	arbiterStatus    string
	unverifiedConfig string
}

func panelRoleFailureInputs() []SaveModelRoleBindingInput {
	return []SaveModelRoleBindingInput{
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p1", StrengthRank: 1},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 2},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p2", StrengthRank: 3},
	}
}

func configurePanelRoleFailureInput(input *SaveModelRoleBindingInput, tt panelRoleFailureCase) {
	switch input.AgentRole {
	case ModelRolePrimaryA:
		if tt.primaryARank != 0 {
			input.StrengthRank = tt.primaryARank
		}
	case ModelRolePrimaryB:
		if tt.primaryBRank != 0 {
			input.StrengthRank = tt.primaryBRank
		}
	case ModelRoleArbiter:
		if tt.arbiterModel != "" {
			input.ManagedModelAPIConfigID = tt.arbiterModel
		}
		if tt.arbiterRank != 0 {
			input.StrengthRank = tt.arbiterRank
		}
		if tt.arbiterStatus != "" {
			input.Status = tt.arbiterStatus
		}
	}
}

// 每个案例单独配置三角色，保护缺席、模型复用、强度不足与未验证模型的拒绝边界。
func exercisePanelRoleFailure(t *testing.T, tt panelRoleFailureCase) {
	t.Helper()
	store := NewMemoryStore()
	for _, id := range []string{"config-a", "config-b", "config-c"} {
		store.managedConfigs[id] = ManagedAPIConfig{ID: id, TenantID: "tenant-1", Status: "active", LastCapabilityStatus: "success", LastCapabilityVersion: ManagedCapabilityProbeVersion}
	}

	inputs := panelRoleFailureInputs()
	for i := range inputs {
		configurePanelRoleFailureInput(&inputs[i], tt)
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
}

func TestResolvePanelRoleBindingsRequiresCompleteGovernedRolesAndIndependentArbiter(t *testing.T) {

	tests := []panelRoleFailureCase{
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
			exercisePanelRoleFailure(t, tt)
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
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a-wildcard", PromptVersion: "fallback", StrengthRank: 5},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a-exact", PromptVersion: "exact", StrengthRank: 1},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p1", StrengthRank: 1},
		{ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "structured_steps", AgentRole: ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p2", StrengthRank: 2},
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

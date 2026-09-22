package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type panelRoleStoreStub struct {
	items []modelgovernance.ModelRoleBinding
}

func TestResolvePanelAgentsFailsClosedForRevokedOrMismatchedConnections(t *testing.T) {
	roles := panelRoleStoreStub{items: []modelgovernance.ModelRoleBinding{
		{ID: "binding-a", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p-v1", StrengthRank: 1, Status: "active"},
		{ID: "binding-b", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p-v1", StrengthRank: 1, Status: "active"},
		{ID: "binding-c", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p-v1", StrengthRank: 2, Status: "active"},
	}}
	connections := panelConnectionsStub{}
	for _, id := range []string{"config-a", "config-b", "config-c"} {
		connections[id] = modelgovernance.ManagedAPIConnection{Config: modelgovernance.ManagedAPIConfig{
			ID: id, TenantID: "tenant-1", ModelVersion: "model-" + id, Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: modelgovernance.ManagedCapabilityProbeVersion,
		}}
	}
	factory := func(_ modelgovernance.ManagedAPIConnection, _ modelgovernance.ModelRoleBinding, _ ModelPolicy) (LLMGradingAdapter, error) {
		return &panelTestAdapter{}, nil
	}
	for _, mutation := range []struct {
		name string
		edit func(*modelgovernance.ManagedAPIConfig)
	}{
		{"revoked probe", func(config *modelgovernance.ManagedAPIConfig) { config.LastCapabilityStatus = "failed" }},
		{"cross tenant", func(config *modelgovernance.ManagedAPIConfig) { config.TenantID = "tenant-2" }},
		{"inactive", func(config *modelgovernance.ManagedAPIConfig) { config.Status = "disabled" }},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			copyConnections := panelConnectionsStub{}
			for key, value := range connections {
				copyConnections[key] = value
			}
			changed := copyConnections["config-c"]
			mutation.edit(&changed.Config)
			copyConnections["config-c"] = changed
			_, err := ResolvePanelAgents(context.Background(), "tenant-1", "senior", "mathematics", "structured_steps", roles, copyConnections, factory, .85)
			if !errors.Is(err, ErrPanelConfiguration) {
				t.Fatalf("unsafe connection was accepted: %v", err)
			}
		})
	}
}

func (s panelRoleStoreStub) SaveModelRoleBinding(context.Context, string, string, modelgovernance.SaveModelRoleBindingInput) (modelgovernance.ModelRoleBinding, error) {
	return modelgovernance.ModelRoleBinding{}, ErrPanelConfiguration
}
func (s panelRoleStoreStub) ListModelRoleBindings(context.Context, string, string, string, string) ([]modelgovernance.ModelRoleBinding, error) {
	return s.items, nil
}

type panelConnectionsStub map[string]modelgovernance.ManagedAPIConnection

func (s panelConnectionsStub) GetManagedAPIConnection(_ context.Context, _, id string) (modelgovernance.ManagedAPIConnection, error) {
	return s[id], nil
}

func TestResolvePanelAgentsUsesThreeGovernedConnectionsAndStrongerArbiter(t *testing.T) {
	roles := panelRoleStoreStub{items: []modelgovernance.ModelRoleBinding{
		{ID: "binding-a", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "p-v1", StrengthRank: 1, Status: "active"},
		{ID: "binding-b", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "p-v1", StrengthRank: 1, Status: "active"},
		{ID: "binding-c", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "p-v1", StrengthRank: 2, Status: "active"},
	}}
	connections := panelConnectionsStub{}
	for _, pair := range [][2]string{{"config-a", "model-a"}, {"config-b", "model-b"}, {"config-c", "model-c"}} {
		connections[pair[0]] = modelgovernance.ManagedAPIConnection{Config: modelgovernance.ManagedAPIConfig{
			ID: pair[0], TenantID: "tenant-1", ModelVersion: pair[1], Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: modelgovernance.ManagedCapabilityProbeVersion,
		}}
	}
	seen := map[string]bool{}
	factory := func(connection modelgovernance.ManagedAPIConnection, binding modelgovernance.ModelRoleBinding, policy ModelPolicy) (LLMGradingAdapter, error) {
		if policy.ModelVersion != connection.Config.ModelVersion || policy.PromptVersion != binding.PromptVersion || policy.MinConfidence != .85 {
			t.Fatalf("role policy was not derived from managed connection: %#v", policy)
		}
		seen[binding.AgentRole] = true
		return &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .95}, nil
	}
	agents, err := ResolvePanelAgents(context.Background(), "tenant-1", "senior", "mathematics", "structured_steps", roles, connections, factory, .85)
	if err != nil || len(seen) != 3 || agents.Arbiter.Policy.ModelVersion != "model-c" || agents.Arbiter.StrengthRank != 2 {
		t.Fatalf("governed panel agents were not resolved: %#v seen=%#v err=%v", agents, seen, err)
	}
	if agents.PrimaryA.ModelConfigID != "config-a" || agents.Arbiter.ModelConfigID != "config-c" {
		t.Fatalf("governed connection identities were not retained: %#v", agents)
	}
	changed := agents
	changed.PrimaryA.ModelConfigID = "replacement-config-a"
	if PanelModelSetReference(changed) == PanelModelSetReference(agents) {
		t.Fatal("replacing a governed config reused the old evaluation identity")
	}
	if _, err := NewPanelOrchestrator(NewMemoryStore(), agents, allowPanelAdmission); err != nil {
		t.Fatalf("resolved agents could not enter shadow orchestrator: %v", err)
	}
}

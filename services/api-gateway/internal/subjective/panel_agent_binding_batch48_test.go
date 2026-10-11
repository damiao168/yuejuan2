package subjective

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestBatch48PanelRuntimeRejectsMissingDependenciesAndInvalidConfidence(t *testing.T) {
	factory := func(modelgovernance.ManagedAPIConnection, modelgovernance.ModelRoleBinding, ModelPolicy) (LLMGradingAdapter, error) {
		t.Fatal("invalid runtime reached adapter factory")
		return nil, nil
	}
	valid := PanelAgentRuntime{Roles: panelRoleStoreStub{}, Connections: panelConnectionsStub{}, Factory: factory}
	for _, scenario := range []struct {
		name       string
		runtime    PanelAgentRuntime
		tenant     string
		confidence float64
	}{
		{"missing roles", PanelAgentRuntime{Connections: valid.Connections, Factory: factory}, "tenant", .85},
		{"missing connections", PanelAgentRuntime{Roles: valid.Roles, Factory: factory}, "tenant", .85},
		{"missing factory", PanelAgentRuntime{Roles: valid.Roles, Connections: valid.Connections}, "tenant", .85},
		{"blank tenant", valid, "  ", .85},
		{"zero confidence", valid, "tenant", 0},
		{"excess confidence", valid, "tenant", 1.01},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			agents, err := ResolvePanelAgents(t.Context(), scenario.tenant, "senior", "mathematics", "structured_steps", scenario.runtime, scenario.confidence)
			if !errors.Is(err, ErrPanelConfiguration) || !reflect.DeepEqual(agents, PanelAgents{}) {
				t.Fatalf("invalid runtime accepted: agents=%#v err=%v", agents, err)
			}
		})
	}
}

type batch48PanelRoleStore struct {
	panelRoleStoreStub
	t     *testing.T
	trace *[]string
	err   error
}

func (s batch48PanelRoleStore) ListModelRoleBindings(_ context.Context, tenant, stage, subject, archetype string) ([]modelgovernance.ModelRoleBinding, error) {
	*s.trace = append(*s.trace, "roles")
	if !reflect.DeepEqual([]string{tenant, stage, subject, archetype}, []string{"tenant-1", "senior", "mathematics", "structured_steps"}) {
		s.t.Fatalf("role scope changed: %q %q %q %q", tenant, stage, subject, archetype)
	}
	return s.items, s.err
}

type batch48PanelConnections struct {
	t       *testing.T
	items   panelConnectionsStub
	trace   *[]string
	failID  string
	failure error
}

func (s batch48PanelConnections) GetManagedAPIConnection(_ context.Context, tenant, id string) (modelgovernance.ManagedAPIConnection, error) {
	*s.trace = append(*s.trace, "connection:"+id)
	if tenant != "tenant-1" {
		s.t.Fatalf("connection read changed tenant: %q", tenant)
	}
	if id == s.failID {
		return modelgovernance.ManagedAPIConnection{}, s.failure
	}
	return s.items[id], nil
}

func TestBatch48PanelRuntimePreservesScopeBuildOrderAndDependencyErrors(t *testing.T) {
	dependencyErr := errors.New("governed dependency failed")
	for _, scenario := range []panelDependencyOrderCase{
		{name: "success", wantTrace: []string{"roles", "connection:config-a", "factory:config-a", "connection:config-b", "factory:config-b", "connection:config-c", "factory:config-c"}},
		{name: "roles fail", roleErr: dependencyErr, wantTrace: []string{"roles"}},
		{name: "connection fails", readFailID: "config-b", wantTrace: []string{"roles", "connection:config-a", "factory:config-a", "connection:config-b"}},
		{name: "factory fails", buildFail: "config-b", wantTrace: []string{"roles", "connection:config-a", "factory:config-a", "connection:config-b", "factory:config-b"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assertPanelDependencyBuildOrder(t, scenario, dependencyErr)
		})
	}
}

type panelDependencyOrderCase struct {
	name       string
	roleErr    error
	readFailID string
	buildFail  string
	wantTrace  []string
}

func assertPanelDependencyBuildOrder(t *testing.T, scenario panelDependencyOrderCase, dependencyErr error) {
	t.Helper()
	trace := []string{}
	roles := batch48PanelRoleStore{t: t, trace: &trace, err: scenario.roleErr, panelRoleStoreStub: panelRoleStoreStub{items: []modelgovernance.ModelRoleBinding{
		{ID: "a", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryA, ManagedModelAPIConfigID: "config-a", PromptVersion: "prompt-a", StrengthRank: 1, Status: "active"},
		{ID: "b", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRolePrimaryB, ManagedModelAPIConfigID: "config-b", PromptVersion: "prompt-b", StrengthRank: 1, Status: "active"},
		{ID: "c", TenantID: "tenant-1", AgentRole: modelgovernance.ModelRoleArbiter, ManagedModelAPIConfigID: "config-c", PromptVersion: "prompt-c", StrengthRank: 2, Status: "active"},
	}}}
	items := panelConnectionsStub{}
	for _, id := range []string{"config-a", "config-b", "config-c"} {
		items[id] = modelgovernance.ManagedAPIConnection{Config: modelgovernance.ManagedAPIConfig{
			ID: id, TenantID: "tenant-1", ModelVersion: "model-" + id, Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: modelgovernance.ManagedCapabilityProbeVersion,
		}}
	}
	factory := func(connection modelgovernance.ManagedAPIConnection, binding modelgovernance.ModelRoleBinding, policy ModelPolicy) (LLMGradingAdapter, error) {
		trace = append(trace, "factory:"+connection.Config.ID)
		if policy.ModelVersion != connection.Config.ModelVersion || policy.PromptVersion != binding.PromptVersion || policy.MinConfidence != .85 {
			t.Fatalf("governed policy changed: %#v", policy)
		}
		if connection.Config.ID == scenario.buildFail {
			return nil, dependencyErr
		}
		return &panelTestAdapter{}, nil
	}
	agents, err := ResolvePanelAgents(t.Context(), "tenant-1", "senior", "mathematics", "structured_steps", PanelAgentRuntime{
		Roles: roles, Connections: batch48PanelConnections{t: t, items: items, trace: &trace, failID: scenario.readFailID, failure: dependencyErr}, Factory: factory,
	}, .85)
	if !reflect.DeepEqual(trace, scenario.wantTrace) {
		t.Fatalf("dependency order changed: %#v", trace)
	}
	failed := scenario.roleErr != nil || scenario.readFailID != "" || scenario.buildFail != ""
	if failed && (!errors.Is(err, dependencyErr) || !reflect.DeepEqual(agents, PanelAgents{})) {
		t.Fatalf("dependency error changed: agents=%#v err=%v", agents, err)
	}
	if !failed && (err != nil || agents.Arbiter.ModelConfigID != "config-c") {
		t.Fatalf("resolved panel changed: agents=%#v err=%v", agents, err)
	}
}

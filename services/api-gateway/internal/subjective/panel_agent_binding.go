package subjective

import (
	"context"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type PanelModelConnectionStore interface {
	GetManagedAPIConnection(context.Context, string, string) (modelgovernance.ManagedAPIConnection, error)
}

// PanelAdapterFactory is supplied by the deployment runtime. It must build a
// role-isolated rubric adapter for the resolved, capability-tested connection;
// it must never place peer scores or results in the model request.
type PanelAdapterFactory func(modelgovernance.ManagedAPIConnection, modelgovernance.ModelRoleBinding, ModelPolicy) (LLMGradingAdapter, error)

// ResolvePanelAgents ties every A/B/C execution to a governed role binding.
// A strength rank only selects the shadow candidate; real senior-subject
// adjudication evaluation remains required before production activation.
func ResolvePanelAgents(ctx context.Context, tenantID, stage, subject, archetype string, roles modelgovernance.ModelRoleBindingStore,
	connections PanelModelConnectionStore, factory PanelAdapterFactory, minConfidence float64) (PanelAgents, error) {
	if roles == nil || connections == nil || factory == nil || strings.TrimSpace(tenantID) == "" ||
		minConfidence <= 0 || minConfidence > 1 {
		return PanelAgents{}, ErrPanelConfiguration
	}
	bindings, err := modelgovernance.ResolvePanelRoleBindings(ctx, roles, tenantID, stage, subject, archetype)
	if err != nil {
		return PanelAgents{}, err
	}
	build := func(binding modelgovernance.ModelRoleBinding) (PanelAgentBinding, error) {
		connection, getErr := connections.GetManagedAPIConnection(ctx, tenantID, binding.ManagedModelAPIConfigID)
		if getErr != nil {
			return PanelAgentBinding{}, getErr
		}
		if connection.Config.TenantID != tenantID || connection.Config.ID != binding.ManagedModelAPIConfigID ||
			connection.Config.Status != "active" || connection.Config.LastCapabilityStatus != "success" ||
			connection.Config.LastCapabilityVersion != modelgovernance.ManagedCapabilityProbeVersion ||
			strings.TrimSpace(connection.Config.ModelVersion) == "" {
			return PanelAgentBinding{}, ErrPanelConfiguration
		}
		policy := ModelPolicy{ModelVersion: connection.Config.ModelVersion, PromptVersion: binding.PromptVersion, MinConfidence: minConfidence}
		adapter, buildErr := factory(connection, binding, policy)
		if buildErr != nil {
			return PanelAgentBinding{}, buildErr
		}
		return PanelAgentBinding{Adapter: adapter, Policy: policy, StrengthRank: binding.StrengthRank}, nil
	}
	a, err := build(bindings.PrimaryA)
	if err != nil {
		return PanelAgents{}, err
	}
	b, err := build(bindings.PrimaryB)
	if err != nil {
		return PanelAgents{}, err
	}
	c, err := build(bindings.Arbiter)
	if err != nil {
		return PanelAgents{}, err
	}
	agents := PanelAgents{PrimaryA: a, PrimaryB: b, Arbiter: c}
	if validatePanelAgents(agents) != nil {
		return PanelAgents{}, ErrPanelConfiguration
	}
	return agents, nil
}

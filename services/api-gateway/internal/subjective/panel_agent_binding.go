package subjective

import (
	"context"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type PanelModelConnectionStore interface {
	GetManagedAPIConnection(context.Context, string, string) (modelgovernance.ManagedAPIConnection, error)
}

// PanelAdapterFactory 由部署运行时提供，必须为已解析且通过能力测试的连接构建角色隔离的评分细则适配器；
// 绝不能将其他评审方的分数或结果放入模型请求。
type PanelAdapterFactory func(modelgovernance.ManagedAPIConnection, modelgovernance.ModelRoleBinding, ModelPolicy) (LLMGradingAdapter, error)

// PanelAgentRuntime 提供受治理连接的读取和适配器构建依赖；连接每次解析时仍须重新读取并校验。
type PanelAgentRuntime struct {
	Roles       modelgovernance.ModelRoleBindingStore
	Connections PanelModelConnectionStore
	Factory     PanelAdapterFactory
}

// ResolvePanelAgents 将每次 A/B/C 执行绑定到受治理的角色绑定。
// 能力排序只选择影子候选；生产启用前仍需真实的资深学科裁决评估。
func ResolvePanelAgents(ctx context.Context, tenantID, stage, subject, archetype string, runtime PanelAgentRuntime, minConfidence float64) (PanelAgents, error) {
	if runtime.Roles == nil || runtime.Connections == nil || runtime.Factory == nil || strings.TrimSpace(tenantID) == "" ||
		minConfidence <= 0 || minConfidence > 1 {
		return PanelAgents{}, ErrPanelConfiguration
	}
	bindings, err := modelgovernance.ResolvePanelRoleBindings(ctx, runtime.Roles, tenantID, stage, subject, archetype)
	if err != nil {
		return PanelAgents{}, err
	}
	build := func(binding modelgovernance.ModelRoleBinding) (PanelAgentBinding, error) {
		connection, getErr := runtime.Connections.GetManagedAPIConnection(ctx, tenantID, binding.ManagedModelAPIConfigID)
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
		adapter, buildErr := runtime.Factory(connection, binding, policy)
		if buildErr != nil {
			return PanelAgentBinding{}, buildErr
		}
		return PanelAgentBinding{
			Adapter: adapter, Policy: policy, StrengthRank: binding.StrengthRank,
			ModelConfigID: connection.Config.ID, ProviderKey: connection.Config.ProviderKey,
			ModelName:   connection.Config.ModelName,
			AdapterType: connection.Config.AdapterType, BaseURL: connection.Config.BaseURL,
		}, nil
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

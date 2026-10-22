package modelgovernance

import "context"

// Store 组合本模块装配所需能力，保持现有构造入口兼容；新消费者应按业务职责依赖下方小接口。
// 接口拆分只说明依赖范围，不代替权限、版本校验，也不承诺跨能力调用自动进入同一事务。
type Store interface {
	RuntimeBaselineStore
	ProviderDeploymentStore
	TenantPolicyStore
	ModelEvaluationStore
	ModelApprovalStore
}

// RuntimeBaselineStore 仅负责部署初始化与生产就绪校验，不能通过配置页面接口替代启动检查。
type RuntimeBaselineStore interface {
	EnsureLocalBaseline(ctx context.Context, tenantID string, baseline LocalBaseline) error
	ValidateProductionReadiness(ctx context.Context, secrets SecretReferenceResolver) error
}

// ProviderDeploymentStore 管理供应方与部署元数据，不负责学校策略或审批决策。
type ProviderDeploymentStore interface {
	ListProviders(ctx context.Context, tenantID string) ([]Provider, error)
	CreateProvider(ctx context.Context, tenantID string, actorID string, input ProviderInput) (Provider, error)
	UpdateProviderStatus(ctx context.Context, tenantID string, id string, input ProviderStatusInput) (Provider, error)
	ListDeployments(ctx context.Context, tenantID string) ([]Deployment, error)
	CreateDeployment(ctx context.Context, tenantID string, actorID string, input DeploymentInput) (Deployment, error)
	UpdateDeploymentState(ctx context.Context, tenantID string, id string, input DeploymentStateInput) (Deployment, error)
}

// TenantPolicyStore 管理学校模型调用策略；租户标识由调用方的授权范围确定。
type TenantPolicyStore interface {
	GetPolicy(ctx context.Context, tenantID string) (TenantPolicy, error)
	UpdatePolicy(ctx context.Context, tenantID string, actorID string, input PolicyUpdateInput) (TenantPolicy, error)
}

// ModelEvaluationStore 管理评测运行及候选结果，评测完成不自动授予生产使用权限。
type ModelEvaluationStore interface {
	ListEvaluationRuns(ctx context.Context, tenantID string, filter EvaluationListFilter) ([]EvaluationRun, error)
	CreateEvaluationRun(ctx context.Context, tenantID string, actorID string, input EvaluationRunInput) (EvaluationRun, error)
	AddEvaluationCandidate(ctx context.Context, tenantID string, actorID string, runID string, input EvaluationCandidateInput) (EvaluationCandidate, error)
	CompleteEvaluationRun(ctx context.Context, tenantID string, actorID string, runID string, reason string) (EvaluationRun, error)
	InvalidateEvaluationRun(ctx context.Context, tenantID string, actorID string, runID string, reason string) (EvaluationRun, error)
}

// ModelApprovalStore 管理沙箱和模型审批，撤销操作保留原接口的审计与状态边界。
type ModelApprovalStore interface {
	ListSandboxApprovals(ctx context.Context, tenantID string) ([]SandboxApproval, error)
	CreateSandboxApproval(ctx context.Context, tenantID string, actorID string, input SandboxApprovalInput) (SandboxApproval, error)
	RevokeSandboxApproval(ctx context.Context, tenantID string, actorID string, id string, reason string) (SandboxApproval, error)
	ListModelApprovals(ctx context.Context, tenantID string) ([]ModelApproval, error)
	CreateModelApproval(ctx context.Context, tenantID string, actorID string, input ModelApprovalInput) (ModelApproval, error)
	RevokeModelApproval(ctx context.Context, tenantID string, actorID string, id string, input ModelApprovalRevokeInput) (ModelApproval, error)
}

func ProviderFromInput(input ProviderInput) Provider {
	return Provider{
		Key:           input.Key,
		DisplayName:   input.DisplayName,
		Kind:          input.Kind,
		AdapterType:   input.AdapterType,
		CredentialRef: input.CredentialRef,
		Region:        input.Region,
		DataPolicy:    input.DataPolicy,
		Status:        input.Status,
	}
}

func DeploymentFromInput(input DeploymentInput, providerKey string) Deployment {
	return Deployment{
		ProviderID:        input.ProviderID,
		Key:               input.Key,
		ProviderKey:       providerKey,
		ModelName:         input.ModelName,
		ModelVersion:      input.ModelVersion,
		Region:            input.Region,
		CapabilityProfile: input.CapabilityProfile,
		Modalities:        input.Modalities,
		CapabilityPolicy:  input.CapabilityPolicy,
		PricingPolicy:     input.PricingPolicy,
		Status:            input.Status,
		HealthState:       input.HealthState,
	}
}

func PolicyFromUpdate(input PolicyUpdateInput) TenantPolicy {
	return TenantPolicy{
		Mode:                     input.Mode,
		ExternalEnabled:          input.ExternalEnabled,
		TextExportEnabled:        input.TextExportEnabled,
		ImageExportEnabled:       input.ImageExportEnabled,
		AllowedDeployments:       input.AllowedDeployments,
		AllowedModelConfigIDs:    input.AllowedModelConfigIDs,
		MaxCostMicrosPerQuestion: input.MaxCostMicrosPerQuestion,
		MaxCostMicrosPerExam:     input.MaxCostMicrosPerExam,
		FallbackMode:             input.FallbackMode,
	}
}

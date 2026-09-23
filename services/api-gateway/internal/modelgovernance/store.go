package modelgovernance

import "context"

type Store interface {
	EnsureLocalBaseline(ctx context.Context, tenantID string, baseline LocalBaseline) error
	ValidateProductionReadiness(ctx context.Context, secrets SecretReferenceResolver) error
	ListProviders(ctx context.Context, tenantID string) ([]Provider, error)
	CreateProvider(ctx context.Context, tenantID string, actorID string, input ProviderInput) (Provider, error)
	UpdateProviderStatus(ctx context.Context, tenantID string, id string, input ProviderStatusInput) (Provider, error)
	ListDeployments(ctx context.Context, tenantID string) ([]Deployment, error)
	CreateDeployment(ctx context.Context, tenantID string, actorID string, input DeploymentInput) (Deployment, error)
	UpdateDeploymentState(ctx context.Context, tenantID string, id string, input DeploymentStateInput) (Deployment, error)
	GetPolicy(ctx context.Context, tenantID string) (TenantPolicy, error)
	UpdatePolicy(ctx context.Context, tenantID string, actorID string, input PolicyUpdateInput) (TenantPolicy, error)
	ListSandboxApprovals(ctx context.Context, tenantID string) ([]SandboxApproval, error)
	CreateSandboxApproval(ctx context.Context, tenantID string, actorID string, input SandboxApprovalInput) (SandboxApproval, error)
	RevokeSandboxApproval(ctx context.Context, tenantID string, actorID string, id string, reason string) (SandboxApproval, error)
	ListEvaluationRuns(ctx context.Context, tenantID string, filter EvaluationListFilter) ([]EvaluationRun, error)
	CreateEvaluationRun(ctx context.Context, tenantID string, actorID string, input EvaluationRunInput) (EvaluationRun, error)
	AddEvaluationCandidate(ctx context.Context, tenantID string, actorID string, runID string, input EvaluationCandidateInput) (EvaluationCandidate, error)
	CompleteEvaluationRun(ctx context.Context, tenantID string, actorID string, runID string, reason string) (EvaluationRun, error)
	InvalidateEvaluationRun(ctx context.Context, tenantID string, actorID string, runID string, reason string) (EvaluationRun, error)
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

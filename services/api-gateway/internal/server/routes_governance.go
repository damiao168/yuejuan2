package server

import (
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"net/http"
)

func registerGovernanceRoutes(mux *http.ServeMux, ctx routerContext) {
	ctx.modules.AIGovernance.ModelGovernanceHandler.WithManagedAPIProbeObserver(ctx.metrics)
	mux.Handle("GET /api/v1/model-providers", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListProviders))
	mux.Handle("POST /api/v1/model-providers", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateProvider))
	mux.Handle("PATCH /api/v1/model-providers/{id}/status", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.UpdateProviderStatus))
	mux.Handle("GET /api/v1/model-deployments", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListDeployments))
	mux.Handle("POST /api/v1/model-deployments", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateDeployment))
	mux.Handle("PATCH /api/v1/model-deployments/{id}/state", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.UpdateDeploymentState))
	mux.Handle("GET /api/v1/model-policy", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.GetPolicy))
	mux.Handle("GET /api/v1/model-prompts/current", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.GetCurrentPrompt))
	mux.Handle("PUT /api/v1/model-policy", ctx.guards.requireModelPolicyManage(ctx.modules.AIGovernance.ModelGovernanceHandler.UpdatePolicy))
	mux.Handle("GET /api/v1/platform/model-api-configs", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ListManagedAPIConfigs))
	mux.Handle("POST /api/v1/platform/model-api-configs", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/resolve", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ResolveManagedAPIProvider))
	mux.Handle("POST /api/v1/platform/model-api-configs/validate", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ValidateManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/models", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ListAvailableManagedAPIModels))
	mux.Handle("POST /api/v1/platform/model-api-configs/auto", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.AutoCreateManagedAPIConfig))
	mux.Handle("PATCH /api/v1/platform/model-api-configs/{id}", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.UpdateManagedAPIConfig))
	mux.Handle("DELETE /api/v1/platform/model-api-configs/{id}", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.DeleteManagedAPIConfig))
	mux.Handle("POST /api/v1/platform/model-api-configs/{id}/probe", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ProbeManagedAPIConfig))
	mux.Handle("GET /api/v1/platform/panel-model-bindings", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.ListPlatformPanelRoleBindings))
	mux.Handle("PUT /api/v1/platform/panel-model-bindings", ctx.guards.requirePlatformModelManage(ctx.modules.AIGovernance.ModelGovernanceHandler.SavePlatformPanelRoleBinding))
	mux.Handle("GET /api/v1/ai-chat/model", ctx.guards.requireSchoolAdmin(ctx.modules.AIGovernance.ModelGovernanceHandler.GetManagedChatModel))
	mux.Handle("POST /api/v1/ai-chat/completions", ctx.guards.requireSchoolAdmin(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateManagedChatCompletion))
	mux.Handle("POST /api/v1/ai-chat/completions/stream", ctx.guards.requireSchoolAdmin(ctx.modules.AIGovernance.ModelGovernanceHandler.StreamManagedChatCompletion))
	mux.Handle("GET /api/v1/model-sandbox-approvals", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListSandboxApprovals))
	mux.Handle("POST /api/v1/model-sandbox-approvals", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateSandboxApproval))
	mux.Handle("POST /api/v1/model-sandbox-approvals/{id}/revoke", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.RevokeSandboxApproval))
	mux.Handle("GET /api/v1/model-evaluation-runs", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListEvaluationRuns))
	mux.Handle("POST /api/v1/model-evaluation-runs", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/candidates", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.AddEvaluationCandidate))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/complete", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CompleteEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/invalidate", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.InvalidateEvaluationRun))
	mux.Handle("GET /api/v1/model-approvals", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListModelApprovals))
	mux.Handle("POST /api/v1/model-approvals", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateModelApproval))
	mux.Handle("POST /api/v1/model-approvals/{id}/revoke", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.RevokeModelApproval))
	if ctx.modules.AIGovernance.EligibilityHandler != nil {
		aieligibility.RegisterRoutes(mux, ctx.modules.AIGovernance.EligibilityHandler, ctx.guards.requireModelRead, ctx.guards.requireModelPolicyManage)
	}
	gradingevaluation.RegisterRoutes(mux, ctx.modules.AIGovernance.GradingEvaluationHandler, ctx.guards.requireModelEvaluationManage)
	modelcalibration.RegisterRoutes(mux, ctx.modules.AIGovernance.ModelCalibrationHandler, ctx.guards.requireModelEvaluationManage)
	aidisagreement.RegisterRoutes(mux, ctx.modules.AIGovernance.DisagreementHandler, ctx.guards.requireReviewWork, ctx.guards.requireReviewManage)
}

package server

import (
	"bytes"
	"edugrade-enterprise/services/api-gateway/internal/aidisagreement"
	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func legacyModelWriteGone(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Deprecation", "true")
	httpx.Error(w, r, http.StatusGone, "legacy_model_registration_closed", "请在模型管理中配置学校模型")
}

func requireManagedModelWrite(handler http.HandlerFunc, policy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_model_request", "无法读取模型请求")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var input map[string]json.RawMessage
		if json.Unmarshal(body, &input) == nil {
			if policy {
				var legacy []string
				_ = json.Unmarshal(input["allowed_deployments"], &legacy)
				if len(legacy) > 0 {
					httpx.Error(w, r, http.StatusGone, "legacy_model_policy_closed", "请使用学校模型配置 ID 更新策略")
					return
				}
			} else {
				var id string
				_ = json.Unmarshal(input["model_config_id"], &id)
				if id == "" {
					httpx.Error(w, r, http.StatusGone, "legacy_model_reference_closed", "请使用学校模型配置 ID")
					return
				}
			}
		}
		handler(w, r)
	}
}

func withGovernanceSchoolScope(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFromContext(r.Context())
		if !ok {
			httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		target := r.URL.Query().Get("tenant_id")
		if target != "" && target != user.TenantID {
			if user.TenantID != auth.PlatformTenantID ||
				!auth.HasPermission(user, "model:config:manage") || uuid.Validate(target) != nil {
				httpx.Error(w, r, http.StatusForbidden, "tenant_scope_forbidden", "target tenant is outside the caller scope")
				return
			}
			user.TenantID = target
			r = r.WithContext(auth.WithUser(r.Context(), user))
		}
		handler(w, r)
	}
}

func registerGovernanceRoutes(mux *http.ServeMux, ctx routerContext) {
	ctx.modules.AIGovernance.ModelGovernanceHandler.WithManagedAPIProbeObserver(ctx.metrics)
	mux.Handle("GET /api/v1/model-providers", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListProviders))
	mux.Handle("POST /api/v1/model-providers", ctx.guards.requireModelProviderManage(legacyModelWriteGone))
	mux.Handle("PATCH /api/v1/model-providers/{id}/status", ctx.guards.requireModelProviderManage(legacyModelWriteGone))
	mux.Handle("GET /api/v1/model-deployments", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListDeployments))
	mux.Handle("POST /api/v1/model-deployments", ctx.guards.requireModelProviderManage(legacyModelWriteGone))
	mux.Handle("PATCH /api/v1/model-deployments/{id}/state", ctx.guards.requireModelProviderManage(legacyModelWriteGone))
	mux.Handle("GET /api/v1/model-policy", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.GetPolicy))
	mux.Handle("GET /api/v1/model-prompts/current", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.GetCurrentPrompt))
	mux.Handle("PUT /api/v1/model-policy", ctx.guards.requireModelPolicyManage(requireManagedModelWrite(ctx.modules.AIGovernance.ModelGovernanceHandler.UpdatePolicy, true)))
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
	mux.Handle("POST /api/v1/model-sandbox-approvals", ctx.guards.requireModelProviderManage(requireManagedModelWrite(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateSandboxApproval, false)))
	mux.Handle("POST /api/v1/model-sandbox-approvals/{id}/revoke", ctx.guards.requireModelProviderManage(ctx.modules.AIGovernance.ModelGovernanceHandler.RevokeSandboxApproval))
	mux.Handle("GET /api/v1/model-evaluation-runs", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListEvaluationRuns))
	mux.Handle("POST /api/v1/model-evaluation-runs", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/candidates", ctx.guards.requireModelEvaluationManage(requireManagedModelWrite(ctx.modules.AIGovernance.ModelGovernanceHandler.AddEvaluationCandidate, false)))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/complete", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.CompleteEvaluationRun))
	mux.Handle("POST /api/v1/model-evaluation-runs/{id}/invalidate", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.InvalidateEvaluationRun))
	mux.Handle("GET /api/v1/model-approvals", ctx.guards.requireModelRead(ctx.modules.AIGovernance.ModelGovernanceHandler.ListModelApprovals))
	mux.Handle("POST /api/v1/model-approvals", ctx.guards.requireModelEvaluationManage(requireManagedModelWrite(ctx.modules.AIGovernance.ModelGovernanceHandler.CreateModelApproval, false)))
	mux.Handle("POST /api/v1/model-approvals/{id}/revoke", ctx.guards.requireModelEvaluationManage(ctx.modules.AIGovernance.ModelGovernanceHandler.RevokeModelApproval))
	if ctx.modules.AIGovernance.EligibilityHandler != nil {
		aieligibility.RegisterRoutes(mux, ctx.modules.AIGovernance.EligibilityHandler,
			func(h http.HandlerFunc) http.Handler {
				return ctx.guards.requireModelRead(withGovernanceSchoolScope(h))
			},
			func(h http.HandlerFunc) http.Handler {
				return ctx.guards.requireModelPolicyManage(withGovernanceSchoolScope(h))
			})
	}
	manageEvaluation := func(h http.HandlerFunc) http.Handler {
		return ctx.guards.requireModelEvaluationManage(withGovernanceSchoolScope(h))
	}
	gradingevaluation.RegisterRoutes(mux, ctx.modules.AIGovernance.GradingEvaluationHandler, manageEvaluation)
	modelcalibration.RegisterRoutes(mux, ctx.modules.AIGovernance.ModelCalibrationHandler, manageEvaluation)
	readDisagreement := func(h http.HandlerFunc) http.Handler {
		return ctx.guards.requireAuth(auth.RequireAnyPermission("review:work", "review:manage", "model:governance:read")(withGovernanceSchoolScope(h)))
	}
	manageDisagreement := func(h http.HandlerFunc) http.Handler {
		return ctx.guards.requireAuth(auth.RequireAnyPermission("review:manage", "model:policy:manage")(withGovernanceSchoolScope(h)))
	}
	aidisagreement.RegisterRoutes(mux, ctx.modules.AIGovernance.DisagreementHandler, readDisagreement, manageDisagreement)
}

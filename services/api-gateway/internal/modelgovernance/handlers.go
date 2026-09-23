package modelgovernance

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

type Handler struct {
	store                   Store
	audit                   auth.AuditRecorder
	secrets                 SecretReferenceResolver
	baseline                LocalBaseline
	prompts                 RuntimePromptSource
	managedAPIProber        ManagedAPIProber
	managedAPIProbeObserver ManagedAPIProbeObserver
	managedAPIChatter       ManagedAPIChatter
	chatMu                  sync.Mutex
	activeChats             map[string]int
}

func NewHandler(store Store, audit auth.AuditRecorder, secrets SecretReferenceResolver, baseline LocalBaseline) *Handler {
	return &Handler{store: store, audit: audit, secrets: secrets, baseline: baseline, managedAPIProber: NewHTTPManagedAPIProber(0)}
}

func (h *Handler) WithRuntimePromptSource(source RuntimePromptSource) *Handler {
	h.prompts = source
	return h
}

func (h *Handler) WithManagedAPIChatter(chatter ManagedAPIChatter) *Handler {
	h.managedAPIChatter = chatter
	return h
}

func (h *Handler) GetCurrentPrompt(w http.ResponseWriter, r *http.Request) {
	if h.prompts == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "runtime_prompt_unavailable", "评分服务当前不可用")
		return
	}
	prompt, err := h.prompts.Current(r.Context())
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "runtime_prompt_unavailable", "无法读取评分服务当前提示词")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"prompt": prompt})
}

func (h *Handler) ListProviders(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok || !h.ensureBaseline(w, r, tenantID) {
		return
	}
	items, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", "failed to list model providers")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"providers": items})
}

func (h *Handler) CreateProvider(w http.ResponseWriter, r *http.Request) {
	var input ProviderInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	provider := ProviderFromInput(input)
	if provider.Status == "" {
		provider.Status = "unverified"
		input.Status = provider.Status
	}
	if strings.TrimSpace(input.DisplayName) == "" || ValidateProvider(provider) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider", "invalid model provider configuration")
		return
	}
	if provider.Kind == ProviderExternal {
		probe, err := h.secrets.Probe(provider.CredentialRef)
		if err != nil {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_secret_reference", "credential_ref must be a supported secret reference URI")
			return
		}
		if provider.Status != "unverified" && provider.Status != "disabled" {
			httpx.Error(w, r, http.StatusConflict, "external_provider_not_verified", "external providers cannot be activated before native adapter verification")
			return
		}
		if provider.Status != "disabled" && probe.ResolverSupported && !probe.Configured {
			httpx.Error(w, r, http.StatusConflict, "secret_not_configured", "the referenced secret is not configured")
			return
		}
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateProvider(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_provider_create_failed", "failed to create model provider")
		return
	}
	h.auditAction(r, "model.provider_created", "model_provider", item.ID, input.Reason(),
		map[string]any{"tenant_id": tenantID, "provider_key": item.Key, "provider_kind": item.Kind, "adapter_type": item.AdapterType, "region": item.Region, "status": item.Status})
	httpx.JSON(w, http.StatusCreated, map[string]any{"provider": item})
}

func (h *Handler) UpdateProviderStatus(w http.ResponseWriter, r *http.Request) {
	var input ProviderStatusInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" || !validProviderStatus(input.Status) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider_status", "valid status and reason are required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", "failed to inspect model provider")
		return
	}
	current, exists := providerByID(providers, r.PathValue("id"))
	if !exists {
		httpx.Error(w, r, http.StatusNotFound, "model_provider_not_found", "model provider not found")
		return
	}
	if current.Kind == ProviderExternal && input.Status != "unverified" && input.Status != "disabled" {
		httpx.Error(w, r, http.StatusConflict, "external_provider_not_verified", "external providers cannot be activated before native adapter verification")
		return
	}
	item, err := h.store.UpdateProviderStatus(r.Context(), tenantID, current.ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_provider_update_failed", "failed to update model provider")
		return
	}
	h.auditAction(r, "model.provider_status_updated", "model_provider", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "provider_key": item.Key, "before_status": current.Status, "status": item.Status})
	httpx.JSON(w, http.StatusOK, map[string]any{"provider": item})
}

func (h *Handler) ListDeployments(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok || !h.ensureBaseline(w, r, tenantID) {
		return
	}
	items, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to list model deployments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployments": items})
}

func (h *Handler) CreateDeployment(w http.ResponseWriter, r *http.Request) {
	var input DeploymentInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if input.Status == "" {
		input.Status = "unverified"
	}
	if input.HealthState == "" {
		input.HealthState = "unverified"
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", "failed to inspect model provider")
		return
	}
	provider, exists := providerByID(providers, input.ProviderID)
	if !exists {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_provider", "provider_id does not belong to the target tenant")
		return
	}
	deployment := DeploymentFromInput(input, provider.Key)
	if strings.TrimSpace(input.ModelName) == "" ||
		ValidateDeployment(deployment, provider) != nil ||
		len(input.PricingPolicy) == 0 ||
		strings.TrimSpace(stringValue(input.PricingPolicy["meter"])) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_deployment", "invalid model deployment configuration")
		return
	}
	if provider.Kind == ProviderExternal &&
		(input.Status != "unverified" || input.HealthState != "unverified") {
		httpx.Error(w, r, http.StatusConflict, "external_deployment_not_verified", "external deployments must remain unverified until native adapter acceptance")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateDeployment(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_deployment_create_failed", "failed to create model deployment")
		return
	}
	h.auditAction(r, "model.deployment_created", "model_deployment", item.ID, "create governed model deployment",
		map[string]any{"tenant_id": tenantID, "provider_key": item.ProviderKey, "deployment_key": item.Key, "region": item.Region, "status": item.Status, "health_state": item.HealthState})
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": item})
}

func (h *Handler) UpdateDeploymentState(w http.ResponseWriter, r *http.Request) {
	var input DeploymentStateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" || !validDeploymentStatus(input.Status) || !validHealthState(input.HealthState) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_deployment_state", "valid status, health_state and reason are required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	deployments, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to inspect model deployment")
		return
	}
	current, exists := deploymentByID(deployments, r.PathValue("id"))
	if !exists {
		httpx.Error(w, r, http.StatusNotFound, "model_deployment_not_found", "model deployment not found")
		return
	}
	providers, err := h.store.ListProviders(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_provider_list_failed", "failed to inspect model provider")
		return
	}
	provider, exists := providerByID(providers, current.ProviderID)
	if !exists {
		httpx.Error(w, r, http.StatusConflict, "model_provider_not_found", "deployment provider is unavailable")
		return
	}
	externalStateAllowed := input.Status == "unverified" && input.HealthState == "unverified" ||
		input.Status == "disabled" &&
			(input.HealthState == "unverified" || input.HealthState == "unavailable" || input.HealthState == "disabled")
	if provider.Kind == ProviderExternal && !externalStateAllowed {
		httpx.Error(w, r, http.StatusConflict, "external_deployment_not_verified", "external deployments cannot enter routing before native adapter acceptance")
		return
	}
	item, err := h.store.UpdateDeploymentState(r.Context(), tenantID, current.ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_deployment_update_failed", "failed to update model deployment")
		return
	}
	h.auditAction(r, "model.deployment_state_updated", "model_deployment", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "deployment_key": item.Key, "before_status": current.Status, "status": item.Status, "before_health_state": current.HealthState, "health_state": item.HealthState})
	httpx.JSON(w, http.StatusOK, map[string]any{"deployment": item})
}

func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok || !h.ensureBaseline(w, r, tenantID) {
		return
	}
	item, err := h.store.GetPolicy(r.Context(), tenantID)
	if err != nil {
		writeStoreError(w, r, err, "model_policy_read_failed", "failed to read model policy")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}

func (h *Handler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var input PolicyUpdateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	if input.AllowedDeployments == nil {
		input.AllowedDeployments = []string{}
	}
	policy := PolicyFromUpdate(input)
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedVersion < 1 || ValidateTenantPolicy(policy) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_policy", "valid policy, expected_version and reason are required")
		return
	}
	deployments, err := h.store.ListDeployments(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_deployment_list_failed", "failed to validate allowed deployments")
		return
	}
	if !deploymentKeysExist(input.AllowedDeployments, deployments) {
		httpx.Error(w, r, http.StatusBadRequest, "unknown_model_deployment", "allowed_deployments contains an unknown tenant deployment")
		return
	}
	item, err := h.store.UpdatePolicy(r.Context(), tenantID, mustUser(r).ID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_policy_update_failed", "failed to update model policy")
		return
	}
	h.auditAction(r, "model.policy_updated", "tenant_model_policy", item.ID, input.Reason,
		map[string]any{"tenant_id": tenantID, "mode": item.Mode, "external_enabled": item.ExternalEnabled, "text_export_enabled": item.TextExportEnabled, "image_export_enabled": item.ImageExportEnabled, "allowed_deployments": item.AllowedDeployments, "version": item.Version})
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": item})
}

func (h *Handler) ListSandboxApprovals(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	items, err := h.store.ListSandboxApprovals(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "sandbox_approval_list_failed", "failed to list sandbox approvals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"approvals": items})
}

func (h *Handler) CreateSandboxApproval(w http.ResponseWriter, r *http.Request) {
	var input SandboxApprovalInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if ValidateSandboxApprovalInput(input, time.Now().UTC()) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_sandbox_approval", "sandbox approval facts are incomplete or invalid")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateSandboxApproval(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "sandbox_approval_create_failed", "failed to create sandbox approval")
		return
	}
	h.auditAction(r, "model.sandbox_approval_created", "model_sandbox_approval", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "provider_key": item.ProviderKey,
			"deployment_key": item.DeploymentKey, "protocol": item.Protocol,
			"approved_region": item.ApprovedRegion, "approval_reference": item.ApprovalReference,
			"image_export_reviewed": item.ImageExportReviewed, "expires_at": item.ExpiresAt,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"approval": item})
}

func (h *Handler) RevokeSandboxApproval(w http.ResponseWriter, r *http.Request) {
	var input SandboxApprovalRevokeInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_sandbox_approval_revocation", "revocation reason is required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.RevokeSandboxApproval(
		r.Context(), tenantID, actorID, r.PathValue("id"), input.Reason,
	)
	if err != nil {
		writeStoreError(w, r, err, "sandbox_approval_revoke_failed", "failed to revoke sandbox approval")
		return
	}
	h.auditAction(r, "model.sandbox_approval_revoked", "model_sandbox_approval", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "provider_key": item.ProviderKey,
			"deployment_key": item.DeploymentKey, "revoked_at": item.RevokedAt,
		})
	httpx.JSON(w, http.StatusOK, map[string]any{"approval": item})
}

func (h *Handler) ListEvaluationRuns(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 30, 100)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	items, err := h.store.ListEvaluationRuns(r.Context(), tenantID, EvaluationListFilter{
		Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID,
	})
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_evaluation_list_failed", "failed to list model evaluations")
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_runs": items, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) CreateEvaluationRun(w http.ResponseWriter, r *http.Request) {
	var input EvaluationRunInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if ValidateEvaluationRunInput(input) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation", "invalid offline model evaluation")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateEvaluationRun(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_create_failed", "failed to create model evaluation")
		return
	}
	h.auditAction(r, "model.evaluation_created", "model_evaluation_run", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_key": item.Key,
			"dataset_reference": item.DatasetReference, "dataset_sha256": item.DatasetSHA256,
			"authorization_reference": item.AuthorizationRef,
			"evidence_class":          item.EvidenceClass, "subject": item.Subject,
			"grade": item.Grade, "question_type": item.QuestionType,
			"modality": item.Modality, "sample_count": item.SampleCount,
			"repeat_count": item.RepeatCount,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"evaluation_run": item})
}

func (h *Handler) AddEvaluationCandidate(w http.ResponseWriter, r *http.Request) {
	var input EvaluationCandidateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation_candidate", "candidate reason is required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.AddEvaluationCandidate(
		r.Context(), tenantID, actorID, r.PathValue("id"), input,
	)
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_candidate_create_failed", "failed to add model evaluation candidate")
		return
	}
	h.auditAction(r, "model.evaluation_candidate_added", "model_evaluation_candidate", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_id": item.RunID,
			"provider_key": item.ProviderKey, "deployment_key": item.DeploymentKey,
			"model_version": item.ModelVersion, "prompt_version": item.PromptVersion,
			"rubric_version": item.RubricVersion, "evaluated_samples": item.EvaluatedSamples,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"candidate": item})
}

func (h *Handler) CompleteEvaluationRun(w http.ResponseWriter, r *http.Request) {
	h.transitionEvaluationRun(w, r, "complete")
}

func (h *Handler) InvalidateEvaluationRun(w http.ResponseWriter, r *http.Request) {
	h.transitionEvaluationRun(w, r, "invalidate")
}

func (h *Handler) transitionEvaluationRun(w http.ResponseWriter, r *http.Request, transition string) {
	var input EvaluationTransitionInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_evaluation_transition", "transition reason is required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	var (
		item EvaluationRun
		err  error
	)
	switch transition {
	case "complete":
		item, err = h.store.CompleteEvaluationRun(
			r.Context(), tenantID, actorID, r.PathValue("id"), input.Reason,
		)
	case "invalidate":
		item, err = h.store.InvalidateEvaluationRun(
			r.Context(), tenantID, actorID, r.PathValue("id"), input.Reason,
		)
	default:
		err = ErrInvalidEvaluation
	}
	if err != nil {
		writeStoreError(w, r, err, "model_evaluation_transition_failed", "failed to transition model evaluation")
		return
	}
	action := "model.evaluation_completed"
	if transition == "invalidate" {
		action = "model.evaluation_invalidated"
	}
	h.auditAction(r, action, "model_evaluation_run", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "run_key": item.Key,
			"evidence_class": item.EvidenceClass, "status": item.Status,
			"candidate_count": len(item.Candidates),
		})
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_run": item})
}

func (h *Handler) ListModelApprovals(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	items, err := h.store.ListModelApprovals(r.Context(), tenantID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "model_approval_list_failed", "failed to list model approvals")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"model_approvals": items})
}

func (h *Handler) CreateModelApproval(w http.ResponseWriter, r *http.Request) {
	var input ModelApprovalInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok {
		return
	}
	if ValidateModelApprovalInput(input, time.Now().UTC()) != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_approval", "model approval facts are incomplete or invalid")
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.CreateModelApproval(r.Context(), tenantID, actorID, input)
	if err != nil {
		writeStoreError(w, r, err, "model_approval_create_failed", "failed to create model approval")
		return
	}
	h.auditAction(r, "model.approval_created", "model_approval", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "evaluation_run_id": item.EvaluationRunID,
			"provider_key": item.ProviderKey, "deployment_key": item.DeploymentKey,
			"model_version": item.ModelVersion, "prompt_version": item.PromptVersion,
			"rubric_version": item.RubricVersion, "subject": item.Subject,
			"grade": item.Grade, "question_type": item.QuestionType,
			"modality": item.Modality, "manual_review_rate": item.ManualReviewRate,
			"decision_reference": item.DecisionReference, "expires_at": item.ExpiresAt,
		})
	httpx.JSON(w, http.StatusCreated, map[string]any{"model_approval": item})
}

func (h *Handler) RevokeModelApproval(w http.ResponseWriter, r *http.Request) {
	var input ModelApprovalRevokeInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedRevision < 1 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_approval_revocation", "revocation reason and expected_revision are required")
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok {
		return
	}
	user := mustUser(r)
	actorID := user.ID
	if tenantID != user.TenantID {
		actorID = ""
	}
	item, err := h.store.RevokeModelApproval(
		r.Context(), tenantID, actorID, r.PathValue("id"), input,
	)
	if err != nil {
		writeStoreError(w, r, err, "model_approval_revoke_failed", "failed to revoke model approval")
		return
	}
	h.auditAction(r, "model.approval_revoked", "model_approval", item.ID, input.Reason,
		map[string]any{
			"tenant_id": item.TenantID, "provider_key": item.ProviderKey,
			"deployment_key": item.DeploymentKey, "decision_reference": item.DecisionReference,
			"revoked_at": item.RevokedAt,
		})
	httpx.JSON(w, http.StatusOK, map[string]any{"model_approval": item})
}

func (h *Handler) ensureBaseline(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if err := h.store.EnsureLocalBaseline(r.Context(), tenantID, h.baseline); err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "local_model_registry_failed", "failed to register local model baseline")
		return false
	}
	return true
}

func (h *Handler) targetTenant(w http.ResponseWriter, r *http.Request, requested string) (string, bool) {
	user := mustUser(r)
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == user.TenantID {
		return user.TenantID, true
	}
	if user.TenantID == auth.PlatformTenantID &&
		(hasPermission(user, "model:provider:manage") ||
			hasPermission(user, "model:evaluation:manage")) {
		return requested, true
	}
	httpx.Error(w, r, http.StatusForbidden, "tenant_scope_forbidden", "target tenant is outside the caller scope")
	return "", false
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string, after map[string]any) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		AfterValue: after,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain exactly one json object")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error, code string, message string) {
	switch {
	case errors.Is(err, ErrInvalidProvider), errors.Is(err, ErrInvalidDeployment),
		errors.Is(err, ErrInvalidPolicy), errors.Is(err, ErrInvalidApproval),
		errors.Is(err, ErrInvalidEvaluation), errors.Is(err, ErrInvalidPromotion):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_governance_request", "invalid model governance request")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "model_governance_not_found", "model governance resource not found")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "resource was updated in another session; refresh and retry")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "model_governance_conflict", "model governance resource conflict")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, code, message)
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func hasPermission(user auth.User, expected string) bool {
	for _, permission := range user.Permissions {
		if permission == expected {
			return true
		}
	}
	return false
}

func providerByID(items []Provider, id string) (Provider, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Provider{}, false
}

func deploymentByID(items []Deployment, id string) (Deployment, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return Deployment{}, false
}

func deploymentKeysExist(expected []string, items []Deployment) bool {
	available := make(map[string]bool, len(items))
	for _, item := range items {
		available[item.Key] = true
	}
	for _, key := range expected {
		if !available[key] {
			return false
		}
	}
	return true
}

func validProviderStatus(value string) bool {
	return value == "unverified" || value == "active" || value == "degraded" || value == "rate_limited" || value == "disabled"
}

func validDeploymentStatus(value string) bool {
	return value == "unverified" || value == "shadow_only" || value == "disabled"
}

func validHealthState(value string) bool {
	return value == "unverified" || value == "available" || value == "degraded" || value == "rate_limited" || value == "unavailable" || value == "disabled"
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (input ProviderInput) Reason() string {
	if input.Kind == ProviderExternal {
		return "register external provider metadata and secret reference"
	}
	return "register local provider metadata"
}

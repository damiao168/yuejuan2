package modelgovernance

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"net/http"
	"strings"
	"time"
)

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

package modelgovernance

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

type savePlatformPanelRoleInput struct {
	TenantID string `json:"tenant_id"`
	SaveModelRoleBindingInput
}

func (h *Handler) panelRoleAdminStore(w http.ResponseWriter, r *http.Request) (ModelRoleBindingAdminStore, bool) {
	store, ok := h.store.(ModelRoleBindingAdminStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "panel_model_binding_unavailable", "三智能体模型配置暂不可用")
		return nil, false
	}
	return store, true
}

func (h *Handler) ListPlatformPanelRoleBindings(w http.ResponseWriter, r *http.Request) {
	store, ok := h.panelRoleAdminStore(w, r)
	if !ok {
		return
	}
	tenantID, ok := h.targetTenant(w, r, r.URL.Query().Get("tenant_id"))
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		writePanelRoleBindingError(w, r, ErrInvalidManagedConfig)
		return
	}
	items, err := store.ListConfiguredModelRoleBindings(r.Context(), tenantID,
		r.URL.Query().Get("education_stage"), r.URL.Query().Get("subject_code"), r.URL.Query().Get("archetype_code"))
	if err != nil {
		writePanelRoleBindingError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bindings": items})
}

func (h *Handler) SavePlatformPanelRoleBinding(w http.ResponseWriter, r *http.Request) {
	store, ok := h.panelRoleAdminStore(w, r)
	if !ok {
		return
	}
	var input savePlatformPanelRoleInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetTenant(w, r, input.TenantID)
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		writePanelRoleBindingError(w, r, ErrInvalidManagedConfig)
		return
	}
	if _, err := uuid.Parse(input.ManagedModelAPIConfigID); err != nil {
		writePanelRoleBindingError(w, r, ErrInvalidManagedConfig)
		return
	}
	normalized, err := normalizeRoleBinding(input.SaveModelRoleBindingInput)
	if err != nil {
		writePanelRoleBindingError(w, r, err)
		return
	}
	item, err := store.SaveModelRoleBinding(r.Context(), tenantID, mustUser(r).ID, normalized)
	if err != nil {
		writePanelRoleBindingError(w, r, err)
		return
	}
	h.auditAction(r, "model.panel_role_bound", "model_role_binding", item.ID, "configure school rubric-scoring panel role",
		map[string]any{"tenant_id": tenantID, "education_stage": item.EducationStage, "subject_code": item.SubjectCode,
			"archetype_code": item.ArchetypeCode, "agent_role": item.AgentRole,
			"managed_model_api_config_id": item.ManagedModelAPIConfigID, "prompt_version": item.PromptVersion,
			"strength_rank": item.StrengthRank, "status": item.Status})
	httpx.JSON(w, http.StatusOK, map[string]any{"binding": item})
}

func writePanelRoleBindingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidManagedConfig):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_panel_model_binding", "三智能体模型配置不完整或范围无效")
	case errors.Is(err, ErrManagedCapabilityRequired), errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusConflict, "panel_model_capability_required", "模型必须属于该学校且通过完整结构化能力检测")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "panel_model_binding_failed", "三智能体模型配置操作失败")
	}
}

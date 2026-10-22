package modelgovernance

import (
	"errors"
	"net/http"

	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"github.com/google/uuid"
)

func (h *Handler) GetPlatformModelApplications(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := h.targetManagedTenant(w, r, r.URL.Query().Get("tenant_id"), "model:read")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	store, ok := h.store.(ModelApplicationStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "model_applications_unavailable", "模型用途配置暂不可用")
		return
	}
	// 用途表和角色表受学校 RLS 约束；授权后只给这些查询绑定目标学校，保留原认证身份。
	ctx := database.WithTenant(r.Context(), tenantID)
	selection, err := store.GetDocumentModelSelection(ctx, tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	uses, err := store.ListGradingModelUses(ctx, tenantID)
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	answerSheet := initialAnswerSheetSelection()
	if visual, ok := h.store.(AnswerSheetModelApplicationStore); ok {
		answerSheet, err = visual.GetAnswerSheetModelSelection(ctx, tenantID)
		if err != nil {
			h.writeManagedAPIError(w, r, err)
			return
		}
	}
	grading := []string{}
	gradingSelection := initialGradingSelection()
	if configured, ok := h.store.(GradingModelApplicationStore); ok {
		gradingSelection, err = configured.GetGradingModelSelection(ctx, tenantID)
		if err != nil {
			h.writeManagedAPIError(w, r, err)
			return
		}
	}
	seen := map[string]bool{}
	if gradingSelection.Mode == "model" {
		grading = append(grading, gradingSelection.ModelConfigID)
		seen[gradingSelection.ModelConfigID] = true
	}
	for _, use := range uses {
		if !seen[use.ModelConfigID] {
			grading = append(grading, use.ModelConfigID)
			seen[use.ModelConfigID] = true
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"document_selection": selection, "answer_sheet_selection": answerSheet, "grading_selection": gradingSelection, "grading_model_ids": grading, "grading_uses": uses})
}

func (h *Handler) SavePlatformDocumentModelSelection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TenantID string `json:"tenant_id"`
		DocumentModelSelectionInput
	}
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if _, err := uuid.Parse(tenantID); err != nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	store, ok := h.store.(ModelApplicationStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "model_applications_unavailable", "模型用途配置暂不可用")
		return
	}
	// 学校范围已校验；数据库按目标学校写入，审计仍记录发起操作的平台管理员。
	ctx := database.WithTenant(r.Context(), tenantID)
	selection, err := store.SaveDocumentModelSelection(ctx, tenantID, mustUser(r).ID, input.DocumentModelSelectionInput)
	if errors.Is(err, ErrDocumentSelectionChanged) {
		httpx.Error(w, r, http.StatusConflict, "document_model_selection_changed", "解析模型设置已被更新，请刷新核对后重新保存")
		return
	}
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.document_selected", "tenant_document_model_selection", tenantID, "configure school document model", map[string]any{"tenant_id": tenantID, "mode": selection.Mode, "model_config_id": selection.ModelConfigID})
	httpx.JSON(w, http.StatusOK, map[string]any{"selection": selection})
}

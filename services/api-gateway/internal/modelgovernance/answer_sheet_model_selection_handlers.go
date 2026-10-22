package modelgovernance

import (
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"errors"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) SavePlatformAnswerSheetModelSelection(w http.ResponseWriter, r *http.Request) {
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
	if uuid.Validate(tenantID) != nil {
		h.writeManagedAPIError(w, r, ErrInvalidManagedConfig)
		return
	}
	store, ok := h.store.(AnswerSheetModelApplicationStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "model_applications_unavailable", "模型用途配置暂不可用")
		return
	}
	// 只有经授权的目标学校进入 RLS；审计保留实际平台管理员身份。
	item, err := store.SaveAnswerSheetModelSelection(database.WithTenant(r.Context(), tenantID), tenantID, mustUser(r).ID, input.DocumentModelSelectionInput)
	if errors.Is(err, ErrAnswerSheetSelectionChanged) {
		httpx.Error(w, r, http.StatusConflict, "answer_sheet_model_selection_changed", "答题卡模型设置已更新，请刷新核对后再保存")
		return
	}
	if errors.Is(err, ErrAnswerSheetModelRequired) {
		httpx.Error(w, r, http.StatusBadRequest, "answer_sheet_model_required", "请选择已启用且已配置凭据的学校模型")
		return
	}
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.answer_sheet_selected", "tenant_answer_sheet_model_selection", tenantID, "configure school answer sheet vision model", map[string]any{"tenant_id": tenantID, "mode": item.Mode, "model_config_id": item.ModelConfigID})
	httpx.JSON(w, http.StatusOK, map[string]any{"selection": item})
}

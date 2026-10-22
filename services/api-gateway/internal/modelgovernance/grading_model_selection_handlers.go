package modelgovernance

import (
	"context"
	database "edugrade-enterprise/services/api-gateway/internal/db"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
)

func (h *Handler) WithGradingModelProbe(probe func(context.Context, string, string) (json.RawMessage, error)) *Handler {
	h.gradingModelProbe = probe
	return h
}

// ProbePlatformGradingModelSelection 只探测目标学校已经保存的选择，不能由请求直接传入密钥或任意接口。
func (h *Handler) ProbePlatformGradingModelSelection(w http.ResponseWriter, r *http.Request) {
	var input struct {
		TenantID  string `json:"tenant_id"`
		RequestID string `json:"request_id"`
	}
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	tenantID, ok := h.targetManagedTenant(w, r, input.TenantID, "model:managed_api:manage")
	if !ok || !managedSchoolTenant(w, r, tenantID) {
		return
	}
	if len(input.RequestID) < 8 || len(input.RequestID) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "检测标识无效")
		return
	}
	if h.gradingModelProbe == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "visual_model_unavailable", "自动阅卷图像服务尚未配置")
		return
	}
	result, err := h.gradingModelProbe(database.WithTenant(r.Context(), tenantID), tenantID, input.RequestID)
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "visual_model_probe_failed", "图片能力检测未通过，请核对所选模型是否支持图片及接口配置")
		return
	}
	h.auditAction(r, "model.grading_visual_probed", "tenant_grading_model_selection", tenantID, "probe selected school image grading model", map[string]any{"tenant_id": tenantID, "request_id": input.RequestID})
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) SavePlatformGradingModelSelection(w http.ResponseWriter, r *http.Request) {
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
	store, ok := h.store.(GradingModelApplicationStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "model_applications_unavailable", "模型用途配置暂不可用")
		return
	}
	// 只有经授权的目标学校进入 RLS；审计保留实际平台管理员身份。
	item, err := store.SaveGradingModelSelection(database.WithTenant(r.Context(), tenantID), tenantID, mustUser(r).ID, input.DocumentModelSelectionInput)
	if errors.Is(err, ErrGradingSelectionChanged) {
		httpx.Error(w, r, http.StatusConflict, "grading_model_selection_changed", "自动阅卷模型设置已更新，请刷新核对后再保存")
		return
	}
	if errors.Is(err, ErrGradingModelRequired) {
		httpx.Error(w, r, http.StatusBadRequest, "grading_model_required", "请选择已启用且已配置凭据的学校模型")
		return
	}
	if err != nil {
		h.writeManagedAPIError(w, r, err)
		return
	}
	h.auditAction(r, "model.grading_selected", "tenant_grading_model_selection", tenantID, "configure school image grading model", map[string]any{"tenant_id": tenantID, "mode": item.Mode, "model_config_id": item.ModelConfigID})
	httpx.JSON(w, http.StatusOK, map[string]any{"selection": item})
}

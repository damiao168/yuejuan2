package grading

import (
	"context"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type gradingAIPlanStore interface {
	GetGradingAIPlan(context.Context, string, string) (GradingAIPlanDetail, error)
	SaveGradingAIPlan(context.Context, string, string, string, GradingAIPlanInput) (GradingAIPlanDetail, error)
}

func (h *Handler) WithGradingAIProbe(probe func(context.Context, string, GradingAIModel, string, bool) (GradingVisionProbe, error)) *Handler {
	h.gradingAIProbe = probe
	return h
}

func canConfigureGradingAIPlan(user auth.User) bool {
	return auth.HasRole(user, "school_admin") || auth.HasRole(user, "tenant_admin") || auth.HasRole(user, "platform_admin")
}

// 状态查询只读当前图像服务的缓存；没有能力事实时不替管理员发起收费探测。
func (h *Handler) decorateGradingAIProbeStatus(r *http.Request, detail *GradingAIPlanDetail) {
	user := mustUser(r)
	detail.CanConfigure = canConfigureGradingAIPlan(user)
	selected := map[string]bool{detail.Plan.FallbackModelConfigID: true}
	for _, role := range detail.Plan.Roles {
		selected[role.ModelConfigID] = true
	}
	statusByID := map[string]GradingVisionProbe{}
	for index := range detail.AvailableModels {
		model := &detail.AvailableModels[index]
		model.VisionProbeStatus, model.VisionProbeReason = "not_checked", "probe_status_not_loaded"
		if !selected[model.ModelConfigID] || h.gradingAIProbe == nil {
			continue
		}
		status, err := h.gradingAIProbe(r.Context(), user.TenantID, *model, "image-status-"+model.ModelConfigID, true)
		if err != nil {
			model.VisionProbeStatus, model.VisionProbeReason = "unavailable", "visual_service_unavailable"
			continue
		}
		model.VisionProbeStatus, model.VisionProbeReason = status.VisionProbeStatus, status.VisionProbeReason
		statusByID[model.ModelConfigID] = status
	}
	for role, model := range detail.Plan.ResolvedRoles {
		if status, ok := statusByID[model.ModelConfigID]; ok {
			model.VisionProbeStatus, model.VisionProbeReason = status.VisionProbeStatus, status.VisionProbeReason
			detail.Plan.ResolvedRoles[role] = model
		}
	}
	if detail.Plan.SingleModel != nil {
		if status, ok := statusByID[detail.Plan.SingleModel.ModelConfigID]; ok {
			detail.Plan.SingleModel.VisionProbeStatus, detail.Plan.SingleModel.VisionProbeReason = status.VisionProbeStatus, status.VisionProbeReason
		}
	}
}

func (h *Handler) GetGradingAIPlan(w http.ResponseWriter, r *http.Request) {
	store, ok := h.store.(gradingAIPlanStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "grading_ai_plan_unavailable", "本场 AI 阅卷方案暂不可用")
		return
	}
	detail, err := store.GetGradingAIPlan(r.Context(), mustUser(r).TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.decorateGradingAIProbeStatus(r, &detail)
	httpx.JSON(w, http.StatusOK, detail)
}

func (h *Handler) SaveGradingAIPlan(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	// 评分管理权限还包含操作员；只有学校管理角色可以确认模型选择，教师不能换掉评分依据。
	if !canConfigureGradingAIPlan(user) {
		httpx.Error(w, r, http.StatusForbidden, "grading_ai_plan_admin_required", "请学校管理员确认本场 AI 阅卷方案")
		return
	}
	store, ok := h.store.(gradingAIPlanStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "grading_ai_plan_unavailable", "本场 AI 阅卷方案暂不可用")
		return
	}
	var input GradingAIPlanInput
	if !decodeJSON(w, r, &input) {
		return
	}
	detail, err := store.SaveGradingAIPlan(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if errors.Is(err, ErrGradingAIPlanChanged) {
		httpx.Error(w, r, http.StatusConflict, "grading_ai_plan_changed", "本场 AI 阅卷方案已更新，请刷新后核对")
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.ai_plan_confirmed", "exam_grading_ai_plan", r.PathValue("examId"), "confirm exam grading models and difference thresholds")
	h.decorateGradingAIProbeStatus(r, &detail)
	httpx.JSON(w, http.StatusOK, detail)
}

func (h *Handler) ProbeGradingAIPlan(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !canConfigureGradingAIPlan(user) {
		httpx.Error(w, r, http.StatusForbidden, "grading_ai_plan_admin_required", "请学校管理员检测本场 AI 阅卷模型")
		return
	}
	store, ok := h.store.(gradingAIPlanStore)
	if !ok || h.gradingAIProbe == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "grading_ai_plan_unavailable", "本场 AI 阅卷方案暂不可用")
		return
	}
	var input struct {
		ModelConfigID string `json:"model_config_id"`
		RequestID     string `json:"request_id"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if len(input.RequestID) < 8 || len(input.RequestID) > 128 {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	detail, err := store.GetGradingAIPlan(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 管理员可以先检测本校可用连接再确认方案；不能为探测而被迫先保存阅卷配置。
	for _, model := range detail.AvailableModels {
		if model.ModelConfigID != input.ModelConfigID {
			continue
		}
		result, err := h.gradingAIProbe(r.Context(), user.TenantID, model, input.RequestID, false)
		if err != nil {
			httpx.Error(w, r, http.StatusBadGateway, "visual_model_probe_failed", "图片能力检测暂未完成，请核对模型连接")
			return
		}
		h.auditAction(r, "grading.ai_plan_visual_probed", "exam_grading_ai_plan", r.PathValue("examId"), "explicitly probe selected exam image model")
		httpx.JSON(w, http.StatusOK, result)
		return
	}
	writeStoreError(w, r, ErrInvalidInput)
}

package paper

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type reviewedTemplateLocker interface {
	LockTemplateAtRevision(context.Context, string, string, string, int) (AnswerSheetTemplate, error)
}

func (h *Handler) ListTemplates(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	items, err := h.templates.ListTemplates(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": items})
}

func (h *Handler) CreateTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		writeConfigurationError(w, r, http.StatusBadRequest, "invalid_template_layout", err.Error())
		return
	}
	item, err := h.templates.CreateTemplate(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_created", "answer_sheet_template", item.ID, "create answer sheet template version")
	writeJSON(w, http.StatusCreated, map[string]any{"template": item})
}

func (h *Handler) UpdateTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdateTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.ExpectedRevision < 1 {
		writeConfigurationError(w, r, http.StatusBadRequest, "expected_revision_required", "expected_revision must be greater than 0")
		return
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		writeConfigurationError(w, r, http.StatusBadRequest, "invalid_template_layout", err.Error())
		return
	}
	item, err := h.templates.UpdateTemplate(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_updated", "answer_sheet_template", item.ID, "update answer sheet template draft")
	writeJSON(w, http.StatusOK, map[string]any{"template": item})
}

func (h *Handler) LockTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var item AnswerSheetTemplate
	var err error
	if r.ContentLength != 0 {
		var input struct {
			ExpectedRevision int `json:"expected_revision"`
		}
		if !decodeJSON(w, r, &input) {
			return
		}
		locker, ok := h.templates.(reviewedTemplateLocker)
		if !ok || input.ExpectedRevision < 1 {
			writeStoreError(w, r, ErrInvalidInput)
			return
		}
		item, err = locker.LockTemplateAtRevision(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input.ExpectedRevision)
	} else {
		// 兼容旧客户端空请求；新收录流程必须提交刚核对并保存的 revision。
		item, err = h.templates.LockTemplate(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_locked", "answer_sheet_template", item.ID, "lock answer sheet template")
	writeJSON(w, http.StatusOK, map[string]any{"template": item})
}

func (h *Handler) CloneTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	item, err := h.templates.CloneTemplate(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "paper.template_cloned", "answer_sheet_template", item.ID, "clone answer sheet template version")
	writeJSON(w, http.StatusCreated, map[string]any{"template": item})
}

func (h *Handler) GetExamTemplateBinding(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	state, err := h.templateBinding.GetExamTemplateBindingState(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	// 空绑定保留解绑后的游标，供合法重建使用；不能把“当前未绑定”解释成修订号归零。
	writeJSON(w, http.StatusOK, state)
}

func (h *Handler) BindExamTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input BindExamTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.templateBinding.BindExamTemplate(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.answer_sheet_template_bound", "exam", item.ExamID, "bind immutable answer sheet template version")
	writeJSON(w, http.StatusOK, map[string]any{"binding": item, "revision": item.Revision})
}

func (h *Handler) UnbindExamTemplate(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UnbindExamTemplateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.templateBinding.UnbindExamTemplate(r.Context(), user.TenantID, r.PathValue("examId"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.answer_sheet_template_unbound", "exam", item.ExamID, input.Reason)
	// 保留已解除的 binding 兼容旧消费者；revision 表示这次解绑提交后的游标。
	writeJSON(w, http.StatusOK, map[string]any{"binding": item, "revision": item.Revision + 1})
}

func (h *Handler) GetReadiness(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.readiness.Readiness(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"readiness": result})
}

// ConfirmReadiness 在提供复核 hash 时只冻结该版本；冲突返回最新准备度且不改变状态。
func (h *Handler) ConfirmReadiness(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ConfirmReadinessInput
	// 旧客户端没有请求体；新客户端带上复核 hash，由持锁仓储核对而非依赖界面二次读取。
	if r.ContentLength != 0 && !decodeJSON(w, r, &input) {
		return
	}
	if len(input.ExpectedConfigurationHash) > 128 || strings.TrimSpace(input.ExpectedConfigurationHash) != input.ExpectedConfigurationHash {
		writeConfigurationError(w, r, http.StatusBadRequest, "invalid_request", "配置校验值不合法")
		return
	}
	result, err := h.readiness.ConfirmReadiness(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		if errors.Is(err, ErrReadinessChanged) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "readiness_configuration_changed", "message": "考试准备内容已变化，请重新核对名单和评分方式后确认"}, "readiness": result})
			return
		}
		if errors.Is(err, ErrNotReady) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "exam_not_ready", "message": "考试配置尚未满足开考条件"}, "readiness": result})
			return
		}
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.readiness_confirmed", "exam", r.PathValue("examId"), "confirm exam configuration readiness")
	writeJSON(w, http.StatusOK, map[string]any{"readiness": result})
}

func (h *Handler) StartCollection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.readiness.StartCollection(r.Context(), user.TenantID, r.PathValue("examId"), user.ID)
	if err != nil {
		if errors.Is(err, ErrNotReady) {
			writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "readiness_confirmation_required", "message": "准备确认已失效，请重新检查并确认"}, "readiness": result})
			return
		}
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "exam.collection_started", "exam", r.PathValue("examId"), "start answer collection after readiness gate")
	writeJSON(w, http.StatusOK, map[string]any{"readiness": result, "status": "collecting"})
}

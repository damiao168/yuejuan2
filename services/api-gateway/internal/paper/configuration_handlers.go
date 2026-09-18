package paper

import (
	"errors"
	"net/http"
)

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
	item, err := h.templates.LockTemplate(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
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
	item, err := h.templateBinding.GetExamTemplateBinding(r.Context(), user.TenantID, r.PathValue("examId"))
	if errors.Is(err, ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"binding": nil})
		return
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"binding": item})
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
	writeJSON(w, http.StatusOK, map[string]any{"binding": item})
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
	writeJSON(w, http.StatusOK, map[string]any{"binding": item})
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

func (h *Handler) ConfirmReadiness(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	result, err := h.readiness.ConfirmReadiness(r.Context(), user.TenantID, r.PathValue("examId"), user.ID)
	if err != nil {
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

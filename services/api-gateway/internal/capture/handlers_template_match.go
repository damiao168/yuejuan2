package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"net/http"
)

func (h *Handler) CompleteTemplateMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runID := r.PathValue("runId")
	var input TemplateMatchResultInput
	if !decodeStrict(w, r, &input) {
		return
	}
	run, err := h.store.GetTemplateMatchRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_template_match_run" || task.SourceID != runID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "template_match_task_mismatch", "worker task does not belong to this template match run")
		return
	}
	var out TemplateMatchRun
	var registrations []RegistrationRun
	if transactional, ok := h.store.(TransactionalTemplateMatchStore); ok {
		out, registrations, err = transactional.SubmitTemplateMatchResultCommand(r.Context(), user.TenantID, runID, input)
	} else {
		out, registrations, err = h.store.ApplyTemplateMatchResult(r.Context(), user.TenantID, runID, input)
	}
	if err != nil {
		writeTemplateMatchCommandError(w, r, err)
		return
	}
	// 生产存储已在同一事务完成任务租约；内存替身才在这里补做任务完成调用。
	if _, ok := h.store.(TransactionalTemplateMatchStore); !ok {
		result := map[string]any{"template_match_run_id": runID, "result_version": input.ResultVersion, "decision": out.Decision, "selected_template_id": out.SelectedTemplateID, "score": out.Score, "margin": out.Margin}
		if _, err = h.runtime.Complete(r.Context(), user.TenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-template-match-result-v1", Result: result, DurationMS: input.DurationMS}); err != nil {
			writeRuntimeError(w, r, err)
			return
		}
	}
	h.auditAction(r, "page.template_matched", "page_template_match_run", out.ID, out.Decision)
	httpx.JSON(w, http.StatusOK, map[string]any{"run": out, "registration_runs": registrations})
}

func (h *Handler) FailTemplateMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runID := r.PathValue("runId")
	var input TemplateMatchFailureInput
	if !decodeStrict(w, r, &input) {
		return
	}
	run, err := h.store.GetTemplateMatchRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_template_match_run" || task.SourceID != runID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "template_match_task_mismatch", "worker task does not belong to this template match run")
		return
	}
	var out TemplateMatchRun
	var taskStatus string
	if transactional, ok := h.store.(TransactionalTemplateMatchStore); ok {
		out, taskStatus, err = transactional.SubmitTemplateMatchFailureCommand(r.Context(), user.TenantID, runID, input)
	} else {
		var updatedTask workerruntime.Task
		updatedTask, err = h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
		if err == nil {
			taskStatus = updatedTask.Status
			out, err = h.store.ApplyTemplateMatchFailure(r.Context(), user.TenantID, runID, input.ErrorCode, input.ErrorDetail, updatedTask.Status == workerruntime.StatusQueued)
		}
	}
	if err != nil {
		writeTemplateMatchCommandError(w, r, err)
		return
	}
	h.auditAction(r, "page.template_match_failed", "page_template_match_run", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"run": out, "task_status": taskStatus})
}

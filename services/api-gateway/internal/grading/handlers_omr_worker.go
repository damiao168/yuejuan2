package grading

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"errors"
	"net/http"
)

func (h *Handler) CompleteOMR(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil || h.files == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_dependencies_unavailable", "OMR result dependencies are unavailable")
		return
	}
	var input OMRResultInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.scoringStore().GetOMRRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "omr_run" || task.SourceID != run.ID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "omr_task_mismatch", "worker task does not belong to this OMR run")
		return
	}
	overlay, err := h.files.Get(r.Context(), user.TenantID, input.OverlayFileAssetID)
	// 叠加图必须属于当前 OMR 运行且哈希一致，避免把其他考试的识别产物写入本题。
	if err != nil || overlay.ExamID != run.ExamID || overlay.OwnerType != "omr_evidence" || overlay.OwnerID != run.ID || overlay.HashSHA256 != input.OverlaySHA256 {
		httpx.Error(w, r, http.StatusBadRequest, "omr_overlay_invalid", "OMR overlay has an invalid owner, hash, or exam")
		return
	}
	out, questionGrade, err := h.scoringStore().ApplyOMRResult(r.Context(), user.TenantID, run.ID, user.ID, input, h.engine)
	if err != nil {
		if isOMRRuntimeCompletionError(err) {
			httpx.Error(w, r, http.StatusConflict, "omr_task_completion_failed", "OMR task lease or result is no longer valid")
			return
		}
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_completed", "omr_run", out.ID, "complete OMR extraction")
	response := map[string]any{"omr_run": out}
	if questionGrade != nil {
		response["question_grade"] = questionGrade
	}
	httpx.JSON(w, http.StatusOK, response)
}

func (h *Handler) FailOMR(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if h.runtime == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_dependencies_unavailable", "OMR result dependencies are unavailable")
		return
	}
	commands, ok := h.store.(OMRFailureCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "omr_dependencies_unavailable", "OMR result dependencies are unavailable")
		return
	}
	var input OMRFailureInput
	if !decodeJSON(w, r, &input) {
		return
	}
	run, err := h.scoringStore().GetOMRRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "omr_run" || task.SourceID != run.ID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "omr_task_mismatch", "worker task does not belong to this OMR run")
		return
	}
	// 来源写入失败时不能先消费租约或结束 attempt；同一租约可在事务回滚后再次提交。
	out, taskStatus, err := commands.ApplyOMRWorkerFailure(r.Context(), user.TenantID, run.ID, input)
	if err != nil {
		if isOMRRuntimeCompletionError(err) || errors.Is(err, ErrRevisionConflict) {
			httpx.Error(w, r, http.StatusConflict, "omr_task_failure_rejected", "OMR task failure is no longer valid")
			return
		}
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "grading.omr_failed", "omr_run", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"omr_run": out, "task_status": taskStatus})
}

func isOMRRuntimeCompletionError(err error) bool {
	return errors.Is(err, workerruntime.ErrNotFound) ||
		errors.Is(err, workerruntime.ErrInvalidInput) ||
		errors.Is(err, workerruntime.ErrLeaseExpired) ||
		errors.Is(err, workerruntime.ErrLeaseMismatch) ||
		errors.Is(err, workerruntime.ErrConflict) ||
		errors.Is(err, workerruntime.ErrInvalidTransition)
}

package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"net/http"
)

func (h *Handler) ProcessSubmissionPages(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runs, err := h.store.QueueSubmissionPages(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "submission.pages_processing_started", "submission", r.PathValue("id"), "queue page registration and segment crops")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"runs": runs})
}

func (h *Handler) ListRegistrationRuns(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runs, err := h.store.ListRegistrationRuns(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *Handler) GetProcessingSummary(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetProcessingSummary(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) ConfirmRegistration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RegistrationDecisionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmRegistration(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_confirmed", "page_registration_run", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"run": out})
}

func (h *Handler) RetryRegistration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	run, err := h.store.GetRegistrationRun(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, run.RuntimeTaskID)
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	coordinator, ok := h.store.(RegistrationRetryStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_command_unavailable", "atomic registration retry is unavailable")
		return
	}
	if _, err = coordinator.RetryRegistrationTask(r.Context(), user.TenantID, run.ID, task.ID, h.runtime); err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	out, err := h.store.GetRegistrationRun(r.Context(), user.TenantID, run.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_retried", "page_registration_run", out.ID, "manual retry")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"run": out})
}

func (h *Handler) CompleteRegistration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runID := r.PathValue("runId")
	var input RegistrationResultInput
	if !decodeStrict(w, r, &input) {
		return
	}
	run, err := h.store.GetRegistrationRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_registration_run" || task.SourceID != runID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "registration_task_mismatch", "worker task does not belong to this registration run")
		return
	}
	source, err := h.files.Get(r.Context(), user.TenantID, run.SourceFileAssetID)
	if err != nil || source.ExamID == "" {
		httpx.Error(w, r, http.StatusConflict, "registration_source_invalid", "registration source asset is missing")
		return
	}
	registered, err := h.files.Get(r.Context(), user.TenantID, input.RegisteredFileAssetID)
	if err != nil || registered.ExamID != source.ExamID || registered.HashSHA256 != input.RegisteredSHA256 {
		httpx.Error(w, r, http.StatusBadRequest, "registered_asset_invalid", "registered page asset is missing, has a different hash, or belongs to another exam")
		return
	}
	for _, segment := range input.Segments {
		asset, assetErr := h.files.Get(r.Context(), user.TenantID, segment.FileAssetID)
		if assetErr != nil || asset.ExamID != source.ExamID || asset.HashSHA256 != segment.SHA256 {
			httpx.Error(w, r, http.StatusBadRequest, "segment_crop_asset_invalid", "segment crop is missing, has a different hash, or belongs to another exam")
			return
		}
	}
	if err := h.validateGradingGroupAssets(r.Context(), user.TenantID, source.ExamID, "answer_segment_crop", runID, task.Payload, input.Segments); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "registration_grading_group_invalid", "shared grading crop does not match the frozen template or current registration")
		return
	}
	coordinator, ok := h.store.(RegistrationResultCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_command_unavailable", "atomic capture completion is unavailable")
		return
	}
	out, err := coordinator.SubmitRegistrationResultCommand(r.Context(), user.TenantID, runID, input, h.runtime)
	if err != nil {
		writeCaptureCompletionError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_completed", "page_registration_run", out.ID, "materialize registered page and segment crops")
	httpx.JSON(w, http.StatusOK, map[string]any{"run": out})
}

func (h *Handler) FailRegistration(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	runID := r.PathValue("runId")
	var input RegistrationFailureInput
	if !decodeStrict(w, r, &input) {
		return
	}
	run, err := h.store.GetRegistrationRun(r.Context(), user.TenantID, runID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_registration_run" || task.SourceID != runID || run.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "registration_task_mismatch", "worker task does not belong to this registration run")
		return
	}
	updatedTask, err := h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	retryable := updatedTask.Status == workerruntime.StatusQueued
	out, err := h.store.ApplyRegistrationFailure(r.Context(), user.TenantID, runID, input.ErrorCode, input.ErrorDetail, retryable)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_failed", "page_registration_run", runID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"run": out, "task_status": updatedTask.Status})
}

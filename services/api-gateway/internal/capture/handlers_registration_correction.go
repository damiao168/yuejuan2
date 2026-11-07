package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"net/http"
)

func (h *Handler) CreateRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateRegistrationCorrectionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.CreateRegistrationCorrection(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_created", "page_registration_correction", out.ID, "create manual registration correction")
	httpx.JSON(w, http.StatusCreated, map[string]any{"correction": out})
}

func (h *Handler) GetRegistrationCorrectionContext(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetRegistrationCorrectionContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"context": out})
}

func (h *Handler) GetRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.GetRegistrationCorrection(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"correction": out})
}

func (h *Handler) PreviewRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input struct {
		Revision int `json:"revision"`
	}
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.QueueRegistrationCorrectionPreview(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input.Revision)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_preview_queued", "page_registration_correction", out.ID, "queue manual registration preview")
	httpx.JSON(w, http.StatusAccepted, map[string]any{"correction": out})
}

func (h *Handler) ApplyRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RegistrationCorrectionDecisionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ApplyRegistrationCorrection(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_applied", "page_registration_correction", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"correction": out})
}

func (h *Handler) UndoRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RegistrationCorrectionDecisionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.UndoRegistrationCorrection(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_undone", "page_registration_correction", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"correction": out})
}

func (h *Handler) CompleteRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	correctionID := r.PathValue("id")
	var input CorrectionPreviewResultInput
	if !decodeStrict(w, r, &input) {
		return
	}
	correction, err := h.store.GetRegistrationCorrection(r.Context(), user.TenantID, correctionID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_registration_correction" || task.SourceID != correctionID || correction.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "correction_task_mismatch", "worker task does not belong to this correction")
		return
	}
	baseRun, err := h.store.GetRegistrationRun(r.Context(), user.TenantID, correction.BaseRegistrationRunID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	context, err := h.store.GetRegistrationCorrectionContext(r.Context(), user.TenantID, correction.BaseRegistrationRunID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	source, err := h.files.Get(r.Context(), user.TenantID, baseRun.SourceFileAssetID)
	if err != nil || (source.ExamID != "" && source.ExamID != context.ExamID) {
		httpx.Error(w, r, http.StatusConflict, "correction_source_invalid", "correction source asset is missing")
		return
	}
	registered, err := h.files.Get(r.Context(), user.TenantID, input.PreviewRegisteredFileAssetID)
	if err != nil || registered.ExamID != context.ExamID || registered.OwnerType != "page_registration_correction_preview" || registered.OwnerID != correctionID || registered.HashSHA256 != input.PreviewRegisteredSHA256 {
		httpx.Error(w, r, http.StatusBadRequest, "correction_preview_asset_invalid", "preview asset has an invalid owner or exam")
		return
	}
	for _, segment := range input.Segments {
		asset, assetErr := h.files.Get(r.Context(), user.TenantID, segment.FileAssetID)
		if assetErr != nil || asset.ExamID != context.ExamID || asset.OwnerType != "page_registration_correction_preview" || asset.OwnerID != correctionID || asset.HashSHA256 != segment.SHA256 {
			httpx.Error(w, r, http.StatusBadRequest, "correction_segment_asset_invalid", "preview segment has an invalid owner, hash, or exam")
			return
		}
	}
	if err := h.validateGradingGroupAssets(r.Context(), user.TenantID, context.ExamID, "page_registration_correction_preview", correctionID, task.Payload, input.Segments); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "correction_grading_group_invalid", "shared grading crop does not match the frozen template or current preview")
		return
	}
	coordinator, ok := h.store.(RegistrationResultCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_command_unavailable", "atomic capture completion is unavailable")
		return
	}
	out, err := coordinator.SubmitRegistrationCorrectionResultCommand(r.Context(), user.TenantID, correctionID, input, h.runtime)
	if err != nil {
		writeCaptureCompletionError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_preview_ready", "page_registration_correction", out.ID, "manual correction preview completed")
	httpx.JSON(w, http.StatusOK, map[string]any{"correction": out})
}

func (h *Handler) FailRegistrationCorrection(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	correctionID := r.PathValue("id")
	var input CorrectionPreviewFailureInput
	if !decodeStrict(w, r, &input) {
		return
	}
	correction, err := h.store.GetRegistrationCorrection(r.Context(), user.TenantID, correctionID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "page_registration_correction" || task.SourceID != correctionID || correction.RuntimeTaskID != task.ID {
		httpx.Error(w, r, http.StatusConflict, "correction_task_mismatch", "worker task does not belong to this correction")
		return
	}
	updatedTask, err := h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	out, err := h.store.ApplyRegistrationCorrectionFailure(r.Context(), user.TenantID, correctionID, input.ErrorCode, input.ErrorDetail)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "page.registration_correction_failed", "page_registration_correction", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"correction": out, "task_status": updatedTask.Status})
}

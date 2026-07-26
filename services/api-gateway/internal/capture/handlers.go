package capture

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store   Store
	files   files.Store
	exams   exam.Store
	runtime workerruntime.Store
	audit   auth.Store
}

func NewHandler(store Store, fileStore files.Store, examStore exam.Store, runtime workerruntime.Store, audit auth.Store) *Handler {
	return &Handler{store: store, files: fileStore, exams: examStore, runtime: runtime, audit: audit}
}

func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	examID := r.PathValue("examId")
	current, err := h.exams.GetExam(r.Context(), user.TenantID, examID)
	if err != nil {
		httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
		return
	}
	if current.Status != "collecting" {
		httpx.Error(w, r, http.StatusConflict, "exam_not_collecting", "exam must be collecting before capture batches can be created")
		return
	}
	var input CreateBatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.CreateBatch(r.Context(), user.TenantID, examID, user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_batch.created", "capture_batch", out.ID, "create capture batch")
	httpx.JSON(w, http.StatusCreated, map[string]any{"batch": out})
}

func (h *Handler) IssueTemplateBarcodes(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.IssueTemplateBarcodes(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_template.barcodes_issued", "answer_sheet_template", out.TemplateID, "issue controlled page barcodes")
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}

func (h *Handler) IssueStudentBarcodes(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input IssueStudentBarcodesInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.IssueStudentBarcodes(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "answer_sheet_template.student_barcodes_issued", "answer_sheet_template", out.TemplateID, "issue student-bound sheet barcodes")
	httpx.JSON(w, http.StatusOK, map[string]any{"barcodes": out})
}

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
	out, err := h.store.ApplyRegistrationCorrectionPreview(r.Context(), user.TenantID, correctionID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result := map[string]any{"correction_id": correctionID, "result_version": input.ResultVersion, "preview_registered_file_asset_id": input.PreviewRegisteredFileAssetID, "segments": input.Segments}
	if _, err = h.runtime.Complete(r.Context(), user.TenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-registration-correction-result-v1", Result: result, DurationMS: input.DurationMS}); err != nil {
		writeRuntimeError(w, r, err)
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

func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	items, err := h.store.ListBatches(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batches": items})
}

func (h *Handler) GetBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	batch, err := h.store.GetBatch(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	fileItems, err := h.store.ListFiles(r.Context(), user.TenantID, batch.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	pages, err := h.store.ListPages(r.Context(), user.TenantID, batch.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, BatchDetail{Batch: batch, Files: fileItems, Pages: pages})
}

func (h *Handler) GetMatchingQueue(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	queue, err := h.store.GetMatchingQueue(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, queue)
}

func (h *Handler) ConfirmStudentMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ConfirmStudentMatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmStudentMatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.student_match_confirmed", "submission", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"submission": out})
}

func (h *Handler) MarkStudentUnknown(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input MarkStudentUnknownInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.MarkStudentUnknown(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.student_marked_unknown", "submission", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"submission": out})
}

func (h *Handler) ConfirmPageMatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ConfirmPageMatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.ConfirmPageMatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_match_confirmed", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}

func (h *Handler) DeletePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input PageLifecycleInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.DeletePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_deleted", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}
func (h *Handler) RestorePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input PageLifecycleInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.RestorePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.page_restored", "capture_page", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}
func (h *Handler) SplitSubmission(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input SplitSubmissionInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.SplitSubmission(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.submission_split", "capture_batch", out.BatchID, input.Reason)
	httpx.JSON(w, http.StatusOK, out)
}
func (h *Handler) MergeSubmissions(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input MergeSubmissionsInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.MergeSubmissions(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture.submissions_merged", "capture_batch", out.BatchID, input.Reason)
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) RegisterFile(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input RegisterFileInput
	if !decodeStrict(w, r, &input) {
		return
	}
	asset, err := h.files.Get(r.Context(), user.TenantID, input.FileAssetID)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "capture_file_asset_invalid", "file asset is missing or outside the current tenant")
		return
	}
	snapshot := FileAssetSnapshot{ID: asset.ID, ExamID: asset.ExamID, OriginalName: asset.OriginalName, ContentType: asset.ContentType, SizeBytes: asset.SizeBytes, SHA256: asset.HashSHA256}
	out, err := h.store.RegisterFile(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input, snapshot)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_file.registered", "capture_file", out.ID, "register capture source file")
	httpx.JSON(w, http.StatusCreated, map[string]any{"file": out})
}

func (h *Handler) ProcessBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.QueueBatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_batch.processing_started", "capture_batch", out.ID, "queue capture files for decoding")
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": out})
}
func (h *Handler) ListPages(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.ListPages(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"pages": out})
}
func (h *Handler) UpdatePage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdatePageInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.UpdatePage(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_page.updated", "capture_page", out.ID, "update capture page order or rotation")
	httpx.JSON(w, http.StatusOK, map[string]any{"page": out})
}

func (h *Handler) CompleteFile(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	fileID := r.PathValue("fileId")
	var input FileResultInput
	if !decodeStrict(w, r, &input) {
		return
	}
	item, err := h.store.GetFile(r.Context(), user.TenantID, fileID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	sourceAsset, err := h.files.Get(r.Context(), user.TenantID, item.FileAssetID)
	if err != nil || sourceAsset.ExamID == "" {
		httpx.Error(w, r, http.StatusConflict, "capture_source_asset_invalid", "capture source asset is missing or lacks exam ownership")
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "capture_file" || task.SourceID != fileID {
		httpx.Error(w, r, http.StatusConflict, "capture_task_mismatch", "worker task does not belong to this capture file")
		return
	}
	for _, page := range input.Pages {
		asset, assetErr := h.files.Get(r.Context(), user.TenantID, page.FileAssetID)
		if assetErr != nil || asset.ExamID != sourceAsset.ExamID || asset.HashSHA256 != page.SHA256 {
			httpx.Error(w, r, http.StatusBadRequest, "decoded_page_asset_invalid", "decoded page asset is missing, has a different hash, or lacks exam ownership")
			return
		}
	}
	result := map[string]any{"capture_file_id": fileID, "decoder_profile": input.DecoderProfile, "original_page_count": input.OriginalPageCount, "detected_content_type": input.DetectedContentType, "pages": input.Pages, "result_version": input.ResultVersion}
	out, err := h.store.ApplyFileResult(r.Context(), user.TenantID, item.ID, input.Pages)
	if err != nil {
		writeError(w, r, err)
		return
	}
	_, err = h.runtime.Complete(r.Context(), user.TenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "capture-file-result-v1", Result: result, DurationMS: input.DurationMS})
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_file.decoded", "capture_file", out.ID, "materialize decoded capture pages")
	httpx.JSON(w, http.StatusOK, map[string]any{"file": out})
}

func (h *Handler) FailFile(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	fileID := r.PathValue("fileId")
	var input FileFailureInput
	if !decodeStrict(w, r, &input) {
		return
	}
	task, err := h.runtime.Get(r.Context(), user.TenantID, input.TaskID)
	if err != nil || task.SourceType != "capture_file" || task.SourceID != fileID {
		httpx.Error(w, r, http.StatusConflict, "capture_task_mismatch", "worker task does not belong to this capture file")
		return
	}
	updatedTask, err := h.runtime.Fail(r.Context(), user.TenantID, input.TaskID, workerruntime.FailInput{LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS})
	if err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	retryable := updatedTask.Status == workerruntime.StatusQueued
	out, err := h.store.ApplyFileFailure(r.Context(), user.TenantID, fileID, input.ErrorCode, retryable)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_file.processing_failed", "capture_file", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"file": out, "task_status": updatedTask.Status})
}

func (h *Handler) CancelBatch(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "cancelled") }
func (h *Handler) ReopenBatch(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "draft") }
func (h *Handler) CompleteBatch(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "completed")
}

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
	if _, err = h.runtime.Requeue(r.Context(), user.TenantID, task.ID); err != nil {
		writeRuntimeError(w, r, err)
		return
	}
	out, err := h.store.PrepareRegistrationRetry(r.Context(), user.TenantID, run.ID)
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
	out, err := h.store.ApplyRegistrationResult(r.Context(), user.TenantID, runID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	result := map[string]any{"registration_run_id": runID, "result_version": input.ResultVersion, "registered_file_asset_id": input.RegisteredFileAssetID, "method": input.Method, "confidence": input.Confidence, "segments": input.Segments}
	if _, err = h.runtime.Complete(r.Context(), user.TenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "page-registration-result-v1", Result: result, DurationMS: input.DurationMS}); err != nil {
		writeRuntimeError(w, r, err)
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
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	user := mustUser(r)
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.store.SetBatchStatus(r.Context(), user.TenantID, r.PathValue("id"), user.ID, status, input.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_batch."+status, "capture_batch", out.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": out})
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body is invalid or contains unknown fields")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return false
	}
	return true
}
func mustUser(r *http.Request) auth.User { user, _ := auth.UserFromContext(r.Context()); return user }
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "capture_not_found", "capture resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "capture_invalid_input", "capture input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "capture_invalid_transition", "capture state does not allow this action")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "capture_revision_conflict", "capture resource changed; refresh and retry")
	case errors.Is(err, ErrDuplicateFile):
		httpx.Error(w, r, http.StatusConflict, "capture_duplicate_file", "file is already registered in this batch")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "capture_operation_failed", "capture operation failed")
	}
}
func writeRuntimeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, workerruntime.ErrLeaseExpired), errors.Is(err, workerruntime.ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "capture_worker_lease_invalid", "worker lease expired or no longer owns this task")
	case errors.Is(err, workerruntime.ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "capture_worker_result_conflict", "worker result conflicts with the saved result")
	default:
		httpx.Error(w, r, http.StatusConflict, "capture_worker_result_rejected", "worker result could not be accepted")
	}
}
func (h *Handler) auditAction(r *http.Request, action, targetType, targetID, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: targetType, TargetID: targetID, Reason: strings.TrimSpace(reason), IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

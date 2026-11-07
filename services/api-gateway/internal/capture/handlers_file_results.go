package capture

import (
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"errors"
	"net/http"
)

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
	// 先核对任务归属和每个产物的考试、哈希，再把解码页面写入数据库。
	for _, page := range input.Pages {
		asset, assetErr := h.files.Get(r.Context(), user.TenantID, page.FileAssetID)
		if assetErr != nil || asset.ExamID != sourceAsset.ExamID || asset.HashSHA256 != page.SHA256 {
			httpx.Error(w, r, http.StatusBadRequest, "decoded_page_asset_invalid", "decoded page asset is missing, has a different hash, or lacks exam ownership")
			return
		}
	}
	result := map[string]any{"capture_file_id": fileID, "decoder_profile": input.DecoderProfile, "original_page_count": input.OriginalPageCount, "detected_content_type": input.DetectedContentType, "pages": input.Pages, "result_version": input.ResultVersion}
	coordinator, ok := h.store.(FileResultCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_command_unavailable", "atomic capture completion is unavailable")
		return
	}
	// 领域页面和租约终态同一次提交；已成功回执只允许同指纹重放，不按页数接受不同产物。
	out, err := coordinator.SubmitFileResultCommand(r.Context(), user.TenantID, item.ID, input, h.runtime, result)
	if err != nil {
		if errors.Is(err, workerruntime.ErrLeaseExpired) || errors.Is(err, workerruntime.ErrLeaseMismatch) || errors.Is(err, workerruntime.ErrConflict) || errors.Is(err, workerruntime.ErrInvalidTransition) {
			writeRuntimeError(w, r, err)
		} else {
			writeError(w, r, err)
		}
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
	coordinator, ok := h.store.(FileFailureCommandStore)
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "capture_command_unavailable", "atomic capture failure is unavailable")
		return
	}
	// 失败租约和采集投影一起落定；领域写入失败时保留原租约供同一回调重试。
	out, taskStatus, err := coordinator.SubmitFileFailureCommand(r.Context(), user.TenantID, fileID, input, h.runtime)
	if err != nil {
		writeCaptureCompletionError(w, r, err)
		return
	}
	h.auditAction(r, "capture_file.processing_failed", "capture_file", out.ID, input.ErrorCode)
	httpx.JSON(w, http.StatusOK, map[string]any{"file": out, "task_status": taskStatus})
}

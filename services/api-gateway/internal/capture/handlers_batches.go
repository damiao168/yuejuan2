package capture

import (
	"context"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	examID := r.PathValue("examId")
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	current, err := h.exams.GetExam(r.Context(), scope, examID)
	if err != nil {
		httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
		return
	}
	var input CreateBatchInput
	if !decodeStrict(w, r, &input) {
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	// 非采集阶段只允许恢复已成功接受的同键命令，不能借恢复接口新建批次。
	if current.Status != "collecting" && !(input.Purpose == "supplement" && (current.Status == "grading" || current.Status == "reviewing")) {
		recovery, recoveryErr := h.store.RecoverBatchCommand(r.Context(), user.TenantID, examID, user.ID, input.IdempotencyKey)
		if recoveryErr != nil || recovery.Status != "succeeded" {
			httpx.Error(w, r, http.StatusConflict, "exam_not_collecting", "exam must be collecting before capture batches can be created")
			return
		}
	}
	out, err := h.store.CreateBatch(r.Context(), user.TenantID, examID, user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, "capture_batch.created", "capture_batch", out.ID, "create capture batch")
	httpx.JSON(w, http.StatusCreated, map[string]any{"batch": out})
}

func (h *Handler) RecoverBatchCommand(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	commandID := strings.TrimSpace(r.PathValue("commandId"))
	if commandID == "" {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_capture_command", "command ID is required")
		return
	}
	result, err := h.store.RecoverBatchCommand(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, commandID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"command": result})
}

func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 30, 100)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	items, err := h.store.ListBatches(r.Context(), user.TenantID, r.PathValue("examId"), BatchListFilter{
		Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextCursor := ""
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batches": items, "next_cursor": nextCursor, "has_more": hasMore})
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
	summaries, err := h.store.ListProcessingSummaries(r.Context(), user.TenantID, batch.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, BatchDetail{Batch: batch, Files: fileItems, Pages: pages, ProcessingSummaries: summaries})
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

func (h *Handler) CancelBatch(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "cancelled") }

func (h *Handler) ReopenBatch(w http.ResponseWriter, r *http.Request) { h.setStatus(w, r, "draft") }

func (h *Handler) CompleteBatch(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "completed")
}

func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request, status string) {
	user := mustUser(r)
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodeStrict(w, r, &input) {
		return
	}
	var out Batch
	var err error
	changed := true
	if status == "cancelled" {
		if cancelStore, ok := h.store.(interface {
			cancelBatch(context.Context, string, string, string, string) (Batch, bool, error)
		}); ok {
			out, changed, err = cancelStore.cancelBatch(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input.Reason)
		} else {
			out, err = h.store.SetBatchStatus(r.Context(), user.TenantID, r.PathValue("id"), user.ID, status, input.Reason)
		}
	} else {
		out, err = h.store.SetBatchStatus(r.Context(), user.TenantID, r.PathValue("id"), user.ID, status, input.Reason)
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	if changed {
		h.auditAction(r, "capture_batch."+status, "capture_batch", out.ID, input.Reason)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"batch": out})
}

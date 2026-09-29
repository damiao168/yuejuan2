package ocr

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	submissionpkg "edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	store       Store
	queue       Queue
	submissions submissionpkg.Store
	audit       auth.Store
	runtime     workerruntime.Store
	atomic      atomicRuntimeCoordinator
}

func NewHandler(store Store, queue Queue, submissions submissionpkg.Store, audit auth.Store, runtimes ...workerruntime.Store) *Handler {
	handler := &Handler{store: store, queue: queue, submissions: submissions, audit: audit}
	if len(runtimes) > 0 {
		handler.runtime = runtimes[0]
		handler.atomic = newAtomicRuntimeCoordinator(store, runtimes[0])
	}
	return handler
}

// 只有 ready_for_ocr 的答题卡允许建任务；幂等键同时保护接口重试和运行时任务，避免重复排队。
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	submissionID := r.PathValue("id")
	sub, err := h.submissions.Get(r.Context(), user.TenantID, submissionID)
	if err != nil {
		writeStoreError(w, r, ErrSubmissionNotReady)
		return
	}
	if sub.Status != "ready_for_ocr" {
		writeStoreError(w, r, ErrSubmissionNotReady)
		return
	}
	var input CreateTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if headerKey != "" && strings.TrimSpace(input.IdempotencyKey) != "" && headerKey != strings.TrimSpace(input.IdempotencyKey) {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	if headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	pages, err := h.submissions.ListPages(r.Context(), user.TenantID, submissionID)
	if err != nil || len(pages) == 0 {
		writeStoreError(w, r, ErrSubmissionNotReady)
		return
	}
	input.SourceFileAssetIDs = make([]string, 0, len(pages))
	for _, page := range pages {
		if fileAssetID := strings.TrimSpace(page.FileAssetID); fileAssetID != "" {
			input.SourceFileAssetIDs = append(input.SourceFileAssetIDs, fileAssetID)
		}
	}
	var task Task
	if h.runtime != nil {
		if h.atomic == nil {
			writeStoreError(w, r, errAtomicRuntimeUnavailable)
			return
		}
		task, err = h.atomic.CreateTask(r.Context(), user.TenantID, submissionID, user.ID, input)
	} else {
		task, err = h.store.CreateTask(r.Context(), user.TenantID, submissionID, user.ID, input)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if h.runtime == nil {
		if err = h.queue.Enqueue(r.Context(), task); err != nil {
			httpx.Error(w, r, http.StatusInternalServerError, "ocr_enqueue_failed", "failed to enqueue ocr task")
			return
		}
	}
	h.auditAction(r, "ocr.task_created", "ocr_task", task.ID, "create ocr task")
	httpx.JSON(w, http.StatusCreated, map[string]any{"task": task})
}

func (h *Handler) ListBySubmission(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 20, 100)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100")
		return
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return
	}
	out, err := h.store.ListBySubmission(r.Context(), user.TenantID, r.PathValue("id"), TaskListFilter{
		Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	nextCursor := ""
	if hasMore && len(out) > 0 {
		last := out[len(out)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": out, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) ListPending(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), 10, 100)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 100")
		return
	}
	out, err := h.store.ListPending(r.Context(), user.TenantID, limit)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

// OCR 输入只返回租户内答题页和必要图片地址；需要整页的区域不下发局部裁剪，避免坐标语义错误。
func (h *Handler) GetTaskInput(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	pages, err := h.submissions.ListPages(r.Context(), user.TenantID, task.SubmissionID)
	if err != nil {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	type pageInput struct {
		ID          string `json:"id"`
		FileAssetID string `json:"file_asset_id"`
		PageNo      int    `json:"page_no"`
		Status      string `json:"status"`
		DownloadURL string `json:"download_url"`
		Regions     any    `json:"regions,omitempty"`
	}
	regionsByPage := map[string][]submissionpkg.AnswerRegion{}
	fullPageRequired := map[string]bool{}
	if lister, ok := h.submissions.(submissionpkg.AnswerRegionLister); ok {
		regions, regionErr := lister.ListAnswerRegions(r.Context(), user.TenantID, task.SubmissionID)
		if regionErr != nil {
			writeStoreError(w, r, ErrInvalidInput)
			return
		}
		for _, region := range regions {
			if region.RequiresFullPage {
				fullPageRequired[region.PageID] = true
			}
			regionsByPage[region.PageID] = append(regionsByPage[region.PageID], region)
		}
	}
	pageInputs := make([]pageInput, 0, len(pages))
	for _, page := range pages {
		pageInputs = append(pageInputs, pageInput{
			ID:          page.ID,
			FileAssetID: page.FileAssetID,
			PageNo:      page.PageNo,
			Status:      page.Status,
			DownloadURL: "/api/v1/files/" + page.FileAssetID + "/download",
			Regions: func() any {
				if fullPageRequired[page.ID] {
					return nil
				}
				if regions := regionsByPage[page.ID]; len(regions) > 0 {
					return regions
				}
				return nil
			}(),
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"task": map[string]any{
			"id":             task.ID,
			"tenant_id":      task.TenantID,
			"submission_id":  task.SubmissionID,
			"engine":         task.Engine,
			"engine_version": task.EngineVersion,
			"min_confidence": task.MinConfidence,
		},
		"pages": pageInputs,
	})
}

func (h *Handler) StartTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	task, err := h.store.StartTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "ocr.task_started", "ocr_task", task.ID, "start ocr task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

// 完成前校验每个结果属于当前答题卡；配置 Runtime 时源任务和 Runtime 任务必须一起成功。
func (h *Handler) CompleteTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CompleteTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	task, err := h.store.GetTask(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	pages, err := h.submissions.ListPages(r.Context(), user.TenantID, task.SubmissionID)
	if err != nil {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	allowedPages := map[string]bool{}
	for _, page := range pages {
		allowedPages[page.ID] = true
	}
	for _, result := range input.Results {
		if !allowedPages[strings.TrimSpace(result.SubmissionPageID)] {
			writeStoreError(w, r, ErrInvalidInput)
			return
		}
	}
	var out Task
	if h.runtime != nil {
		if h.atomic == nil {
			writeStoreError(w, r, errAtomicRuntimeUnavailable)
			return
		}
		out, err = h.atomic.CompleteTask(r.Context(), user.TenantID, task.ID, input)
	} else {
		out, err = h.store.CompleteTask(r.Context(), user.TenantID, task.ID, input)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "ocr.task_completed", "ocr_task", out.ID, "complete ocr task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": out})
}

func sameCompletion(task Task, input CompleteTaskInput) bool {
	if task.ModelVersion != input.ModelVersion || task.ConfigHash != input.ConfigHash || task.InputHash != input.InputHash ||
		task.DurationMS != input.DurationMS || task.WorkerID != input.WorkerID || len(task.Results) != len(input.Results) {
		return false
	}
	for index, current := range task.Results {
		candidate := input.Results[index]
		if current.SubmissionPageID != candidate.SubmissionPageID || current.Text != candidate.Text ||
			current.Confidence != candidate.Confidence || current.SourceImageFileID != candidate.SourceImageFileID ||
			current.PreprocessProfile != input.PreprocessProfile || len(current.BBox) != len(candidate.BBox) {
			return false
		}
		for bboxIndex := range current.BBox {
			if current.BBox[bboxIndex] != candidate.BBox[bboxIndex] {
				return false
			}
		}
	}
	return true
}

func (h *Handler) FailTask(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input FailTaskInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ErrorMessage = strings.TrimSpace(input.ErrorMessage)
	if input.ErrorMessage == "" {
		writeStoreError(w, r, ErrInvalidInput)
		return
	}
	var task Task
	var err error
	if h.runtime != nil {
		if h.atomic == nil {
			writeStoreError(w, r, errAtomicRuntimeUnavailable)
			return
		}
		task, err = h.atomic.FailTask(r.Context(), user.TenantID, r.PathValue("id"), input)
	} else {
		task, err = h.store.FailTask(r.Context(), user.TenantID, r.PathValue("id"), input.ErrorMessage)
	}
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "ocr.task_failed", "ocr_task", task.ID, "fail ocr task")
	httpx.JSON(w, http.StatusOK, map[string]any{"task": task})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "ocr_task_not_found", "ocr task not found")
	case errors.Is(err, ErrSubmissionNotReady):
		httpx.Error(w, r, http.StatusConflict, "submission_not_ready_for_ocr", "submission must be ready_for_ocr before creating an ocr task")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_ocr_input", "ocr input is invalid")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_ocr_status_transition", "ocr task status transition is not allowed")
	case errors.Is(err, ErrResultConflict):
		httpx.Error(w, r, http.StatusConflict, "ocr_result_conflict", "ocr result conflicts with the completed task")
	case errors.Is(err, ErrIdempotencyConflict):
		httpx.Error(w, r, http.StatusConflict, "ocr_idempotency_conflict", "ocr idempotency key was already used with a different request")
	case errors.Is(err, errAtomicRuntimeUnavailable):
		httpx.Error(w, r, http.StatusInternalServerError, "ocr_runtime_atomicity_unavailable", "ocr runtime coordination is unavailable")
	case errors.Is(err, workerruntime.ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "worker_task_not_found", "worker task not found")
	case errors.Is(err, workerruntime.ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_worker_task_input", "worker task input is invalid")
	case errors.Is(err, workerruntime.ErrConflict), errors.Is(err, workerruntime.ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "worker_task_conflict", "worker task operation conflicts with current state")
	case errors.Is(err, workerruntime.ErrLeaseExpired):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_expired", "worker task lease expired")
	case errors.Is(err, workerruntime.ErrLeaseMismatch):
		httpx.Error(w, r, http.StatusConflict, "worker_task_lease_mismatch", "worker task lease mismatch")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "ocr_operation_failed", "ocr operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}

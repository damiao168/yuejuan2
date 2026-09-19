package backmark

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type Handler struct {
	service      *Service
	audit        auth.Store
	segmentImage segment.CropImageReader
	regrade      *regrade.Service
}

func NewHandler(service *Service, audit auth.Store) *Handler {
	return &Handler{service: service, audit: audit}
}

func (h *Handler) WithSegmentImage(reader segment.CropImageReader) *Handler {
	h.segmentImage = reader
	return h
}

// WithRegradeService links the completed, manager-reviewed backmark
// population to A19.  The link is deliberately opt-in so a backmark handler
// never gains any ability to publish or update a current score.
func (h *Handler) WithRegradeService(service *regrade.Service) *Handler {
	h.regrade = service
	return h
}

// RegisterRoutes keeps manager impact selection separate from a grader's
// assigned-item actions. The submit endpoint writes a backmark_grade only; it
// is intentionally not the normal review submit endpoint.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireManage, requireWork func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/backmark-preview", requireManage(h.Preview))
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/backmark-batches", requireManage(h.Create))
	mux.Handle("GET /api/v1/backmark-batches", requireManage(h.List))
	mux.Handle("GET /api/v1/backmark-batches/{batchId}", requireManage(h.Get))
	mux.Handle("POST /api/v1/backmark-batches/{batchId}/regrade-preview", requireManage(h.PreviewRegrade))
	mux.Handle("POST /api/v1/backmark-batches/{batchId}/regrade-jobs", requireManage(h.CreateRegrade))
	mux.Handle("GET /api/v1/backmark-items/mine", requireWork(h.ListMine))
	mux.Handle("POST /api/v1/backmark-items/{itemId}/claim", requireWork(h.Claim))
	mux.Handle("GET /api/v1/backmark-items/{itemId}/context", requireWork(h.GetContext))
	mux.Handle("GET /api/v1/backmark-items/{itemId}/segment-image", requireWork(h.GetSegmentImage))
	mux.Handle("POST /api/v1/backmark-items/{itemId}/submit", requireWork(h.Submit))
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Selector Selector `json:"selector"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	preview, err := h.service.Preview(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), input.Selector)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"preview": preview})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input CreateInput
	if !decodeBody(w, r, &input) {
		return
	}
	summary, err := h.service.Create(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "backmark.batch_created", summary.Batch.ID, map[string]any{
		"incident_id": summary.Batch.SourceIncidentID, "affected_count": summary.Batch.AffectedCount,
		"disposition": summary.Batch.Policy.Disposition,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"backmark": summary, "next_cursor": "", "has_more": false})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	page, limit, ok := backmarkPage(w, r)
	if !ok {
		return
	}
	batches, err := h.service.ListPage(r.Context(), user.TenantID, strings.TrimSpace(r.URL.Query().Get("exam_id")), strings.TrimSpace(r.URL.Query().Get("question_id")), page)
	if err != nil {
		writeError(w, r, err)
		return
	}
	hasMore := len(batches) > limit
	if hasMore {
		batches = batches[:limit]
	}
	nextCursor := ""
	if hasMore && len(batches) > 0 {
		last := batches[len(batches)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backmark_batches": batches, "next_cursor": nextCursor, "has_more": hasMore})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	page, limit, ok := backmarkPage(w, r)
	if !ok {
		return
	}
	page.Status = strings.TrimSpace(r.URL.Query().Get("status"))
	summary, err := h.service.GetPage(r.Context(), user.TenantID, r.PathValue("batchId"), page)
	if err != nil {
		writeError(w, r, err)
		return
	}
	hasMore := len(summary.Items) > limit
	if hasMore {
		summary.Items = summary.Items[:limit]
	}
	nextCursor := ""
	if hasMore && len(summary.Items) > 0 {
		last := summary.Items[len(summary.Items)-1]
		nextCursor = pagination.Encode(last.CreatedAt, last.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backmark": summary, "next_cursor": nextCursor, "has_more": hasMore})
}

type backmarkRegradeInput struct {
	SourceReleaseID string `json:"source_release_id"`
	AssigneeID      string `json:"assignee_id"`
}

func (h *Handler) PreviewRegrade(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	input, batch, selector, ok := h.backmarkRegradeInput(w, r, user)
	if !ok {
		return
	}
	preview, err := h.regrade.Preview(r.Context(), user.TenantID, batch.ExamID, batch.QuestionID, input.SourceReleaseID, selector)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"preview": preview})
}

func (h *Handler) CreateRegrade(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	input, batch, selector, ok := h.backmarkRegradeInput(w, r, user)
	if !ok {
		return
	}
	// Keep the idempotency key stable across a retry or double-click while
	// allowing a later correction from a different immutable source release.
	key := "backmark:" + batch.ID + ":" + input.SourceReleaseID
	summary, err := h.regrade.Create(r.Context(), user.TenantID, batch.ExamID, batch.QuestionID, user.ID, regrade.CreateInput{
		SourceReleaseID: input.SourceReleaseID,
		ReasonCode:      regrade.ReasonQualityIssue,
		ReasonText:      "backmark batch " + batch.ID + " from quality incident " + batch.SourceIncidentID,
		Strategy:        regrade.StrategyBackmarkImport,
		Selector:        selector,
		IdempotencyKey:  key,
		AssigneeID:      input.AssigneeID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "backmark.regrade_created", batch.ID, map[string]any{
		"regrade_job_id": summary.Job.ID, "source_release_id": input.SourceReleaseID,
		"affected_count": summary.Job.AffectedCount,
	})
	// A19 returns an awaiting-approval job.  No backmark candidate is copied to
	// final_grade and no score release is published by this endpoint.
	httpx.JSON(w, http.StatusCreated, map[string]any{"regrade": summary})
}

func (h *Handler) backmarkRegradeInput(w http.ResponseWriter, r *http.Request, user auth.User) (backmarkRegradeInput, Batch, regrade.Selector, bool) {
	if h.regrade == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "backmark_regrade_unavailable", "backmark regrade workflow is unavailable")
		return backmarkRegradeInput{}, Batch{}, regrade.Selector{}, false
	}
	var input backmarkRegradeInput
	if !decodeBody(w, r, &input) {
		return backmarkRegradeInput{}, Batch{}, regrade.Selector{}, false
	}
	input.SourceReleaseID, input.AssigneeID = strings.TrimSpace(input.SourceReleaseID), strings.TrimSpace(input.AssigneeID)
	if input.SourceReleaseID == "" || input.AssigneeID == "" {
		writeError(w, r, ErrInvalidInput)
		return backmarkRegradeInput{}, Batch{}, regrade.Selector{}, false
	}
	batch, submissionIDs, err := h.service.RegradeSelection(r.Context(), user.TenantID, r.PathValue("batchId"))
	if err != nil {
		writeError(w, r, err)
		return backmarkRegradeInput{}, Batch{}, regrade.Selector{}, false
	}
	return input, batch, regrade.Selector{SubmissionIDs: submissionIDs}, true
}

func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	page, limit, ok := backmarkPage(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListAssignedPage(r.Context(), user.TenantID, user.ID, page)
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
	httpx.JSON(w, http.StatusOK, map[string]any{"backmark_items": items, "next_cursor": nextCursor, "has_more": hasMore})
}

func backmarkPage(w http.ResponseWriter, r *http.Request) (PageOptions, int, bool) {
	limit, err := pagination.Limit(r.URL.Query().Get("limit"), DefaultPageSize, MaxPageSize)
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "limit must be between 1 and 200")
		return PageOptions{}, 0, false
	}
	cursor, err := pagination.Decode(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_pagination", "cursor is invalid")
		return PageOptions{}, 0, false
	}
	return PageOptions{Limit: limit + 1, CursorCreatedAt: cursor.CreatedAt, CursorID: cursor.ID}, limit, true
}

func (h *Handler) Claim(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Claim(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "backmark.item_claimed", item.ID, map[string]any{"batch_id": item.BatchID})
	httpx.JSON(w, http.StatusOK, map[string]any{"backmark_item": graderView(item)})
}

func (h *Handler) GetContext(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	contextValue, err := h.service.GetGraderContext(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"backmark_context": contextValue})
}

func (h *Handler) GetSegmentImage(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	if h.segmentImage == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "backmark_image_unavailable", "backmark image handler is unavailable")
		return
	}
	segmentID, err := h.service.GetSegmentID(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resource, err := h.segmentImage.ReadCropImage(r.Context(), user.TenantID, segmentID)
	if err != nil {
		segment.WriteImageError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input SubmitInput
	if !decodeBody(w, r, &input) {
		return
	}
	item, grade, err := h.service.Submit(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "backmark.diff_recorded", item.ID, map[string]any{
		"batch_id": item.BatchID, "new_grade_id": grade.ID, "status": item.Status,
		"diff": item.Diff,
	})
	// This response deliberately does not include a final/current grade. The
	// client must route the resulting status to confirmation/arbitration/A19.
	httpx.JSON(w, http.StatusCreated, map[string]any{"backmark_item": graderView(item), "backmark_grade": grade})
}

func currentUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must be valid JSON with known fields")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object")
		return false
	}
	return true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "backmark_not_found", "backmark resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_backmark_input", "backmark input is invalid")
	case errors.Is(err, ErrNoAffectedTasks):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "backmark_no_affected_tasks", "selector matched no completed review tasks")
	case errors.Is(err, ErrNoRegradeItems):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "backmark_no_regrade_items", "backmark batch has no completed items requiring regrade")
	case errors.Is(err, ErrTooManyItems):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "backmark_async_required", "affected population exceeds the synchronous backmark limit")
	case errors.Is(err, ErrOriginalGrader), errors.Is(err, ErrAssigneeForbidden):
		httpx.Error(w, r, http.StatusForbidden, "backmark_assignee_forbidden", "backmark must be completed by its assigned non-original grader")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "backmark_revision_conflict", "backmark item changed; reload before submitting")
	case errors.Is(err, ErrPreviewStale):
		httpx.Error(w, r, http.StatusConflict, "backmark_preview_stale", "backmark selection or source facts changed; preview again before creating")
	case errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "backmark_state_conflict", "backmark item is not ready for this action")
	case errors.Is(err, regrade.ErrSourceRelease):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "backmark_regrade_source_release_invalid", "source release must be a published release for this batch exam")
	case errors.Is(err, regrade.ErrNoAffectedItems):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "backmark_regrade_no_matching_release_items", "selected release has no matching submissions for the completed backmark batch")
	case errors.Is(err, regrade.ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "backmark_regrade_state_conflict", "backmark batch cannot enter regrade in its current state")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "backmark_operation_failed", "backmark operation failed")
	}
}

func (h *Handler) record(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "backmark", TargetID: targetID,
		AfterValue: after, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}

package regrade

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type Handler struct {
	service      *Service
	audit        auth.Store
	segmentImage segment.CropImageReader
}

func NewHandler(service *Service, audit auth.Store) *Handler {
	return &Handler{service: service, audit: audit}
}

func (h *Handler) WithSegmentImage(reader segment.CropImageReader) *Handler {
	h.segmentImage = reader
	return h
}

// RegisterRoutes deliberately keeps creation/finalization under a management
// permission, while a qualified reviewer may only claim and propose an item.
// A future server integration should pass the same policy middleware used by
// A18 and review work; no route can publish a score release.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireManage, requireWork func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/regrade-preview", requireManage(h.Preview))
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/regrade-jobs", requireManage(h.Create))
	mux.Handle("GET /api/v1/regrade-jobs", requireManage(h.List))
	mux.Handle("GET /api/v1/regrade-jobs/{jobId}", requireManage(h.Get))
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/approve", requireManage(h.Approve))
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/start", requireManage(h.Start))
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/pause", requireManage(h.Pause))
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/resume", requireManage(h.Resume))
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/finalize", requireManage(h.Finalize))
	mux.Handle("POST /api/v1/regrade-items/{itemId}/claim", requireWork(h.Claim))
	mux.Handle("GET /api/v1/regrade-items/mine", requireWork(h.ListMine))
	mux.Handle("GET /api/v1/regrade-items/{itemId}/context", requireWork(h.GetContext))
	mux.Handle("GET /api/v1/regrade-items/{itemId}/segment-image", requireWork(h.GetSegmentImage))
	mux.Handle("POST /api/v1/regrade-items/{itemId}/candidate", requireWork(h.RecordCandidate))
	mux.Handle("POST /api/v1/regrade-items/{itemId}/review", requireManage(h.Review))
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input struct {
		SourceReleaseID string   `json:"source_release_id"`
		Selector        Selector `json:"selector"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	preview, err := h.service.Preview(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), input.SourceReleaseID, input.Selector)
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
	h.record(r, user, "regrade.job_created", summary.Job.ID, map[string]any{"question_id": summary.Job.QuestionID, "source_release_id": summary.Job.SourceReleaseID, "affected_count": summary.Job.AffectedCount, "reason_code": summary.Job.ReasonCode})
	httpx.JSON(w, http.StatusCreated, map[string]any{"regrade": summary})
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	jobs, err := h.service.List(r.Context(), user.TenantID, strings.TrimSpace(r.URL.Query().Get("exam_id")), strings.TrimSpace(r.URL.Query().Get("question_id")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_jobs": jobs})
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	summary, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("jobId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade": summary})
}
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	h.jobTransition(w, r, "regrade.job_approved", func(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
		return h.service.Approve(ctx, tenantID, jobID, actorID)
	})
}
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	h.jobTransition(w, r, "regrade.job_started", func(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
		return h.service.Start(ctx, tenantID, jobID, actorID)
	})
}
func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.jobTransition(w, r, "regrade.job_paused", func(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
		return h.service.Pause(ctx, tenantID, jobID, actorID)
	})
}
func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	h.jobTransition(w, r, "regrade.job_resumed", func(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
		return h.service.Resume(ctx, tenantID, jobID, actorID)
	})
}
func (h *Handler) Finalize(w http.ResponseWriter, r *http.Request) {
	h.jobTransition(w, r, "regrade.job_ready_for_release", func(ctx context.Context, tenantID, jobID, actorID string) (Job, error) {
		return h.service.Finalize(ctx, tenantID, jobID, actorID)
	})
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
	h.record(r, user, "regrade.item_claimed", item.ID, map[string]any{"job_id": item.JobID})
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_item": workItem(item)})
}
func (h *Handler) ListMine(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListAssigned(r.Context(), user.TenantID, user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// Service.ListAssigned already maps the store's Item rows to WorkItem, so
	// the response cannot disclose a source release's old score or grade ID.
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_items": items})
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
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_context": contextValue})
}
func (h *Handler) GetSegmentImage(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	if h.segmentImage == nil {
		httpx.Error(w, r, http.StatusInternalServerError, "regrade_image_unavailable", "regrade image handler is unavailable")
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
func (h *Handler) RecordCandidate(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input CandidateInput
	if !decodeBody(w, r, &input) {
		return
	}
	item, err := h.service.RecordCandidate(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "regrade.candidate_recorded", item.ID, map[string]any{"job_id": item.JobID, "status": item.Status})
	httpx.JSON(w, http.StatusCreated, map[string]any{"regrade_item": workItem(item)})
}
func (h *Handler) Review(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input ReviewInput
	if !decodeBody(w, r, &input) {
		return
	}
	item, err := h.service.Review(r.Context(), user.TenantID, r.PathValue("itemId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, "regrade.item_reviewed", item.ID, map[string]any{"job_id": item.JobID, "status": item.Status, "decision": input.Decision})
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_item": item})
}

// jobTransition keeps response and strong-audit handling identical for each
// lifecycle change without allowing a generic unvalidated status endpoint.
func (h *Handler) jobTransition(w http.ResponseWriter, r *http.Request, event string, transition func(context.Context, string, string, string) (Job, error)) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	job, err := transition(r.Context(), user.TenantID, r.PathValue("jobId"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.record(r, user, event, job.ID, map[string]any{"status": job.Status})
	httpx.JSON(w, http.StatusOK, map[string]any{"regrade_job": job})
}

func (h *Handler) record(r *http.Request, user auth.User, action, target string, detail map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "regrade", TargetID: target,
		AfterValue: detail, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
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
		httpx.Error(w, r, http.StatusNotFound, "regrade_not_found", "regrade resource was not found")
	case errors.Is(err, ErrSourceRelease):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "regrade_source_release_invalid", "source release must be a published release for this exam")
	case errors.Is(err, ErrNoAffectedItems):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "regrade_no_affected_items", "selector matched no frozen source-release question facts")
	case errors.Is(err, ErrAssignmentForbidden):
		httpx.Error(w, r, http.StatusForbidden, "regrade_assignment_forbidden", "regrade item is assigned to another reviewer")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "regrade_revision_conflict", "regrade item changed; refresh before retrying")
	case errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "regrade_state_conflict", "regrade operation is not valid in its current state")
	default:
		httpx.Error(w, r, http.StatusBadRequest, "invalid_regrade_input", "regrade input is invalid")
	}
}

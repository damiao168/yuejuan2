package processing

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type Handler struct {
	service *Service
	audit   auth.AuditRecorder
}

func NewHandler(service *Service, audit auth.AuditRecorder) *Handler {
	return &Handler{service: service, audit: audit}
}

// Read access is deliberately separate from operational mutation. A school
// manager can see blockers, while retry/assignment/resolution remain under
// the existing capture/OCR management permission selected at composition.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireExamRead, requireExceptionRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/processing/summary", requireExamRead(h.Summary))
	mux.Handle("GET /api/v1/processing/exceptions", requireExceptionRead(h.ListExceptions))
	mux.Handle("POST /api/v1/processing/exceptions/{id}/retry", requireManage(h.Retry))
	mux.Handle("POST /api/v1/processing/exceptions/{id}/assign", requireManage(h.Assign))
	mux.Handle("POST /api/v1/processing/exceptions/{id}/resolve", requireManage(h.Resolve))
}

func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	out, err := h.service.Summary(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"summary": out})
}

func (h *Handler) ListExceptions(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	filter, err := exceptionFilter(r)
	if err != nil {
		writeError(w, r, ErrInvalidInput)
		return
	}
	out, err := h.service.ListExceptions(r.Context(), user.TenantID, filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, out)
}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	task, err := h.service.Retry(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "processing.exception_retry_queued", r.PathValue("id"), map[string]any{"worker_task_id": task.ID, "source_type": task.SourceType, "source_id": task.SourceID})
	httpx.JSON(w, http.StatusAccepted, map[string]any{"task": task})
}

func (h *Handler) Assign(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	var input AssignInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.service.Assign(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "processing.exception_assigned", out.ID, map[string]any{"assignee_id": out.AssignedTo})
	httpx.JSON(w, http.StatusOK, map[string]any{"exception": out})
}

func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	user, ok := processingUser(w, r)
	if !ok {
		return
	}
	var input ResolveInput
	if !decodeStrict(w, r, &input) {
		return
	}
	out, err := h.service.Resolve(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "processing.exception_resolved", out.ID, map[string]any{"resolution": out.Resolution})
	httpx.JSON(w, http.StatusOK, map[string]any{"exception": out})
}

func exceptionFilter(r *http.Request) (ExceptionFilter, error) {
	query := r.URL.Query()
	filter := ExceptionFilter{ExamID: strings.TrimSpace(query.Get("exam_id")), Subject: strings.TrimSpace(query.Get("subject"))}
	if value := strings.TrimSpace(query.Get("severity")); value != "" {
		filter.Severity = Severity(value)
	}
	if value := strings.TrimSpace(query.Get("stage")); value != "" {
		filter.Stage = Stage(value)
	}
	if value := strings.TrimSpace(query.Get("status")); value != "" {
		filter.Status = ExceptionStatus(value)
	}
	if value := strings.TrimSpace(query.Get("limit")); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return ExceptionFilter{}, err
		}
		filter.Limit = limit
	}
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		parts := strings.Split(raw, "|")
		if len(parts) != 2 || parts[1] == "" {
			return ExceptionFilter{}, ErrInvalidInput
		}
		at, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			return ExceptionFilter{}, err
		}
		filter.CursorAt, filter.CursorID = at.UTC(), parts[1]
	}
	return filter, nil
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_processing_request", "request body must be valid JSON with known fields")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_processing_request", "request body must contain one JSON object")
		return false
	}
	return true
}

func processingUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_processing_input", "processing request is invalid")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "processing_resource_not_found", "processing resource was not found")
	case errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "processing_state_conflict", "processing exception is already resolved or changed")
	case errors.Is(err, ErrRetryForbidden), errors.Is(err, workerruntime.ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "processing_retry_unavailable", "this exception has no failed worker task eligible for retry")
	case errors.Is(err, workerruntime.ErrNotFound):
		httpx.Error(w, r, http.StatusConflict, "processing_retry_source_missing", "the source worker task is unavailable")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "processing_operation_failed", "processing operation could not be completed")
	}
}

func (h *Handler) auditAction(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "operational_exception", TargetID: targetID, Reason: action, AfterValue: after, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

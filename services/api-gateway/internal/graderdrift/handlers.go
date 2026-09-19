package graderdrift

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	service *Service
	audit   auth.AuditRecorder
}

func NewHandler(service *Service, audit auth.AuditRecorder) *Handler {
	return &Handler{service: service, audit: audit}
}

// These are manager quality routes. They deliberately return only aggregate
// metrics and incident evidence, never an individual Seed/Gold answer.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/grader-quality-windows", requireRead(h.ListWindows))
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/grader-drift/recompute", requireManage(h.Recompute))
	mux.Handle("GET /api/v1/grading-quality-incidents", requireRead(h.ListIncidents))
	mux.Handle("POST /api/v1/grading-quality-incidents/{id}/resolve", requireManage(h.ResolveIncident))
}

func (h *Handler) Recompute(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var body struct {
		GraderID string `json:"grader_id"`
	}
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	result, err := h.service.Recompute(r.Context(), user.TenantID, RefreshInput{
		ExamID: r.PathValue("examId"), QuestionID: r.PathValue("questionId"), GraderID: strings.TrimSpace(body.GraderID),
	})
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_drift.recomputed", r.PathValue("questionId"))
	httpx.JSON(w, http.StatusOK, result)
}

func (h *Handler) ListWindows(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListWindows(r.Context(), user.TenantID, WindowFilter{
		ExamID: r.PathValue("examId"), QuestionID: r.PathValue("questionId"),
		GraderID: strings.TrimSpace(r.URL.Query().Get("grader_id")), Limit: parseLimit(r.URL.Query().Get("limit")),
	})
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"windows": items})
}

func (h *Handler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	status := IncidentStatus(strings.TrimSpace(r.URL.Query().Get("status")))
	if status != "" && status != IncidentOpen && status != IncidentAcknowledged && status != IncidentResolved {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_grader_drift_input", "incident status is invalid")
		return
	}
	items, err := h.service.ListIncidents(r.Context(), user.TenantID, IncidentFilter{
		ExamID: strings.TrimSpace(r.URL.Query().Get("exam_id")), QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")),
		GraderID: strings.TrimSpace(r.URL.Query().Get("grader_id")), Status: status, Limit: parseLimit(r.URL.Query().Get("limit")),
	})
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"incidents": items})
}

func (h *Handler) ResolveIncident(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	incident, err := h.service.ResolveIncident(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_drift.incident_resolved", incident.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"incident": incident})
}

func currentUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func decodeOptionalBody(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
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

func parseLimit(value string) int {
	var limit int
	_, _ = fmt.Sscanf(value, "%d", &limit)
	return limit
}

func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "grader_drift_not_found", "grader drift resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_grader_drift_input", "grader drift input is invalid")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "grader_drift_operation_failed", "grader drift operation failed")
	}
}

func (h *Handler) record(r *http.Request, user auth.User, action, targetID string) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID,
		Action: action, TargetType: "grader_quality", TargetID: targetID, Reason: action,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

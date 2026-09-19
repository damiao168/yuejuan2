package calibration

import (
	"encoding/json"
	"errors"
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

// RegisterRoutes leaves permission vocabulary with the composition root:
// policy management is administrative; session/attempt operations are grader
// work; qualification reads are needed both by administrators and claim gates.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage, requireGrade func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/calibration-policy", requireRead(h.GetPolicy))
	mux.Handle("PUT /api/v1/exams/{examId}/questions/{questionId}/calibration-policy", requireManage(h.PutPolicy))
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/calibration-sessions", requireGrade(h.CreateSession))
	mux.Handle("GET /api/v1/calibration-sessions/{id}", requireGrade(h.GetSession))
	mux.Handle("POST /api/v1/calibration-sessions/{id}/attempts", requireGrade(h.SubmitAttempt))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/grader-qualification", requireRead(h.GetQualification))
}

func (h *Handler) PutPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input PutPolicyInput
	if !decodeBody(w, r, &input) {
		return
	}
	policy, err := h.service.PutPolicy(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_calibration.policy_configured", policy.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	policy, err := h.service.GetPolicy(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input CreateSessionInput
	if !decodeBody(w, r, &input) {
		return
	}
	graderID := strings.TrimSpace(input.GraderID)
	if graderID == "" {
		graderID = user.ID
	}
	if graderID != user.ID && !auth.HasPermission(user, "review:manage") {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "only review managers may start calibration for another grader")
		return
	}
	session, err := h.service.CreateSession(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), graderID)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "grader_calibration.started", session.ID)
	httpx.JSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (h *Handler) GetSession(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	session, err := h.service.GetSession(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	if session.GraderID != user.ID && !auth.HasPermission(user, "review:manage") {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "calibration session belongs to another grader")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"session": session})
}

func (h *Handler) SubmitAttempt(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	var input SubmitAttemptInput
	if !decodeBody(w, r, &input) {
		return
	}
	existing, err := h.service.GetSession(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	if existing.GraderID != user.ID && !auth.HasPermission(user, "review:manage") {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "calibration session belongs to another grader")
		return
	}
	attempt, session, qualification, err := h.service.SubmitAttempt(r.Context(), user.TenantID, r.PathValue("id"), input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	payload := map[string]any{"attempt": attempt, "session": session}
	if qualification != nil {
		payload["qualification"] = qualification
		h.record(r, user, "grader_calibration.completed", session.ID)
	}
	httpx.JSON(w, http.StatusOK, payload)
}

func (h *Handler) GetQualification(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	graderID := strings.TrimSpace(r.URL.Query().Get("grader_id"))
	if graderID == "" {
		graderID = user.ID
	}
	if graderID != user.ID && !auth.HasPermission(user, "review:manage") {
		httpx.Error(w, r, http.StatusForbidden, "forbidden", "only review managers may read another grader qualification")
		return
	}
	qualification, err := h.service.GetQualification(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), graderID)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"qualification": qualification})
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

func writeHandlerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "calibration_not_found", "calibration resource was not found")
	case errors.Is(err, ErrPolicyMissing):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "calibration_policy_missing", "configure a question-specific calibration policy first")
	case errors.Is(err, ErrGoldSetIncomplete):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "calibration_gold_set_incomplete", "the active approved Gold Set does not meet the configured sample requirement")
	case errors.Is(err, ErrSampleNotInSession), errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_calibration_input", "calibration input is invalid")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "calibration_conflict", "calibration state conflicts with this operation")
	case errors.Is(err, ErrQualificationRequired):
		httpx.Error(w, r, http.StatusForbidden, "grader_qualification_required", "current grader qualification is required")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "calibration_operation_failed", "calibration operation failed")
	}
}

func (h *Handler) record(r *http.Request, user auth.User, action, targetID string) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID,
		Action: action, TargetType: "grader_calibration", TargetID: targetID, Reason: action,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

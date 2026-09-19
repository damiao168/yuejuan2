package seedquality

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

// Only managers receive policy and observation routes. Claim, context and
// submit deliberately stay on the ordinary review routes so graders cannot
// infer that an item is a Seed from its endpoint.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/seed-policy", requireRead(h.GetPolicy))
	mux.Handle("PUT /api/v1/exams/{examId}/questions/{questionId}/seed-policy", requireManage(h.PutPolicy))
	mux.Handle("GET /api/v1/seed-observations", requireRead(h.ListObservations))
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
	policy, err := h.service.PutPolicy(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), user.ID, input)
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	h.record(r, user, "seed_sampling.policy_configured", policy.ID)
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

func (h *Handler) ListObservations(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListObservations(r.Context(), user.TenantID, ObservationFilter{
		ExamID: strings.TrimSpace(r.URL.Query().Get("exam_id")), QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")),
		GraderID: strings.TrimSpace(r.URL.Query().Get("grader_id")),
	})
	if err != nil {
		writeHandlerError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"observations": items})
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
		httpx.Error(w, r, http.StatusNotFound, "seed_quality_not_found", "Seed quality resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_seed_quality_input", "Seed quality input is invalid")
	case errors.Is(err, ErrGoldSetMissing):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "seed_gold_set_missing", "approve an active Gold Set before enabling Seed sampling")
	case errors.Is(err, ErrGoldSetChanged), errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "seed_quality_conflict", "Seed policy or active Gold Set changed")
	case errors.Is(err, ErrQualificationNeeded), errors.Is(err, ErrSeedTaskForbidden):
		httpx.Error(w, r, http.StatusForbidden, "seed_quality_forbidden", "current grader qualification is required")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "seed_quality_operation_failed", "Seed quality operation failed")
	}
}

func (h *Handler) record(r *http.Request, user auth.User, action, targetID string) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID,
		Action: action, TargetType: "seed_sampling_policy", TargetID: targetID, Reason: action,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

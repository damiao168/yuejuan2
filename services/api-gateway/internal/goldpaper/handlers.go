package goldpaper

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store Store
	audit auth.AuditRecorder
}

func NewHandler(store Store, audit auth.AuditRecorder) *Handler {
	return &Handler{store: store, audit: audit}
}

func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/questions/{questionId}/gold-papers", requireManage(h.Nominate))
	mux.Handle("GET /api/v1/exams/{examId}/questions/{questionId}/gold-coverage", requireRead(h.GetCoverage))
	mux.Handle("GET /api/v1/gold-papers", requireRead(h.List))
	mux.Handle("GET /api/v1/gold-papers/{id}", requireRead(h.Get))
	mux.Handle("POST /api/v1/gold-papers/{id}/versions", requireManage(h.CreateVersion))
	mux.Handle("POST /api/v1/gold-papers/{id}/versions/{version}/approve", requireManage(h.Approve))
	mux.Handle("POST /api/v1/gold-papers/{id}/retire", requireManage(h.Retire))
}

func (h *Handler) Nominate(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var input NominateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.Nominate(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "gold_paper.nominated", item.ID)
	httpx.JSON(w, http.StatusCreated, map[string]any{"gold_paper": item})
}

func (h *Handler) CreateVersion(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var input CreateVersionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.CreateVersion(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "gold_paper.version_nominated", item.ID)
	httpx.JSON(w, http.StatusCreated, map[string]any{"gold_paper": item})
}

func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version <= 0 {
		writeError(w, r, ErrInvalidInput)
		return
	}
	item, err := h.store.Approve(r.Context(), user.TenantID, r.PathValue("id"), user.ID, version)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "gold_paper.approved", item.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"gold_paper": item})
}

func (h *Handler) Retire(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var input RetireInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.Retire(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditAction(r, user, "gold_paper.retired", item.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"gold_paper": item})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	item, err := h.store.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"gold_paper": item})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	filter := ListFilter{ExamID: strings.TrimSpace(r.URL.Query().Get("exam_id")), QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")), Status: Status(strings.TrimSpace(r.URL.Query().Get("status")))}
	items, err := h.store.List(r.Context(), user.TenantID, filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"gold_papers": items})
}

func (h *Handler) GetCoverage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	coverage, err := h.store.Coverage(r.Context(), user.TenantID, r.PathValue("examId"), r.PathValue("questionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"coverage": coverage})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
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
		httpx.Error(w, r, http.StatusNotFound, "gold_paper_not_found", "Gold Paper was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_gold_paper_input", "Gold Paper input is invalid")
	case errors.Is(err, ErrSourceGradeMissing):
		httpx.Error(w, r, http.StatusUnprocessableEntity, "gold_source_grade_missing", "source grades must belong to the nominated answer")
	case errors.Is(err, ErrConflict), errors.Is(err, ErrAlreadyApproved), errors.Is(err, ErrApprovedImmutable):
		httpx.Error(w, r, http.StatusConflict, "gold_paper_conflict", "Gold Paper state conflicts with this operation")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "gold_paper_operation_failed", "Gold Paper operation failed")
	}
}

func (h *Handler) auditAction(r *http.Request, user auth.User, action, targetID string) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "grading_gold_paper", TargetID: targetID, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

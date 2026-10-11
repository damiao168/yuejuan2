package aidisagreement

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// RegisterRoutes 特意不提供采集端点。采集是复核提交后的进程内钩子，浏览器不得自行指定 AI/人工来源 ID 或分数。
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireRead, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/ai-human-disagreements", requireRead(h.List))
	mux.Handle("GET /api/v1/ai-human-disagreements/dataset", requireManage(h.Dataset))
	mux.Handle("GET /api/v1/ai-human-disagreements/{id}", requireRead(h.Get))
	mux.Handle("PUT /api/v1/ai-human-disagreements/{id}/classification", requireManage(h.Classify))
	mux.Handle("POST /api/v1/ai-human-disagreements/{id}/route", requireManage(h.Route))
}

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := disagreementUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.List(r.Context(), user.TenantID, parseFilter(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disagreements": items})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := disagreementUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disagreement": item, "recommended_follow_ups": RecommendedFollowUps(item.Taxonomy)})
}

func (h *Handler) Classify(w http.ResponseWriter, r *http.Request) {
	user, ok := disagreementUser(w, r)
	if !ok {
		return
	}
	var input ClassifyInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Classify(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disagreement": item, "recommended_follow_ups": RecommendedFollowUps(item.Taxonomy)})
}

func (h *Handler) Route(w http.ResponseWriter, r *http.Request) {
	user, ok := disagreementUser(w, r)
	if !ok {
		return
	}
	var input RouteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Route(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"disagreement": item})
}

func (h *Handler) Dataset(w http.ResponseWriter, r *http.Request) {
	user, ok := disagreementUser(w, r)
	if !ok {
		return
	}
	entries, err := h.service.Dataset(r.Context(), user.TenantID, parseFilter(r))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"dataset": entries, "anonymized": true})
}

func disagreementUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func parseFilter(r *http.Request) Filter {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return Filter{
		ExamID: strings.TrimSpace(r.URL.Query().Get("exam_id")), QuestionID: strings.TrimSpace(r.URL.Query().Get("question_id")),
		Status: Status(strings.TrimSpace(r.URL.Query().Get("status"))), Severity: Severity(strings.TrimSpace(r.URL.Query().Get("severity"))), Limit: limit,
	}
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
		httpx.Error(w, r, http.StatusNotFound, "ai_human_disagreement_not_found", "AI-human disagreement was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_ai_human_disagreement_input", "AI-human disagreement input is invalid")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "ai_human_disagreement_conflict", "AI-human disagreement changed; reload before retrying")
	case errors.Is(err, ErrInvalidState):
		httpx.Error(w, r, http.StatusConflict, "ai_human_disagreement_state_conflict", "AI-human disagreement state does not allow this action")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "ai_human_disagreement_operation_failed", "AI-human disagreement operation failed")
	}
}

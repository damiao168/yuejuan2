package modelcalibration

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// RegisterRoutes 由组装入口接在模型评测管理权限之后。这些端点只管理证据产物，不提供评分或发布学生答案的路由。
func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/model-calibrations", requireManage(handler.Create))
	mux.Handle("GET /api/v1/model-calibrations", requireManage(handler.List))
	mux.Handle("GET /api/v1/model-calibrations/{calibrationId}", requireManage(handler.Get))
	mux.Handle("GET /api/v1/model-calibrations/{calibrationId}/evidence", requireManage(handler.ListEvidence))
	mux.Handle("POST /api/v1/model-calibrations/{calibrationId}/evidence", requireManage(handler.AddEvidence))
	mux.Handle("POST /api/v1/model-calibrations/{calibrationId}/complete", requireManage(handler.Complete))
	mux.Handle("POST /api/v1/model-calibrations/{calibrationId}/approve", requireManage(handler.Approve))
	mux.Handle("POST /api/v1/model-calibrations/{calibrationId}/invalidate", requireManage(handler.Invalidate))
	mux.Handle("POST /api/v1/model-score-candidates", requireManage(handler.RecordCandidate))
}

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	var input CreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Create(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"calibration": item})
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	axis := axisFromQuery(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.List(r.Context(), user.TenantID, axis, limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibrations": items})
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("calibrationId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": item})
}
func (h *Handler) ListEvidence(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListEvidence(r.Context(), user.TenantID, r.PathValue("calibrationId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evidence": items})
}
func (h *Handler) AddEvidence(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	var input AddEvidenceInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.AddEvidence(r.Context(), user.TenantID, r.PathValue("calibrationId"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"evidence": item})
}
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Complete(r.Context(), user.TenantID, r.PathValue("calibrationId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": item})
}
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Approve(r.Context(), user.TenantID, r.PathValue("calibrationId"), user.ID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": item})
}
func (h *Handler) Invalidate(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Invalidate(r.Context(), user.TenantID, r.PathValue("calibrationId"), user.ID, input.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"calibration": item})
}
func (h *Handler) RecordCandidate(w http.ResponseWriter, r *http.Request) {
	user, ok := calibrationUser(w, r)
	if !ok {
		return
	}
	var input RecordCandidateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.RecordCandidate(r.Context(), user.TenantID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"candidate": item})
}

func axisFromQuery(r *http.Request) Axis {
	return Axis{ModelReference: r.URL.Query().Get("model_reference"), PromptVersion: r.URL.Query().Get("prompt_version"), RubricVersion: r.URL.Query().Get("rubric_version"), Subject: r.URL.Query().Get("subject"), Archetype: r.URL.Query().Get("archetype"), SliceKey: r.URL.Query().Get("slice_key")}
}
func calibrationUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
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
		httpx.Error(w, r, http.StatusNotFound, "model_calibration_not_found", "model calibration resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_model_calibration_input", "model calibration input is invalid")
	case errors.Is(err, ErrEvidenceUnavailable), errors.Is(err, ErrEvaluationRequired):
		httpx.Error(w, r, http.StatusConflict, "aligned_evaluation_required", "a completed matching aligned evaluation observation is required")
	case errors.Is(err, ErrConflict), errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "model_calibration_conflict", "model calibration state changed; reload before retrying")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "model_calibration_operation_failed", "model calibration operation failed")
	}
}

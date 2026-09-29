package gradingevaluation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// RegisterRoutes is intentionally not called by server.go yet. A composition
// root should connect it behind the model-evaluation management permission;
// this package cannot make a production model eligible by itself.
// 评估运行、观测和失效操作统一要求管理权限；结果读取也沿用同一受控服务边界。
func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/grading-evaluations", requireManage(handler.CreateRun))
	mux.Handle("GET /api/v1/grading-evaluations", requireManage(handler.ListRuns))
	mux.Handle("GET /api/v1/grading-evaluations/{runId}", requireManage(handler.GetRun))
	mux.Handle("POST /api/v1/grading-evaluations/{runId}/observations", requireManage(handler.AddObservation))
	mux.Handle("POST /api/v1/grading-evaluations/{runId}/complete", requireManage(handler.Complete))
	mux.Handle("POST /api/v1/grading-evaluations/{runId}/invalidate", requireManage(handler.Invalidate))
	mux.Handle("GET /api/v1/grading-evaluations/{runId}/slice-metrics", requireManage(handler.ListSliceMetrics))
	mux.Handle("GET /api/v1/grading-evaluations/{runId}/response-difficulty", requireManage(handler.ListResponseDifficulty))
	mux.Handle("GET /api/v1/grading-evaluations/{runId}/quality-summary", requireManage(handler.QualitySummary))
}

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) CreateRun(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	var input CreateRunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.CreateRun(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"evaluation_run": item})
}
func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := h.service.ListRuns(r.Context(), user.TenantID, RunFilter{Limit: limit})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_runs": items})
}
func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.GetRun(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_run": item})
}
func (h *Handler) AddObservation(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	var input AddObservationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.AddObservation(r.Context(), user.TenantID, r.PathValue("runId"), input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"observation": item})
}
func (h *Handler) Complete(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.Complete(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_run": item})
}
func (h *Handler) Invalidate(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.service.Invalidate(r.Context(), user.TenantID, r.PathValue("runId"), input.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evaluation_run": item})
}
func (h *Handler) ListSliceMetrics(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListSliceMetrics(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"slice_metrics": items})
}
func (h *Handler) ListResponseDifficulty(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListResponseDifficulty(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"response_difficulty": items})
}

func (h *Handler) QualitySummary(w http.ResponseWriter, r *http.Request) {
	user, ok := evaluationUser(w, r)
	if !ok {
		return
	}
	item, err := h.service.QualitySummary(r.Context(), user.TenantID, r.PathValue("runId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"quality_summary": item})
}

func evaluationUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
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
		httpx.Error(w, r, http.StatusNotFound, "grading_evaluation_not_found", "grading evaluation resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_grading_evaluation_input", "aligned offline evaluation input is invalid")
	case errors.Is(err, ErrConflict):
		httpx.Error(w, r, http.StatusConflict, "grading_evaluation_conflict", "evaluation run changed; reload before completing it")
	case errors.Is(err, ErrStateConflict):
		httpx.Error(w, r, http.StatusConflict, "grading_evaluation_state_conflict", "evaluation run state does not allow this operation")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "grading_evaluation_operation_failed", "grading evaluation operation failed")
	}
}

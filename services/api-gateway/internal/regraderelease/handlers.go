package regraderelease

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// 创建后继成绩发布属于管理操作，路由层先拦截普通阅卷和学生账号。
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireManage func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/regrade-jobs/{jobId}/score-release", requireManage(h.Create))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var input Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_json", "request body must contain one JSON object with known fields")
		return
	}
	release, err := h.service.Create(r.Context(), user.TenantID, r.PathValue("jobId"), user.ID, input)
	if errors.Is(err, ErrNotReady) {
		httpx.Error(w, r, http.StatusConflict, "regrade_release_not_ready", "all regrade items must be reviewed before creating a successor release")
		return
	}
	if errors.Is(err, ErrInvalid) || errors.Is(err, scorerelease.ErrInvalidInput) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_regrade_release_input", "regrade release input is invalid")
		return
	}
	if errors.Is(err, scorerelease.ErrNotFound) || errors.Is(err, regrade.ErrNotFound) {
		httpx.Error(w, r, http.StatusNotFound, "regrade_release_not_found", "regrade job or frozen release was not found")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "regrade_release_failed", "could not create successor score release")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"score_release": release})
}

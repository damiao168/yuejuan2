package qualitydashboard

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// RegisterRoutes 接受组装入口的权限包装器。
// 此仪表盘是学校管理员投影，不能仅因普通阅卷人员能处理复核任务就向其开放。
func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireRead func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/quality-dashboard", requireRead(handler.Get))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if h == nil || h.service == nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "quality_dashboard_unavailable", "quality dashboard is not configured")
		return
	}
	dashboard, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidInput):
			httpx.Error(w, r, http.StatusBadRequest, "invalid_quality_dashboard_input", "exam id is required")
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, r, http.StatusNotFound, "quality_dashboard_not_found", "exam questions were not found")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "quality_dashboard_failed", "quality dashboard could not be loaded")
		}
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"dashboard": dashboard})
}

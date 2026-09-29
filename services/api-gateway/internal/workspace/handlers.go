package workspace

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct {
	service *Service
}

func NewHandler(deps Dependencies) *Handler {
	return &Handler{service: NewService(deps)}
}

// 先确认登录和访问范围，再把越权考试伪装成 404，避免泄露考试是否存在。
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	_, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	scope, ok := auth.AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	projection, err := h.service.Get(r.Context(), scope, r.PathValue("examId"))
	if err != nil {
		if errors.Is(err, exam.ErrNotFound) || errors.Is(err, exam.ErrScopeForbidden) {
			httpx.Error(w, r, http.StatusNotFound, "exam_not_found", "exam not found")
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "exam_workspace_unavailable", "exam workspace is temporarily unavailable")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"workspace": projection})
}

func RegisterRoutes(mux *http.ServeMux, handler *Handler, requireRead func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/exams/{examId}/workspace", requireRead(handler.Get))
}

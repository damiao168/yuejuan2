package studentportal

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type Handler struct{ service *Service }

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// RegisterRoutes 添加 A21 发现端点。A18 已在相同路径系列中注册两个标准的学生安全结果端点：
//
//	GET /api/v1/student/exams/{examId}/result
//	GET /api/v1/student/exams/{examId}/questions/{questionId}
//
// 这些 DTO 保持唯一归属，避免门户偏移为管理员或复核表示，也避免注册重复的 ServeMux 模式。
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireStudentRead func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /api/v1/student/exams", requireStudentRead(h.ListPublishedExams))
}

func (h *Handler) ListPublishedExams(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentUser(w, r)
	if !ok {
		return
	}
	items, err := h.service.ListPublishedExams(r.Context(), user.TenantID, studentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"exams": items})
}

func studentUser(w http.ResponseWriter, r *http.Request) (auth.User, string, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return auth.User{}, "", false
	}
	studentID, scoped := auth.ScopedStudentID(user)
	if !auth.HasPermission(user, "student:grade:read") || !scoped {
		httpx.Error(w, r, http.StatusForbidden, "student_score_scope_required", "student scope is required for published scores")
		return auth.User{}, "", false
	}
	return user, studentID, true
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_student_portal_input", "student portal request is invalid")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "student_result_not_found", "no published result was found")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "student_portal_operation_failed", "student portal operation failed")
	}
}

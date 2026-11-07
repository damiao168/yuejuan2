package reviewannotation

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

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

func (h *Handler) CreateAnnotation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input CreateAnnotationInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	item, err := h.store.CreateAnnotation(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.annotation_created", "review_annotation", item.ID)
	httpx.JSON(w, http.StatusCreated, map[string]any{"annotation": item})
}

func (h *Handler) ListAnnotations(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	items, err := h.store.ListAnnotations(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"annotations": items})
}

func (h *Handler) GetAnnotation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	item, err := h.store.GetAnnotation(r.Context(), user.TenantID, r.PathValue("annotationId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"annotation": item})
}

func (h *Handler) ListStudentAnnotations(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	items, err := h.store.ListStudentAnnotations(r.Context(), user.TenantID, r.PathValue("submissionId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"annotations": items})
}

// 学生查询从身份上下文取得 student_id，只读取当前发布版本的公开批注；
// 不接收 URL 中的学生或提交 ID，避免横向枚举他人答案。
func (h *Handler) ListStudentQuestionAnnotations(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	studentID, scoped := auth.ScopedStudentID(user)
	if !auth.HasPermission(user, "student:grade:read") || !scoped {
		httpx.Error(w, r, http.StatusForbidden, "student_score_scope_required", "student scope is required for published annotations")
		return
	}
	items, err := h.store.ListStudentQuestionAnnotations(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"), r.URL.Query().Get("expected_release_id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"annotations": items})
}

func (h *Handler) UpdateAnnotation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input UpdateAnnotationInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	item, err := h.store.UpdateAnnotation(r.Context(), user.TenantID, r.PathValue("annotationId"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.annotation_updated", "review_annotation", item.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"annotation": item})
}

func (h *Handler) DeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input DeleteInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	id := r.PathValue("annotationId")
	if err := h.store.DeleteAnnotation(r.Context(), user.TenantID, id, user.ID, input.ExpectedRevision); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.annotation_deleted", "review_annotation", id)
	w.WriteHeader(http.StatusNoContent)
}

// 限制请求体大小、拒绝未知字段和尾随 JSON，避免客户端误传字段却被静默忽略。
func decodeStrictJSON(w http.ResponseWriter, r *http.Request, target any) bool {
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

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrStudentReleaseChanged):
		httpx.Error(w, r, http.StatusConflict, "student_release_changed", "published scores changed; review the current result before filing an appeal")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_annotation_not_found", "review annotation resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_annotation_input", "review annotation input is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "review_annotation_revision_conflict", "resource changed; reload before saving")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_annotation_operation_failed", "review annotation operation failed")
	}
}

func (h *Handler) auditAction(r *http.Request, user auth.User, action, targetType, targetID string) {
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action,
		TargetType: targetType, TargetID: targetID, Reason: action,
		IPAddress: httpx.ClientIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}

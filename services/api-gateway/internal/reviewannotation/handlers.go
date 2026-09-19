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

// ListStudentQuestionAnnotations exposes only the public annotation DTO for
// the current student's currently released answer. It has no submission-id
// parameter by design, which prevents horizontal enumeration of answer pages.
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
	items, err := h.store.ListStudentQuestionAnnotations(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"))
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

func (h *Handler) CreateCommentTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input CreateCommentTemplateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	item, err := h.store.CreateCommentTemplate(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.comment_template_created", "review_comment_template", item.ID)
	httpx.JSON(w, http.StatusCreated, map[string]any{"comment_template": item})
}

func (h *Handler) ListCommentTemplates(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	items, err := h.store.ListCommentTemplates(r.Context(), user.TenantID, user.ID)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"comment_templates": items})
}

func (h *Handler) GetCommentTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	item, err := h.store.GetCommentTemplate(r.Context(), user.TenantID, user.ID, r.PathValue("templateId"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"comment_template": item})
}

func (h *Handler) UpdateCommentTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input UpdateCommentTemplateInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	item, err := h.store.UpdateCommentTemplate(r.Context(), user.TenantID, user.ID, r.PathValue("templateId"), input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.comment_template_updated", "review_comment_template", item.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"comment_template": item})
}

func (h *Handler) DeleteCommentTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	var input DeleteInput
	if !decodeStrictJSON(w, r, &input) {
		return
	}
	id := r.PathValue("templateId")
	if err := h.store.DeleteCommentTemplate(r.Context(), user.TenantID, user.ID, id, input.ExpectedRevision); err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.comment_template_deleted", "review_comment_template", id)
	w.WriteHeader(http.StatusNoContent)
}

// UseCommentTemplate resolves a personal shortcut and atomically increments
// usage_count. Returning the whole template lets clients insert the authoritative
// content without a second request.
func (h *Handler) UseCommentTemplate(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	item, err := h.store.UseCommentTemplate(r.Context(), user.TenantID, user.ID, r.PathValue("shortcut"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, user, "review.comment_template_used", "review_comment_template", item.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"comment_template": item})
}

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
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "review_annotation_not_found", "review annotation resource was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_review_annotation_input", "review annotation input is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "review_annotation_revision_conflict", "resource changed; reload before saving")
	case errors.Is(err, ErrShortcutConflict):
		httpx.Error(w, r, http.StatusConflict, "review_comment_shortcut_conflict", "shortcut is already in use")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "review_annotation_operation_failed", "review annotation operation failed")
	}
}

func (h *Handler) auditAction(r *http.Request, user auth.User, action, targetType, targetID string) {
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action,
		TargetType: targetType, TargetID: targetID, Reason: action,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}

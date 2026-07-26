package appeal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store Store
	audit auth.Store
}

func NewHandler(store Store, audit auth.Store) *Handler {
	return &Handler{store: store, audit: audit}
}

func (h *Handler) CreateAppeal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CreateAppealInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !hasPermission(user, "appeal:manage") {
		studentID, ok := scopedStudentID(user)
		if !ok {
			httpx.Error(w, r, http.StatusForbidden, "student_scope_required", "student scope is required")
			return
		}
		if input.StudentID == "" {
			input.StudentID = studentID
		}
		if input.StudentID != studentID {
			httpx.Error(w, r, http.StatusForbidden, "appeal_student_scope_violation", "student can only appeal own grade")
			return
		}
	}
	item, err := h.store.CreateAppeal(r.Context(), user.TenantID, user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "appeal.created", "appeal", item.ID, "create appeal")
	httpx.JSON(w, http.StatusCreated, map[string]any{"appeal": item})
}

func (h *Handler) ListAppeals(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	filter := ListFilter{
		ExamID:    r.URL.Query().Get("exam_id"),
		StudentID: r.URL.Query().Get("student_id"),
		Status:    r.URL.Query().Get("status"),
	}
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		filter.StudentID = ""
		filter.AssignedTo = user.ID
	} else if !hasPermission(user, "appeal:manage") {
		studentID, ok := scopedStudentID(user)
		if !ok {
			httpx.Error(w, r, http.StatusForbidden, "student_scope_required", "student scope is required")
			return
		}
		filter.StudentID = studentID
	}
	items, err := h.store.ListAppeals(r.Context(), user.TenantID, filter)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		for index := range items {
			items[index] = teacherAppealView(items[index])
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeals": items})
}

func (h *Handler) GetAppeal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	scopedID := ""
	if !hasPermission(user, "appeal:manage") && !hasPermission(user, "appeal:work") {
		studentID, ok := scopedStudentID(user)
		if !ok {
			httpx.Error(w, r, http.StatusForbidden, "student_scope_required", "student scope is required")
			return
		}
		scopedID = studentID
	}
	item, err := h.store.GetAppeal(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		if item.AssignedTo != user.ID {
			httpx.Error(w, r, http.StatusForbidden, "appeal_assignment_scope_violation", "teacher can only view assigned appeals")
			return
		}
		item = teacherAppealView(item)
	} else if !hasPermission(user, "appeal:manage") {
		if item.StudentID != scopedID {
			httpx.Error(w, r, http.StatusForbidden, "appeal_student_scope_violation", "student can only view own appeal")
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *Handler) AssignAppeal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input AssignAppealInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.AssignAppeal(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "appeal.assigned", "appeal", item.ID, "assigned_to="+input.AssignedTo)
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *Handler) SubmitRecommendation(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input SubmitRecommendationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.SubmitRecommendation(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	auditReason := fmt.Sprintf("recommendation=%s reason=%s", input.Recommendation, input.Reason)
	if input.RecommendedScore != nil {
		auditReason = fmt.Sprintf("recommendation=%s score=%.2f reason=%s", input.Recommendation, *input.RecommendedScore, input.Reason)
	}
	h.auditAction(r, "appeal.teacher_recommendation_submitted", "appeal", item.ID, auditReason)
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": teacherAppealView(item)})
}

func teacherAppealView(item Appeal) Appeal {
	item.StudentID = ""
	item.CreatedBy = ""
	item.ReviewedBy = ""
	item.ClosedBy = ""
	if item.Evidence != nil {
		for _, grade := range item.Evidence.HumanGrades {
			delete(grade, "reviewer_id")
		}
	}
	for index := range item.Adjustments {
		item.Adjustments[index].AdjustedBy = ""
	}
	return item
}

func (h *Handler) ReviewAppeal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input ReviewAppealInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, adjustment, err := h.store.ReviewAppeal(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "appeal.reviewed", "appeal", item.ID, "review appeal")
	if adjustment != nil {
		h.auditAction(r, "appeal.score_adjusted", "score_adjustment", adjustment.ID, "adjust score through appeal")
	}
	response := map[string]any{"appeal": item}
	if adjustment != nil {
		response["score_adjustment"] = adjustment
	}
	httpx.JSON(w, http.StatusOK, response)
}

func (h *Handler) CloseAppeal(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input CloseAppealInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := h.store.CloseAppeal(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "appeal.closed", "appeal", item.ID, "close appeal")
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *Handler) Statistics(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	stats, err := h.store.Statistics(r.Context(), user.TenantID, StatisticsFilter{ExamID: r.URL.Query().Get("exam_id")})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"statistics": stats})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "appeal_resource_not_found", "appeal resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_appeal_input", "appeal input is invalid")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "appeal_action_forbidden", "appeal action is forbidden")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_appeal_transition", "appeal transition is invalid")
	case errors.Is(err, ErrUnpublishedGrade):
		httpx.Error(w, r, http.StatusConflict, "grade_not_published", "grade is not published")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "appeal_operation_failed", "appeal operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
}

func hasPermission(user auth.User, permission string) bool {
	for _, item := range user.Permissions {
		if item == permission {
			return true
		}
	}
	return false
}

func scopedStudentID(user auth.User) (string, bool) {
	return auth.ScopedStudentID(user)
}

func (h *Handler) auditAction(r *http.Request, action string, targetType string, targetID string, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		IPAddress:  r.RemoteAddr,
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
}

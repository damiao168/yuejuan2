package appeal

import (
	"encoding/json"
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

// PublishedQuestionAppealHandler exposes separate student and staff shapes.
// It does not reuse the legacy mutable-grade appeal handler because its source
// and visibility boundary are materially different.
type PublishedQuestionAppealHandler struct {
	service      *PublishedQuestionAppealService
	audit        auth.AuditRecorder
	segmentImage segment.CropImageReader
}

func (h *PublishedQuestionAppealHandler) WithSegmentImage(reader segment.CropImageReader) *PublishedQuestionAppealHandler {
	h.segmentImage = reader
	return h
}

func (h *PublishedQuestionAppealHandler) Context(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authorizedStaffAppeal(r); err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	user := mustUser(r)
	context, err := h.service.Context(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	// Never serialize answer_segment_id. The browser gets a staff-gated image
	// route instead, so it cannot turn one appeal into an arbitrary crop URL.
	context.AnswerSegmentID = ""
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		// A reviewer needs the frozen scoring evidence, but not the student's
		// identity, internal submission/regrade identifiers, or another staff
		// member's identity.
		context = workerQuestionAppealContextView(context)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"context": context})
}

func (h *PublishedQuestionAppealHandler) AnswerImage(w http.ResponseWriter, r *http.Request) {
	if _, err := h.authorizedStaffAppeal(r); err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	user := mustUser(r)
	context, err := h.service.Context(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	if !context.AnswerImageReady || context.AnswerSegmentID == "" || h.segmentImage == nil {
		httpx.Error(w, r, http.StatusNotFound, "appeal_answer_image_unavailable", "appeal answer image is unavailable")
		return
	}
	resource, err := h.segmentImage.ReadCropImage(r.Context(), user.TenantID, context.AnswerSegmentID)
	if err != nil {
		segment.WriteImageError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func NewPublishedQuestionAppealHandler(service *PublishedQuestionAppealService, auditStore auth.AuditRecorder) *PublishedQuestionAppealHandler {
	return &PublishedQuestionAppealHandler{service: service, audit: auditStore}
}

func (h *PublishedQuestionAppealHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	studentID, ok := scopedStudentID(user)
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "student_scope_required", "student scope is required")
		return
	}
	var input CreatePublishedQuestionAppealInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	if examID := r.PathValue("examId"); examID != "" {
		if input.ExamID != "" && input.ExamID != examID {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_appeal_input", "exam id does not match request path")
			return
		}
		input.ExamID = examID
	}
	item, err := h.service.Create(r.Context(), user.TenantID, studentID, user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.created", "question_appeal", item.ID, "student appeal against published score release")
	httpx.JSON(w, http.StatusCreated, map[string]any{"appeal": studentQuestionAppealView(item)})
}

func (h *PublishedQuestionAppealHandler) List(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	filter := QuestionAppealFilter{ExamID: r.URL.Query().Get("exam_id"), Status: r.URL.Query().Get("status")}
	studentView := false
	if hasPermission(user, "appeal:manage") {
		// School administrators may see the staff workflow, including the
		// immutable source context and private decision note.
	} else if hasPermission(user, "appeal:work") {
		filter.AssignedTo = user.ID
	} else {
		studentID, ok := scopedStudentID(user)
		if !ok {
			httpx.Error(w, r, http.StatusForbidden, "student_scope_required", "student scope is required")
			return
		}
		filter.StudentID, studentView = studentID, true
	}
	items, err := h.service.List(r.Context(), user.TenantID, filter)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	if studentView {
		views := make([]StudentQuestionAppeal, 0, len(items))
		for _, item := range items {
			views = append(views, studentQuestionAppealView(item))
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"appeals": views})
		return
	}
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		for index := range items {
			items[index] = workerQuestionAppealView(items[index])
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeals": items})
}

func (h *PublishedQuestionAppealHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	item, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	if hasPermission(user, "appeal:manage") {
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
		return
	}
	if hasPermission(user, "appeal:work") {
		if item.AssignedTo != user.ID {
			httpx.Error(w, r, http.StatusForbidden, "appeal_assignment_scope_violation", "worker can only view assigned appeals")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": workerQuestionAppealView(item)})
		return
	}
	studentID, ok := scopedStudentID(user)
	if !ok || item.StudentID != studentID {
		httpx.Error(w, r, http.StatusForbidden, "appeal_student_scope_violation", "student can only view own appeal")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": studentQuestionAppealView(item)})
}

func (h *PublishedQuestionAppealHandler) StartReview(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !hasPermission(user, "appeal:manage") {
		httpx.Error(w, r, http.StatusForbidden, "appeal_action_forbidden", "appeal manager permission is required")
		return
	}
	var input StartQuestionAppealReviewInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	item, err := h.service.StartReview(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.review_started", "question_appeal", item.ID, "appeal assigned for review")
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *PublishedQuestionAppealHandler) Decide(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	item, err := h.authorizedStaffAppeal(r)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	_ = item
	var input DecideQuestionAppealInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	updated, err := h.service.Decide(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.decided", "question_appeal", updated.ID, "appeal decision recorded")
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": workerQuestionAppealView(updated)})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": updated})
}

func (h *PublishedQuestionAppealHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if _, err := h.authorizedStaffAppeal(r); err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	var input ResolveQuestionAppealInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	item, err := h.service.Resolve(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.resolved", "question_appeal", item.ID, "successor score release linked")
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": workerQuestionAppealView(item)})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *PublishedQuestionAppealHandler) Events(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if _, err := h.authorizedStaffAppeal(r); err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	events, err := h.service.Events(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	if hasPermission(user, "appeal:work") && !hasPermission(user, "appeal:manage") {
		for index := range events {
			events[index].ActorID = ""
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *PublishedQuestionAppealHandler) authorizedStaffAppeal(r *http.Request) (PublishedQuestionAppeal, error) {
	user := mustUser(r)
	item, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if hasPermission(user, "appeal:manage") || (hasPermission(user, "appeal:work") && item.AssignedTo == user.ID) {
		return item, nil
	}
	return PublishedQuestionAppeal{}, ErrForbidden
}

func decodePublishedQuestionAppealJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writePublishedQuestionAppealError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "appeal_resource_not_found", "appeal resource not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_appeal_input", "appeal input is invalid")
	case errors.Is(err, ErrSourceRelease):
		httpx.Error(w, r, http.StatusConflict, "appeal_source_release_invalid", "appeal must target a published release question")
	case errors.Is(err, ErrAppealWindowClosed):
		httpx.Error(w, r, http.StatusConflict, "appeal_window_closed", "appeal window is closed for this release")
	case errors.Is(err, ErrAppealAlreadyFiled):
		httpx.Error(w, r, http.StatusConflict, "appeal_already_filed", "this released question already has an appeal")
	case errors.Is(err, ErrResolutionRelease):
		httpx.Error(w, r, http.StatusConflict, "appeal_resolution_release_invalid", "appeal resolution must link a later published release")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "appeal_action_forbidden", "appeal action is forbidden")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "invalid_appeal_transition", "appeal transition is invalid")
	case errors.Is(err, ErrRevisionConflict):
		httpx.Error(w, r, http.StatusConflict, "resource_version_conflict", "appeal was updated by another user; refresh and retry")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "appeal_operation_failed", "appeal operation failed")
	}
}

func (h *PublishedQuestionAppealHandler) auditAction(r *http.Request, action, targetType, targetID, reason string) {
	user := mustUser(r)
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: targetType, TargetID: targetID, Reason: reason,
		IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
}

func workerQuestionAppealView(item PublishedQuestionAppeal) PublishedQuestionAppeal {
	item.StudentID, item.SubmissionID, item.RegradeJobID, item.NewReleaseID, item.CreatedBy, item.DecidedBy = "", "", "", "", "", ""
	return item
}

func workerQuestionAppealContextView(context PublishedQuestionAppealContext) PublishedQuestionAppealContext {
	context.Appeal = workerQuestionAppealView(context.Appeal)
	for index := range context.ReleaseHistory {
		context.ReleaseHistory[index].ID = ""
	}
	return context
}

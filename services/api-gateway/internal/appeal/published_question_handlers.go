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

// PublishedQuestionAppealHandler 分别提供学生端和教职工端的数据结构。其来源与可见性边界不同，因此不复用旧的可变成绩申诉处理器。
type PublishedQuestionAppealHandler struct {
	service      *PublishedQuestionAppealService
	audit        auth.AuditRecorder
	segmentImage segment.CropImageReader
	examSummary  QuestionAppealExamSummaryReader
}

func (h *PublishedQuestionAppealHandler) WithExamSummaryReader(reader QuestionAppealExamSummaryReader) *PublishedQuestionAppealHandler {
	h.examSummary = reader
	return h
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
	// 不得序列化 answer_segment_id。浏览器只取得受教职工权限保护的图片路由，不能将一条申诉变成任意裁剪图 URL。
	context.AnswerSegmentID = ""
	context.Appeal = h.withStaffExamSummary(r, context.Appeal, nil)
	if hasPermission(user, appealWorkPermission) && !hasPermission(user, appealManagePermission) {
		// 复核者需要冻结的评分证据，不需要学生身份、内部提交或重评标识，也不需要其他教职工的身份。
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
	if hasPermission(user, appealManagePermission) {
		// 学校管理员可查看教职工工作流，包括不可变的来源上下文和私有裁决备注。
	} else if hasPermission(user, appealWorkPermission) {
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
	// 一次列表刷新中相同考试只读取一次；缓存限于本请求，重读时仍可恢复先前失败的名称。
	summaries := make(map[string]QuestionAppealExamSummary)
	for index := range items {
		items[index] = h.withStaffExamSummary(r, items[index], summaries)
	}
	if hasPermission(user, appealWorkPermission) && !hasPermission(user, appealManagePermission) {
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
	if hasPermission(user, appealManagePermission) {
		item = h.withStaffExamSummary(r, item, nil)
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
		return
	}
	if hasPermission(user, appealWorkPermission) {
		if item.AssignedTo != user.ID {
			httpx.Error(w, r, http.StatusForbidden, "appeal_assignment_scope_violation", "worker can only view assigned appeals")
			return
		}
		item = h.withStaffExamSummary(r, item, nil)
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
	if !hasPermission(user, appealManagePermission) {
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
	item = h.withStaffExamSummary(r, item, nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": item})
}

func (h *PublishedQuestionAppealHandler) Decide(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	// 独立教师的意见交给管理员裁决，工作权限不能通过旧 decide 入口直接结案。
	if !hasPermission(user, appealManagePermission) {
		writePublishedQuestionAppealError(w, r, ErrForbidden)
		return
	}
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
	updated = h.withStaffExamSummary(r, updated, nil)
	if hasPermission(user, appealWorkPermission) && !hasPermission(user, appealManagePermission) {
		httpx.JSON(w, http.StatusOK, map[string]any{"appeal": workerQuestionAppealView(updated)})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": updated})
}

func (h *PublishedQuestionAppealHandler) Resolve(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !hasPermission(user, appealManagePermission) {
		writePublishedQuestionAppealError(w, r, ErrForbidden)
		return
	}
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
	item = h.withStaffExamSummary(r, item, nil)
	if hasPermission(user, appealWorkPermission) && !hasPermission(user, appealManagePermission) {
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
	if hasPermission(user, appealWorkPermission) && !hasPermission(user, appealManagePermission) {
		for index := range events {
			events[index].ActorID = ""
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": events})
}

func (h *PublishedQuestionAppealHandler) SubmitReviewOpinion(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	item, err := h.authorizedStaffAppeal(r)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	if !hasPermission(user, appealWorkPermission) || item.AssignedTo != user.ID {
		writePublishedQuestionAppealError(w, r, ErrForbidden)
		return
	}
	var input SubmitQuestionAppealReviewOpinionInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	updated, err := h.service.SubmitReviewOpinion(r.Context(), user.TenantID, item.ID, user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.review_opinion_submitted", "question_appeal", updated.ID, "independent review opinion handed to school administrator")
	updated = h.withStaffExamSummary(r, updated, nil)
	if !hasPermission(user, appealManagePermission) {
		updated = workerQuestionAppealView(updated)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": updated})
}

func (h *PublishedQuestionAppealHandler) authorizedStaffAppeal(r *http.Request) (PublishedQuestionAppeal, error) {
	user := mustUser(r)
	item, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if hasPermission(user, appealManagePermission) || (hasPermission(user, appealWorkPermission) && item.AssignedTo == user.ID) {
		return item, nil
	}
	return PublishedQuestionAppeal{}, ErrForbidden
}

func (h *PublishedQuestionAppealHandler) withStaffExamSummary(r *http.Request, item PublishedQuestionAppeal, summaries map[string]QuestionAppealExamSummary) PublishedQuestionAppeal {
	user := mustUser(r)
	if item.TenantID != user.TenantID || item.ExamID == "" ||
		(!hasPermission(user, appealManagePermission) && !(hasPermission(user, appealWorkPermission) && item.AssignedTo == user.ID)) {
		return item
	}
	item.ExamName, item.ExamSubject = "", ""
	if h.examSummary == nil {
		return item
	}
	summary, found := summaries[item.ExamID]
	if !found {
		// 授权来自这条申请，只读其精确考试摘要；失败不隐藏仍可复核的冻结证据，前端显示重读入口。
		var err error
		summary, err = h.examSummary.ReadQuestionAppealExamSummary(r.Context(), user.TenantID, item.ExamID)
		if err != nil {
			summary = QuestionAppealExamSummary{}
		}
		if summaries != nil {
			summaries[item.ExamID] = summary
		}
	}
	item.ExamName, item.ExamSubject = summary.Name, summary.Subject
	return item
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

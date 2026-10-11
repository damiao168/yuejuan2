package scorerelease

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type Handler struct {
	service                     *Service
	audit                       auth.AuditRecorder
	publisher                   PublicationPublisher
	studentQuestionImageReader  segment.CropImageReader
	studentPaperPageImageReader segment.PageImageReader
	highScorePaper              HighScorePaperManager
}

// PublicationPublisher 允许组装入口要求独立记录的策略门禁，而无需此不可变发布包导入策略包。
// 未提供它时，使用 A18 的事务发布器。
type PublicationPublisher interface {
	PublishPublication(context.Context, string, string, string, string) (Release, error)
}

func NewHandler(service *Service, audit auth.AuditRecorder) *Handler {
	return &Handler{service: service, audit: audit}
}

func (h *Handler) WithPublicationPublisher(publisher PublicationPublisher) *Handler {
	h.publisher = publisher
	return h
}

func (h *Handler) WithStudentQuestionImage(reader segment.CropImageReader) *Handler {
	h.studentQuestionImageReader = reader
	return h
}

func (h *Handler) WithStudentPaperPageImage(reader segment.PageImageReader) *Handler {
	h.studentPaperPageImageReader = reader
	return h
}

func (h *Handler) WithHighScorePaper(manager HighScorePaperManager) *Handler {
	h.highScorePaper = manager
	return h
}

// RouteGuards 将普通发布准备与两项可改变正式发布结果的操作分开。
type RouteGuards struct {
	ExamManage      func(http.HandlerFunc) http.Handler
	ReleaseManage   func(http.HandlerFunc) http.Handler
	CriticalRelease func(http.HandlerFunc) http.Handler
	CriticalExam    func(http.HandlerFunc) http.Handler
	StudentRead     func(http.HandlerFunc) http.Handler
}

// RegisterRoutes 区分考试作用域路由与发布 ID 路由，使组装入口可同时应用资源作用域和对应风险的认证要求。
func RegisterRoutes(mux *http.ServeMux, h *Handler, guards RouteGuards) {
	mux.Handle("POST /api/v1/exams/{examId}/score-releases", guards.ExamManage(h.Create))
	mux.Handle("GET /api/v1/exams/{examId}/score-releases", guards.ExamManage(h.List))
	mux.Handle("GET /api/v1/exams/{examId}/release-gate", guards.ExamManage(h.Gate))
	mux.Handle("GET /api/v1/exams/{examId}/score-releases/current-published", guards.ExamManage(h.Current))
	mux.Handle("GET /api/v1/score-releases/{id}", guards.ReleaseManage(h.Get))
	mux.Handle("GET /api/v1/score-releases/{id}/student-preview", guards.ReleaseManage(h.StudentPreview))
	mux.Handle("GET /api/v1/score-releases/{id}/diff", guards.ReleaseManage(h.Diff))
	mux.Handle("POST /api/v1/score-releases/{id}/publish", guards.CriticalRelease(h.Publish))
	mux.Handle("POST /api/v1/score-releases/{id}/high-score-paper/revoke", guards.CriticalRelease(h.RevokeHighScorePaper))
	mux.Handle("POST /api/v1/exams/{examId}/score-releases/rollback", guards.CriticalExam(h.Rollback))

	mux.Handle("GET /api/v1/student/exams/{examId}/result", guards.StudentRead(h.StudentResult))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}", guards.StudentRead(h.StudentQuestion))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/answer-image", guards.StudentRead(h.StudentQuestionImage))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/page-image", guards.StudentRead(h.StudentPaperPageImage))
}

func (h *Handler) RevokeHighScorePaper(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	releaseID := r.PathValue("id")
	if err := h.service.RevokeHighScorePaper(r.Context(), user.TenantID, releaseID, user.ID); err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "score_release.high_score_paper_revoked", releaseID, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	var input CreateInput
	if !decodeBody(w, r, &input) {
		return
	}
	release, err := h.service.Create(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "score_release.created", release.ID, map[string]any{"exam_id": release.ExamID, "version": release.Version, "source": release.Source})
	httpx.JSON(w, http.StatusCreated, map[string]any{"score_release": release})
}

func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	var input RollbackInput
	if !decodeBody(w, r, &input) {
		return
	}
	release, err := h.service.CreateRollback(r.Context(), user.TenantID, r.PathValue("examId"), user.ID, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "score_release.rollback_drafted", release.ID, map[string]any{"exam_id": release.ExamID, "version": release.Version, "source_release_id": release.SourceReleaseID})
	httpx.JSON(w, http.StatusCreated, map[string]any{"score_release": release})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	releases, err := h.service.List(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"score_releases": releases})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	detail, err := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, detail)
}

// 路由先校验 score:manage 和发布所属考试的资源边界，再在快照中核对所选学生。
func (h *Handler) StudentPreview(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	result, err := h.service.StudentPreview(r.Context(), user.TenantID, r.PathValue("id"), strings.TrimSpace(r.URL.Query().Get("student_id")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) Diff(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	diff, err := h.service.Diff(r.Context(), user.TenantID, r.PathValue("id"), strings.TrimSpace(r.URL.Query().Get("base")))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"diff": diff})
}

func (h *Handler) Gate(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	gate, err := h.service.Gate(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"release_gate": gate})
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	detail, err := h.service.CurrentPublished(r.Context(), user.TenantID, r.PathValue("examId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, detail)
}

func (h *Handler) Publish(w http.ResponseWriter, r *http.Request) {
	user, ok := releaseUser(w, r)
	if !ok {
		return
	}
	releaseID := r.PathValue("id")
	var release Release
	var err error
	if h.publisher != nil {
		// 在租户作用域内解析不可变发布所属的考试。
		// 传给策略协调器的关联绝不由浏览器提供。
		detail, detailErr := h.service.Get(r.Context(), user.TenantID, releaseID)
		if detailErr != nil {
			writeError(w, r, detailErr)
			return
		}
		release, err = h.publisher.PublishPublication(r.Context(), user.TenantID, detail.Release.ExamID, releaseID, user.ID)
	} else {
		release, err = h.service.Publish(r.Context(), user.TenantID, releaseID, user.ID)
	}
	if errors.Is(err, ErrGateBlocked) {
		detail, detailErr := h.service.Get(r.Context(), user.TenantID, r.PathValue("id"))
		if detailErr == nil {
			gate, gateErr := h.service.Gate(r.Context(), user.TenantID, detail.Release.ExamID)
			// Gate() 面向整个考试，不能包含发布快照检查。
			// 不能在被阻断的响应上附加与之矛盾的已通过门禁。
			if gateErr == nil && !gate.Passed {
				httpx.JSON(w, http.StatusConflict, map[string]any{"code": "score_release_gate_blocked", "message": "score release is blocked by the current quality gate", "release_gate": gate})
				return
			}
		}
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	h.auditEvent(r, user, "score_release.published", release.ID, map[string]any{"exam_id": release.ExamID, "version": release.Version, "supersedes_release_id": release.SupersedesReleaseID})
	httpx.JSON(w, http.StatusOK, map[string]any{"score_release": release})
}

func (h *Handler) StudentResult(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	result, err := h.service.StudentResult(r.Context(), user.TenantID, r.PathValue("examId"), studentID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// 公开契约一直要求questions数组；隐藏逐题时只返回空数组，不恢复任何隐藏的题目或分数。
	// 内部仍保留nil区分隐藏投影，不能用这个兼容输出改变证据接口的权限判断。
	questions := result.Questions
	if questions == nil {
		questions = []StudentQuestion{}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"result": struct {
		StudentResult
		Questions []StudentQuestion `json:"questions"`
	}{StudentResult: result, Questions: questions}})
}

func (h *Handler) StudentQuestion(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	question, err := h.service.StudentQuestion(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"), r.URL.Query().Get("expected_release_id"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Error(w, r, http.StatusNotFound, "student_question_unavailable", "published question details are unavailable")
			return
		}
		writeError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"question": question})
}

func (h *Handler) StudentQuestionImage(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	source, err := h.service.StudentQuestionImage(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"), r.URL.Query().Get("expected_release_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.studentQuestionImageReader == nil {
		httpx.Error(w, r, http.StatusNotFound, "student_answer_image_unavailable", "answer image is not available")
		return
	}
	// 公开路由绝不接受或暴露答案分段 ID。
	// 只有上方核验不可变发布、学生和题目关联后才设置该 ID；
	// 随后由既有证据 handler 检查文件完整性，再返回裁剪图。
	resource, err := h.studentQuestionImageReader.ReadCropImage(r.Context(), user.TenantID, source.AnswerSegmentID)
	if err != nil {
		segment.WriteImageError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func (h *Handler) StudentPaperPageImage(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	highScore := r.URL.Query().Get("variant") == "high_score"
	source, err := h.service.StudentPaperPageImage(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"), highScore, r.URL.Query().Get("expected_release_id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if highScore {
		if h.highScorePaper == nil || source.AnonymousPageID == "" || source.AnonymousFileAssetID == "" || source.ReleaseID == "" {
			httpx.Error(w, r, http.StatusNotFound, "student_paper_page_unavailable", "anonymous paper page is not available")
			return
		}
		resource, readErr := h.highScorePaper.ReadAnonymousPage(r.Context(), user.TenantID, source.ReleaseID, source.AnonymousPageID, source.AnonymousFileAssetID)
		if readErr != nil {
			writeError(w, r, readErr)
			return
		}
		binaryresourcehttp.Serve(w, r, resource, h.audit)
		return
	}
	if h.studentPaperPageImageReader == nil {
		httpx.Error(w, r, http.StatusNotFound, "student_paper_page_unavailable", "paper page is not available")
		return
	}
	resource, err := h.studentPaperPageImageReader.ReadPageImage(r.Context(), user.TenantID, source.AnswerSegmentID)
	if err != nil {
		segment.WriteImageError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

func releaseUser(w http.ResponseWriter, r *http.Request) (auth.User, bool) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
	}
	return user, ok
}

func studentReleaseUser(w http.ResponseWriter, r *http.Request) (auth.User, string, bool) {
	user, ok := releaseUser(w, r)
	if !ok {
		return auth.User{}, "", false
	}
	// 学生端只能使用认证上下文中的本人 ID；路径参数只标识考试和题目。
	studentID, scoped := auth.ScopedStudentID(user)
	if !auth.HasPermission(user, "student:grade:read") || !scoped {
		httpx.Error(w, r, http.StatusForbidden, "student_score_scope_required", "student scope is required for published scores")
		return auth.User{}, "", false
	}
	return user, studentID, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
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
	case errors.Is(err, ErrStudentReleaseChanged):
		httpx.Error(w, r, http.StatusConflict, "student_release_changed", "published scores changed; review the current result before filing an appeal")
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "score_release_not_found", "score release was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_score_release_input", "score release input is invalid")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "score_release_forbidden", "published question details are not visible")
	case errors.Is(err, ErrGateBlocked):
		httpx.Error(w, r, http.StatusConflict, "score_release_gate_blocked", "score release is blocked by the current quality gate")
	case errors.Is(err, ErrAnonymousPaperUnavailable):
		httpx.Error(w, r, http.StatusConflict, "anonymous_high_score_paper_unavailable", "anonymous high-score paper is unavailable")
	case errors.Is(err, ErrInvalidTransition):
		httpx.Error(w, r, http.StatusConflict, "score_release_invalid_transition", "score release is not in a valid state for this action")
	case errors.Is(err, ErrStaleSource):
		httpx.Error(w, r, http.StatusConflict, "score_release_stale_source", "published scores have changed; prepare the regrade again from the current release")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "score_release_operation_failed", "score release operation failed")
	}
}

func (h *Handler) auditEvent(r *http.Request, user auth.User, action, targetID string, after map[string]any) {
	if h.audit == nil {
		return
	}
	auth.RecordAudit(r.Context(), h.audit, auth.AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "score_release", TargetID: targetID, AfterValue: after, Reason: action, IPAddress: r.RemoteAddr, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

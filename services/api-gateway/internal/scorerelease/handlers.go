package scorerelease

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	service                      *Service
	audit                        auth.Store
	publisher                    PublicationPublisher
	studentQuestionImageHandler  http.HandlerFunc
	studentPaperPageImageHandler http.HandlerFunc
}

// PublicationPublisher lets the composition root require an independently
// recorded policy gate without making this immutable release package import
// the policy package. Without one, A18's transactional publisher is used.
type PublicationPublisher interface {
	PublishPublication(context.Context, string, string, string, string) (Release, error)
}

func NewHandler(service *Service, audit auth.Store) *Handler {
	return &Handler{service: service, audit: audit}
}

func (h *Handler) WithPublicationPublisher(publisher PublicationPublisher) *Handler {
	h.publisher = publisher
	return h
}

func (h *Handler) WithStudentQuestionImage(handler http.HandlerFunc) *Handler {
	h.studentQuestionImageHandler = handler
	return h
}

func (h *Handler) WithStudentPaperPageImage(handler http.HandlerFunc) *Handler {
	h.studentPaperPageImageHandler = handler
	return h
}

// RegisterRoutes keeps exam-scoped routes distinct from release-id routes.
// The composition root can therefore apply withScopedExam only where an
// examId is present, without accidentally rejecting a valid release-id URL.
func RegisterRoutes(mux *http.ServeMux, h *Handler, requireExamManage, requireReleaseManage, requireStudentRead func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /api/v1/exams/{examId}/score-releases", requireExamManage(h.Create))
	mux.Handle("GET /api/v1/exams/{examId}/score-releases", requireExamManage(h.List))
	mux.Handle("GET /api/v1/exams/{examId}/release-gate", requireExamManage(h.Gate))
	mux.Handle("GET /api/v1/exams/{examId}/score-releases/current-published", requireExamManage(h.Current))
	mux.Handle("GET /api/v1/score-releases/{id}", requireReleaseManage(h.Get))
	mux.Handle("GET /api/v1/score-releases/{id}/diff", requireReleaseManage(h.Diff))
	mux.Handle("POST /api/v1/score-releases/{id}/publish", requireReleaseManage(h.Publish))
	mux.Handle("POST /api/v1/exams/{examId}/score-releases/rollback", requireExamManage(h.Rollback))

	mux.Handle("GET /api/v1/student/exams/{examId}/result", requireStudentRead(h.StudentResult))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}", requireStudentRead(h.StudentQuestion))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/answer-image", requireStudentRead(h.StudentQuestionImage))
	mux.Handle("GET /api/v1/student/exams/{examId}/questions/{questionId}/page-image", requireStudentRead(h.StudentPaperPageImage))
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
		// Resolve the immutable release's exam under tenant scope. The browser
		// never supplies the association passed into the policy coordinator.
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
			if gateErr == nil {
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
	httpx.JSON(w, http.StatusOK, map[string]any{"result": result})
}

func (h *Handler) StudentQuestion(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	question, err := h.service.StudentQuestion(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"))
	if err != nil {
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
	source, err := h.service.StudentQuestionImage(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.studentQuestionImageHandler == nil {
		httpx.Error(w, r, http.StatusNotFound, "student_answer_image_unavailable", "answer image is not available")
		return
	}
	// The public route never accepts or exposes an answer segment ID. It is set
	// only after the immutable release, student and question association has
	// been checked above, then the existing evidence handler performs its
	// file-integrity checks before serving the crop.
	r.SetPathValue("id", source.AnswerSegmentID)
	h.studentQuestionImageHandler(w, r)
}

func (h *Handler) StudentPaperPageImage(w http.ResponseWriter, r *http.Request) {
	user, studentID, ok := studentReleaseUser(w, r)
	if !ok {
		return
	}
	highScore := r.URL.Query().Get("variant") == "high_score"
	source, err := h.service.StudentPaperPageImage(r.Context(), user.TenantID, r.PathValue("examId"), studentID, r.PathValue("questionId"), highScore)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.studentPaperPageImageHandler == nil {
		httpx.Error(w, r, http.StatusNotFound, "student_paper_page_unavailable", "paper page is not available")
		return
	}
	r.SetPathValue("id", source.AnswerSegmentID)
	h.studentPaperPageImageHandler(w, r)
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
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "score_release_not_found", "score release was not found")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_score_release_input", "score release input is invalid")
	case errors.Is(err, ErrForbidden):
		httpx.Error(w, r, http.StatusForbidden, "score_release_forbidden", "published question details are not visible")
	case errors.Is(err, ErrGateBlocked):
		httpx.Error(w, r, http.StatusConflict, "score_release_gate_blocked", "score release is blocked by the current quality gate")
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

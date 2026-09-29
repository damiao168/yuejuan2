package segment

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresourcehttp"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	submissionpkg "edugrade-enterprise/services/api-gateway/internal/submission"
)

type Handler struct {
	store       Store
	papers      paper.QuestionRepository
	submissions submissionpkg.Store
	audit       auth.Store
	images      *ImageService
}

func NewHandler(store Store, papers paper.QuestionRepository, submissions submissionpkg.Store, audit auth.Store, fileStore files.Store, objects files.ObjectStorage) *Handler {
	return &Handler{store: store, papers: papers, submissions: submissions, audit: audit,
		images: NewImageService(store, submissions, fileStore, objects)}
}

func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
	// 旧接口只负责把已配置的答题区域转换为片段；提交物必须先进入 ready_for_ocr，避免对未完成质检的页面生成坐标。
	w.Header().Set("Deprecation", "true")
	w.Header().Set("Sunset", "Thu, 01 Oct 2026 00:00:00 GMT")
	w.Header().Set("Warning", `299 EduGrade "Legacy direct segmentation is deprecated; use the capture batch registration pipeline"`)
	user := mustUser(r)
	submissionID := r.PathValue("id")
	sub, err := h.submissions.Get(r.Context(), user.TenantID, submissionID)
	if err != nil {
		writeStoreError(w, r, ErrNotFound)
		return
	}
	w.Header().Set("Link", fmt.Sprintf("</api/v1/exams/%s/capture-batches>; rel=\"successor-version\"", sub.ExamID))
	if sub.Status != "ready_for_ocr" {
		writeStoreError(w, r, ErrNotReady)
		return
	}
	questions, err := h.papers.ListQuestions(r.Context(), user.TenantID, sub.ExamID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "segment_question_load_failed", "failed to load questions")
		return
	}
	pages, err := h.submissions.ListPages(r.Context(), user.TenantID, submissionID)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "segment_page_load_failed", "failed to load submission pages")
		return
	}
	pageByNo := map[int]string{}
	for _, page := range pages {
		pageByNo[page.PageNo] = page.ID
	}

	issues := []Issue{}
	inputs := []CreateSegmentInput{}
	if len(questions) == 0 {
		issues = append(issues, Issue{Code: "no_questions", Message: "exam has no configured questions"})
	}
	for _, question := range questions {
		if question.AnswerArea == nil {
			issues = append(issues, Issue{Code: "answer_area_missing", Message: "question " + question.QuestionNo + " is missing answer_area"})
			continue
		}
		pageNo, bbox, err := ParseAnswerArea(question.AnswerArea)
		if err != nil {
			issues = append(issues, Issue{Code: "answer_area_invalid", Message: "question " + question.QuestionNo + ": " + err.Error()})
			continue
		}
		pageID, ok := pageByNo[pageNo]
		if !ok {
			issues = append(issues, Issue{Code: "submission_page_missing", Message: fmt.Sprintf("question %s expects page %d, but submission page is missing", question.QuestionNo, pageNo)})
			continue
		}
		inputs = append(inputs, CreateSegmentInput{
			TenantID:         user.TenantID,
			SubmissionID:     submissionID,
			SubmissionPageID: pageID,
			QuestionID:       question.ID,
			QuestionNo:       question.QuestionNo,
			BBox:             bbox,
			Source:           "configured_answer_area",
			Status:           "generated",
		})
	}
	// 即使部分题目配置有误，也返回可生成的片段和逐题问题，便于维护人员修正配置后重试。
	segments := []Segment{}
	if len(inputs) > 0 {
		segments, err = h.store.CreateSegments(r.Context(), inputs)
		if err != nil {
			writeStoreError(w, r, err)
			return
		}
	}
	h.auditAction(r, "segment.generated", "submission", submissionID, "generate answer segments from configured answer areas")
	httpx.JSON(w, http.StatusOK, map[string]any{"result": GenerateResult{Valid: len(issues) == 0, Issues: issues, Segments: segments}})
}

func (h *Handler) ListBySubmission(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	out, err := h.store.ListBySubmission(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"segments": out})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	var input UpdateSegmentInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Status != nil {
		status := strings.TrimSpace(*input.Status)
		input.Status = &status
	}
	if input.ReviewNotes != nil {
		notes := strings.TrimSpace(*input.ReviewNotes)
		input.ReviewNotes = &notes
	}
	out, err := h.store.Update(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	h.auditAction(r, "segment.updated", "answer_segment", out.ID, "update answer segment")
	httpx.JSON(w, http.StatusOK, map[string]any{"segment": out})
}

func (h *Handler) GetEvidence(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	evidence, err := h.store.GetEvidence(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if evidence.ProcessingStatus != "completed" || evidence.RegistrationStatus != "completed" || evidence.CropFileAssetID == "" || evidence.CropSHA256 == "" {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"evidence": evidence})
}

func (h *Handler) GetImage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	resource, err := h.images.ReadCropImage(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}

// GetPageImage serves the full answer-sheet page that owns an already
// authorized answer segment. Public callers never provide a submission or
// file id, so the score-release boundary remains the source of authorization.
func (h *Handler) GetPageImage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	resource, err := h.images.ReadPageImage(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	binaryresourcehttp.Serve(w, r, resource, h.audit)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	// 将领域错误稳定映射为客户端可处理的状态码；底层数据库错误不泄露细节。
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Error(w, r, http.StatusNotFound, "answer_segment_not_found", "answer segment not found")
	case errors.Is(err, ErrNotReady):
		httpx.Error(w, r, http.StatusConflict, "submission_not_ready_for_segmentation", "submission must be ready_for_ocr before segmentation")
	case errors.Is(err, ErrInvalidInput):
		httpx.Error(w, r, http.StatusBadRequest, "invalid_answer_segment", "answer segment input is invalid")
	case errors.Is(err, ErrEvidenceUnavailable):
		httpx.Error(w, r, http.StatusConflict, "answer_segment_evidence_unavailable", "answer segment image is not active grading evidence")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "answer_segment_operation_failed", "answer segment operation failed")
	}
}

func mustUser(r *http.Request) auth.User {
	user, _ := auth.UserFromContext(r.Context())
	return user
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

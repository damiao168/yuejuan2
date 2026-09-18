package segment

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/auth"
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
	fileStore   files.Store
	objects     files.ObjectStorage
}

func NewHandler(store Store, papers paper.QuestionRepository, submissions submissionpkg.Store, audit auth.Store, fileStore files.Store, objects files.ObjectStorage) *Handler {
	return &Handler{store: store, papers: papers, submissions: submissions, audit: audit, fileStore: fileStore, objects: objects}
}

func (h *Handler) Generate(w http.ResponseWriter, r *http.Request) {
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
	evidence, err := h.store.GetEvidence(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if evidence.ProcessingStatus != "completed" || evidence.RegistrationStatus != "completed" || evidence.CropFileAssetID == "" || evidence.CropSHA256 == "" {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	asset, err := h.fileStore.Get(r.Context(), user.TenantID, evidence.CropFileAssetID)
	if err != nil || asset.ExamID != evidence.ExamID || asset.HashSHA256 != evidence.CropSHA256 || asset.DeletedAt != nil || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	validOwner := (asset.OwnerType == "answer_segment_crop" && asset.OwnerID == evidence.RegistrationRunID) || (asset.OwnerType == "page_registration_correction_preview" && evidence.CorrectionID != "" && asset.OwnerID == evidence.CorrectionID)
	if !validOwner {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	etag := `"sha256:` + evidence.CropSHA256 + `"`
	w.Header().Set("ETag", etag)
	// The segment URL is stable while its crop evidence may be replaced after a
	// registration correction. Revalidate the private cache so graders never
	// keep seeing an obsolete crop under the same URL.
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "segment-" + evidence.SegmentID + ".png"}))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := h.objects.Get(r.Context(), asset.StorageBucket, asset.StorageKey)
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "object_storage_failed", "failed to read segment image")
		return
	}
	defer body.Close()
	h.auditAction(r, "segment.image_viewed", "answer_segment", evidence.SegmentID, "view active answer segment image")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// GetPageImage serves the full answer-sheet page that owns an already
// authorized answer segment. Public callers never provide a submission or
// file id, so the score-release boundary remains the source of authorization.
func (h *Handler) GetPageImage(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	evidence, err := h.store.GetEvidence(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	pages, err := h.submissions.ListPages(r.Context(), user.TenantID, evidence.SubmissionID)
	if err != nil {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	assetID := ""
	for _, page := range pages {
		if page.ID == evidence.SubmissionPageID {
			assetID = page.NormalizedFileAssetID
			if assetID == "" {
				assetID = page.FileAssetID
			}
			break
		}
	}
	if assetID == "" {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	asset, err := h.fileStore.Get(r.Context(), user.TenantID, assetID)
	if err != nil || asset.DeletedAt != nil || asset.ExamID != evidence.ExamID || !strings.HasPrefix(asset.ContentType, "image/") || asset.SizeBytes <= 0 {
		writeStoreError(w, r, ErrEvidenceUnavailable)
		return
	}
	etag := `"sha256:` + asset.HashSHA256 + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", asset.SizeBytes))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": fmt.Sprintf("paper-page-%s", evidence.SubmissionPageID)}))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := h.objects.Get(r.Context(), asset.StorageBucket, asset.StorageKey)
	if err != nil {
		httpx.Error(w, r, http.StatusBadGateway, "object_storage_failed", "failed to read paper page image")
		return
	}
	defer body.Close()
	h.auditAction(r, "student.paper_page_viewed", "submission_page", evidence.SubmissionPageID, "view published paper page")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
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

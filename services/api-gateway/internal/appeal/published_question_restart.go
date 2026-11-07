package appeal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

var ErrRestartReviewUnavailable = errors.New("question appeal review cannot be restarted from this source")
var ErrRestartReviewSharedJob = errors.New("question appeal regrade job is shared with other submissions or appeals")

type RestartQuestionAppealReviewInput struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}

// 来源由正式版本读取并写入只追加事件，不能从客户端提供的成绩或来源编号建立新一轮复核。
type restartReviewSource struct {
	ReleaseID    string  `json:"review_source_release_id"`
	FinalGradeID string  `json:"review_source_final_grade_id"`
	Score        float64 `json:"review_source_score"`
	MaxScore     float64 `json:"review_source_max_score"`
}

func effectiveReviewSourceID(item PublishedQuestionAppeal) string {
	if item.ReviewSourceReleaseID != "" {
		return item.ReviewSourceReleaseID
	}
	return item.SourceReleaseID
}

func effectiveReviewFinalGradeID(item PublishedQuestionAppeal) string {
	if item.ReviewSourceFinalGradeID != "" {
		return item.ReviewSourceFinalGradeID
	}
	return item.SourceFinalGradeID
}

func applyRestartReviewSource(item *PublishedQuestionAppeal, source restartReviewSource) {
	item.ReviewSourceReleaseID, item.ReviewSourceFinalGradeID = source.ReleaseID, source.FinalGradeID
	item.ReviewSourceScore, item.ReviewSourceMaxScore = &source.Score, &source.MaxScore
}

func (s *PublishedQuestionAppealService) RestartReview(ctx context.Context, tenantID, appealID, actorID string, input RestartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if !validPublishedQuestionAppealActor(tenantID, appealID, actorID) || input.ExpectedRevision <= 0 || input.Reason == "" || utf8.RuneCountInString(input.Reason) > 1000 {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	store, ok := s.store.(interface {
		RestartPublishedQuestionAppealReview(context.Context, string, string, string, RestartQuestionAppealReviewInput) (PublishedQuestionAppeal, error)
	})
	if !ok {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	return store.RestartPublishedQuestionAppealReview(ctx, tenantID, appealID, actorID, input)
}

func (h *PublishedQuestionAppealHandler) RestartReview(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !hasPermission(user, appealManagePermission) {
		writePublishedQuestionAppealError(w, r, ErrForbidden)
		return
	}
	if _, err := h.authorizedStaffAppeal(r); err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	var input RestartQuestionAppealReviewInput
	if !decodePublishedQuestionAppealJSON(w, r, &input) {
		return
	}
	item, err := h.service.RestartReview(r.Context(), user.TenantID, r.PathValue("id"), user.ID, input)
	if err != nil {
		writePublishedQuestionAppealError(w, r, err)
		return
	}
	h.auditAction(r, "question_appeal.review_restarted", "question_appeal", item.ID, input.Reason)
	httpx.JSON(w, http.StatusOK, map[string]any{"appeal": h.withStaffExamSummary(r, item, nil)})
}

func (s *PublishedQuestionAppealMemoryStore) RestartPublishedQuestionAppealReview(_ context.Context, tenantID, appealID, actorID string, input RestartQuestionAppealReviewInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return PublishedQuestionAppeal{}, ErrRevisionConflict
	}
	job, exists := s.regradeJobs[item.RegradeJobID]
	if item.Status != QuestionAppealUpheldPendingRegrade || !exists || job.Status == "cancelled" {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	if len(job.Items) != 1 {
		return PublishedQuestionAppeal{}, ErrRestartReviewSharedJob
	}
	for _, other := range s.appeals {
		if other.TenantID == tenantID && other.ID != item.ID && other.RegradeJobID == item.RegradeJobID {
			return PublishedQuestionAppeal{}, ErrRestartReviewSharedJob
		}
	}
	var source PublishedQuestionAppealSource
	for _, candidate := range s.sources {
		if candidate.TenantID == tenantID && candidate.ExamID == item.ExamID && candidate.StudentID == item.StudentID && candidate.SubmissionID == item.SubmissionID && candidate.QuestionID == item.QuestionID && candidate.Published && candidate.ReleaseVersion > source.ReleaseVersion {
			source = candidate
		}
	}
	if source.ReleaseID == "" || source.ReleaseID == job.SourceReleaseID {
		return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
	}
	for _, release := range s.publishedReleases {
		if s.resolutionReleaseMatchesLocked(item, release.ID) {
			return PublishedQuestionAppeal{}, ErrRestartReviewUnavailable
		}
	}
	previousJob := item.RegradeJobID
	job.Status = "cancelled"
	for id, entry := range job.Items {
		entry.Status = "cancelled"
		job.Items[id] = entry
	}
	s.regradeJobs[previousJob] = job
	item.Status, item.RegradeJobID, item.Decision, item.PublicResponse, item.PrivateNote, item.DecidedBy = QuestionAppealUnderReview, "", "", "", "", ""
	item.ReviewOpinion, item.DecidedAt = nil, nil
	item.Revision, item.UpdatedAt = item.Revision+1, s.now().UTC()
	applyRestartReviewSource(&item, restartReviewSource{ReleaseID: source.ReleaseID, FinalGradeID: source.FinalGradeID, Score: source.Score, MaxScore: source.MaxScore})
	s.appeals[appealID] = item
	s.appendEventLocked(appealID, actorID, "review_restarted", map[string]any{"revision": item.Revision, "previous_regrade_job_id": previousJob, "review_source_release_id": source.ReleaseID, "review_source_final_grade_id": source.FinalGradeID, "review_source_score": source.Score, "review_source_max_score": source.MaxScore, "reason": input.Reason})
	return clonePublishedQuestionAppeal(item), nil
}

package appeal

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	QuestionAppealOpinionSupport       = "support_adjustment"
	QuestionAppealOpinionDoNotSupport  = "do_not_support"
	QuestionAppealOpinionNeedsEvidence = "needs_evidence"
)

func (s *PublishedQuestionAppealService) SubmitReviewOpinion(ctx context.Context, tenantID, appealID, actorID string, input SubmitQuestionAppealReviewOpinionInput) (PublishedQuestionAppeal, error) {
	input.Opinion, input.Explanation = strings.TrimSpace(input.Opinion), strings.TrimSpace(input.Explanation)
	if !validPublishedQuestionAppealActor(tenantID, appealID, actorID) || input.ExpectedRevision <= 0 || input.Explanation == "" || utf8.RuneCountInString(input.Explanation) > 4000 {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	switch input.Opinion {
	case QuestionAppealOpinionSupport, QuestionAppealOpinionDoNotSupport, QuestionAppealOpinionNeedsEvidence:
	default:
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	if input.SuggestedScore != nil && (math.IsNaN(*input.SuggestedScore) || math.IsInf(*input.SuggestedScore, 0) || *input.SuggestedScore < 0) {
		return PublishedQuestionAppeal{}, ErrInvalidInput
	}
	return s.store.SubmitPublishedQuestionAppealReviewOpinion(ctx, strings.TrimSpace(tenantID), strings.TrimSpace(appealID), strings.TrimSpace(actorID), input)
}

// 受派教师只能提交私有意见；记录仍等待管理员裁决，建议分值从不写入任何评分或重评表。
func validateQuestionAppealReviewOpinion(item PublishedQuestionAppeal, actorID string, input SubmitQuestionAppealReviewOpinionInput) error {
	if item.AssignedTo != actorID {
		return ErrForbidden
	}
	if item.Revision != input.ExpectedRevision {
		return ErrRevisionConflict
	}
	if item.Status != QuestionAppealUnderReview {
		return ErrInvalidTransition
	}
	if input.SuggestedScore != nil && *input.SuggestedScore > item.SourceMaxScore {
		return ErrInvalidInput
	}
	return nil
}

func questionAppealReviewOpinion(input SubmitQuestionAppealReviewOpinionInput, now time.Time) *QuestionAppealReviewOpinion {
	return cloneQuestionAppealReviewOpinion(&QuestionAppealReviewOpinion{Opinion: input.Opinion, SuggestedScore: input.SuggestedScore, Explanation: input.Explanation, SubmittedAt: now})
}

func cloneQuestionAppealReviewOpinion(opinion *QuestionAppealReviewOpinion) *QuestionAppealReviewOpinion {
	if opinion == nil {
		return nil
	}
	copy := *opinion
	if copy.SuggestedScore != nil {
		value := *copy.SuggestedScore
		copy.SuggestedScore = &value
	}
	return &copy
}

func questionAppealOpinionPayload(opinion *QuestionAppealReviewOpinion, revision int64) map[string]any {
	payload := map[string]any{"opinion": opinion.Opinion, "explanation": opinion.Explanation, "submitted_at": opinion.SubmittedAt.Format(time.RFC3339Nano), "revision": revision}
	if opinion.SuggestedScore != nil {
		payload["suggested_score"] = *opinion.SuggestedScore
	}
	return payload
}

func (s *PublishedQuestionAppealMemoryStore) SubmitPublishedQuestionAppealReviewOpinion(_ context.Context, tenantID, appealID, actorID string, input SubmitQuestionAppealReviewOpinionInput) (PublishedQuestionAppeal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[appealID]
	if !ok || item.TenantID != tenantID {
		return PublishedQuestionAppeal{}, ErrNotFound
	}
	if err := validateQuestionAppealReviewOpinion(item, actorID, input); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	for _, reviewerID := range s.sourceReviewers[tenantID+"\x00"+item.SourceFinalGradeID] {
		if reviewerID == actorID {
			return PublishedQuestionAppeal{}, ErrForbidden
		}
	}
	source, exists := s.sources[publishedQuestionSourceKey(tenantID, item.ExamID, item.StudentID, item.SourceReleaseID, item.QuestionID)]
	if !exists || !source.Published {
		return PublishedQuestionAppeal{}, ErrSourceRelease
	}
	for _, release := range s.publishedReleases {
		if release.TenantID == tenantID && release.ExamID == item.ExamID && release.Version > item.SourceReleaseVersion {
			return PublishedQuestionAppeal{}, ErrSourceRelease
		}
	}
	item.UpdatedAt, item.Revision = s.now().UTC(), item.Revision+1
	item.ReviewOpinion = questionAppealReviewOpinion(input, item.UpdatedAt)
	s.appeals[appealID] = item
	s.appendEventLocked(appealID, actorID, "review_opinion_submitted", questionAppealOpinionPayload(item.ReviewOpinion, item.Revision))
	return clonePublishedQuestionAppeal(item), nil
}

func (s *PublishedQuestionAppealPostgresStore) SubmitPublishedQuestionAppealReviewOpinion(ctx context.Context, tenantID, appealID, actorID string, input SubmitQuestionAppealReviewOpinionInput) (PublishedQuestionAppeal, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := publishedQuestionAppealForUpdate(ctx, tx, tenantID, appealID)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := validateQuestionAppealReviewOpinion(item, actorID, input); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if ok, err := appealWorkerExists(ctx, tx, tenantID, actorID); err != nil {
		return PublishedQuestionAppeal{}, err
	} else if !ok {
		return PublishedQuestionAppeal{}, ErrForbidden
	}
	if participated, err := publishedQuestionSourceReviewerParticipated(ctx, tx, tenantID, item.SourceFinalGradeID, actorID); err != nil {
		return PublishedQuestionAppeal{}, err
	} else if participated {
		return PublishedQuestionAppeal{}, ErrForbidden
	}
	// 锁住当前发布指针直到意见与事件一起提交；并发发布不能在核对后切换版本，再把旧意见记为当前依据。
	var currentReleaseID string
	err = tx.QueryRowContext(ctx, `
SELECT current_release.release_id::text
FROM score_release_current current_release
JOIN score_release release ON release.tenant_id=current_release.tenant_id AND release.id=current_release.release_id
WHERE current_release.tenant_id=$1 AND current_release.exam_id=$2::uuid AND release.status='published'
FOR SHARE OF current_release`, tenantID, item.ExamID).Scan(&currentReleaseID)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && currentReleaseID != item.SourceReleaseID) {
		return PublishedQuestionAppeal{}, ErrSourceRelease
	}
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	err = tx.QueryRowContext(ctx, `UPDATE question_appeal SET updated_at=now(), revision=revision+1
WHERE tenant_id=$1 AND id=$2::uuid AND revision=$3 RETURNING updated_at, revision`, tenantID, appealID, input.ExpectedRevision).Scan(&item.UpdatedAt, &item.Revision)
	if err != nil {
		return PublishedQuestionAppeal{}, err
	}
	item.ReviewOpinion = questionAppealReviewOpinion(input, item.UpdatedAt)
	// 私有意见持久化在既有审计事件；投影按修订取最近意见，学生公开模型不包含此字段。
	if err := insertPublishedQuestionAppealEvent(ctx, tx, tenantID, appealID, actorID, "review_opinion_submitted", questionAppealOpinionPayload(item.ReviewOpinion, item.Revision)); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishedQuestionAppeal{}, err
	}
	return item, nil
}

package appeal

import (
	"context"
	"strings"
	"time"
)

func (s *MemoryStore) AssignAppeal(_ context.Context, tenantID string, id string, _ string, input AssignAppealInput) (Appeal, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[id]
	if !ok || item.TenantID != tenantID {
		return Appeal{}, ErrNotFound
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	if item.FinalGradeID != "" && reviewerParticipated(s.finals[item.FinalGradeID], input.AssignedTo) {
		return Appeal{}, ErrForbidden
	}
	item.AssignedTo = input.AssignedTo
	item.Status = "under_review"
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	s.appeals[id] = item
	return s.withDetailsLocked(item), nil
}

// SubmitRecommendation 只保存受派教师的建议，实际分数仍由后续裁决决定。
func (s *MemoryStore) SubmitRecommendation(_ context.Context, tenantID string, id string, actorID string, input SubmitRecommendationInput) (Appeal, error) {
	input = normalizeRecommendationInput(input)
	if err := validateRecommendationInput(input); err != nil {
		return Appeal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[id]
	if !ok || item.TenantID != tenantID {
		return Appeal{}, ErrNotFound
	}
	if item.AssignedTo != actorID {
		return Appeal{}, ErrForbidden
	}
	if isTerminalStatus(item.Status) {
		return Appeal{}, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	if input.RecommendedScore != nil {
		final, ok := s.finals[item.FinalGradeID]
		if !ok || *input.RecommendedScore > final.MaxScore {
			return Appeal{}, ErrInvalidInput
		}
	}
	now := time.Now().UTC()
	item.Status = "under_review"
	item.Recommendation = input.Recommendation
	item.RecommendationReason = input.Reason
	item.RecommendedScore = cloneFloat(input.RecommendedScore)
	item.RecommendationBy = actorID
	item.RecommendationAt = &now
	item.Revision++
	item.UpdatedAt = now
	s.appeals[id] = item
	return s.withDetailsLocked(item), nil
}

// ReviewAppeal 沿用旧版可变成绩流程；终态与 revision 检查和所有调分写入使用同一把锁。
func (s *MemoryStore) ReviewAppeal(_ context.Context, tenantID string, id string, actorID string, input ReviewAppealInput) (Appeal, *ScoreAdjustment, error) {
	input = normalizeReviewInput(input)
	if err := validateReviewInput(input); err != nil {
		return Appeal{}, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[id]
	if !ok || item.TenantID != tenantID {
		return Appeal{}, nil, ErrNotFound
	}
	if item.Status == "closed" {
		return Appeal{}, nil, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, nil, ErrRevisionConflict
	}
	if !canReviewTransition(item.Status, input.Status) {
		return Appeal{}, nil, ErrInvalidTransition
	}
	now := time.Now().UTC()
	item.Status = input.Status
	item.ResultReason = input.Reason
	if input.AssignedTo != "" {
		item.AssignedTo = input.AssignedTo
	}
	item.ReviewedBy = actorID
	item.ReviewedAt = &now
	item.Revision++
	item.UpdatedAt = now
	var adjustment *ScoreAdjustment
	if input.Status == "score_adjusted" {
		var err error
		adjustment, err = s.adjustReviewedScoreLocked(&item, tenantID, actorID, input, now)
		if err != nil {
			return Appeal{}, nil, err
		}
	}
	s.appeals[id] = item
	out := s.withDetailsLocked(item)
	return out, adjustment, nil
}

// 调用方持有 ReviewAppeal 的写锁；全部调分校验通过后才写成绩和历史，不重复加锁。
func (s *MemoryStore) adjustReviewedScoreLocked(item *Appeal, tenantID string, actorID string, input ReviewAppealInput, now time.Time) (*ScoreAdjustment, error) {
	finalID := input.FinalGradeID
	if finalID == "" {
		finalID = item.FinalGradeID
	}
	if finalID == "" || input.AdjustedScore == nil {
		return nil, ErrInvalidInput
	}
	final, ok := s.finals[finalID]
	if !ok || final.ExamID != item.ExamID || final.SubmissionID != item.SubmissionID {
		return nil, ErrInvalidInput
	}
	if *input.AdjustedScore < 0 || *input.AdjustedScore > final.MaxScore {
		return nil, ErrInvalidInput
	}
	previous := final.Score
	final.Score = *input.AdjustedScore
	final.Locked = true
	final.Status = "locked"
	s.finals[finalID] = final
	submission := s.submissions[key(item.ExamID, item.StudentID)]
	submission.TotalScore += final.Score - previous
	submission.Locked = true
	submission.Status = "published"
	s.submissions[key(item.ExamID, item.StudentID)] = submission
	created := ScoreAdjustment{
		ID:                s.id("score-adjustment"),
		TenantID:          tenantID,
		AppealID:          item.ID,
		ExamID:            item.ExamID,
		SubmissionID:      item.SubmissionID,
		SubmissionGradeID: item.SubmissionGradeID,
		FinalGradeID:      finalID,
		QuestionID:        final.QuestionID,
		QuestionNo:        final.QuestionNo,
		PreviousScore:     previous,
		AdjustedScore:     final.Score,
		Delta:             final.Score - previous,
		Reason:            input.Reason,
		AdjustedBy:        actorID,
		CreatedAt:         now,
	}
	s.adjustments[item.ID] = append(s.adjustments[item.ID], created)
	item.FinalGradeID = finalID
	item.QuestionID = final.QuestionID
	item.QuestionNo = final.QuestionNo
	return &created, nil
}

func (s *MemoryStore) CloseAppeal(_ context.Context, tenantID string, id string, actorID string, input CloseAppealInput) (Appeal, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return Appeal{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.appeals[id]
	if !ok || item.TenantID != tenantID {
		return Appeal{}, ErrNotFound
	}
	if item.Status == "closed" {
		return Appeal{}, ErrInvalidTransition
	}
	if item.Revision != input.ExpectedRevision {
		return Appeal{}, ErrRevisionConflict
	}
	now := time.Now().UTC()
	item.Status = "closed"
	item.ResultReason = input.Reason
	item.ClosedBy = actorID
	item.ClosedAt = &now
	item.Revision++
	item.UpdatedAt = now
	s.appeals[id] = item
	return s.withDetailsLocked(item), nil
}

func reviewerParticipated(final FinalGradeSeed, userID string) bool {
	for _, grade := range final.HumanGrades {
		if reviewerID, ok := grade["reviewer_id"].(string); ok && reviewerID == userID {
			return true
		}
	}
	return false
}

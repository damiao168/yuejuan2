package appeal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type SubmissionGradeSeed struct {
	ID            string
	ExamID        string
	ExamName      string
	Subject       string
	SubmissionID  string
	StudentID     string
	AnonymousCode string
	TotalScore    float64
	MaxScore      float64
	Status        string
	Locked        bool
}

type FinalGradeSeed struct {
	ID              string
	ExamID          string
	SubmissionID    string
	AnswerSegmentID string
	QuestionID      string
	QuestionNo      string
	Score           float64
	MaxScore        float64
	Status          string
	Locked          bool
	RawAnswer       string
	OCRText         string
	AIGrades        []map[string]any
	HumanGrades     []map[string]any
	Rubric          map[string]any
}

type MemoryStore struct {
	mu          sync.RWMutex
	next        int
	submissions map[string]SubmissionGradeSeed
	finals      map[string]FinalGradeSeed
	appeals     map[string]Appeal
	adjustments map[string][]ScoreAdjustment
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:        1,
		submissions: map[string]SubmissionGradeSeed{},
		finals:      map[string]FinalGradeSeed{},
		appeals:     map[string]Appeal{},
		adjustments: map[string][]ScoreAdjustment{},
	}
}

func (s *MemoryStore) AddSubmissionGrade(seed SubmissionGradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed.ID == "" {
		seed.ID = s.id("submission-grade")
	}
	s.submissions[key(seed.ExamID, seed.StudentID)] = seed
}

func (s *MemoryStore) AddFinalGrade(seed FinalGradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed.ID == "" {
		seed.ID = s.id("final-grade")
	}
	s.finals[seed.ID] = seed
}

func (s *MemoryStore) CreateAppeal(_ context.Context, tenantID string, actorID string, input CreateAppealInput) (Appeal, error) {
	input = normalizeCreateInput(input)
	if err := validateCreateInput(input); err != nil {
		return Appeal{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	submission, ok := s.submissions[key(input.ExamID, input.StudentID)]
	if !ok || submission.Status != "published" || !submission.Locked {
		return Appeal{}, ErrUnpublishedGrade
	}
	var final FinalGradeSeed
	if input.TargetType != "exam" {
		var finalOK bool
		final, finalOK = s.finals[input.FinalGradeID]
		if !finalOK || final.ExamID != input.ExamID || final.SubmissionID != submission.SubmissionID {
			return Appeal{}, ErrInvalidInput
		}
	}
	now := time.Now().UTC()
	item := Appeal{
		ID:                s.id("appeal"),
		TenantID:          tenantID,
		ExamID:            input.ExamID,
		ExamName:          submission.ExamName,
		Subject:           submission.Subject,
		SubmissionID:      submission.SubmissionID,
		SubmissionGradeID: submission.ID,
		AnonymousCode:     submission.AnonymousCode,
		StudentID:         input.StudentID,
		TargetType:        input.TargetType,
		FinalGradeID:      input.FinalGradeID,
		QuestionID:        final.QuestionID,
		QuestionNo:        final.QuestionNo,
		DeductionPointID:  input.DeductionPointID,
		Reason:            input.Reason,
		Attachment:        cloneMap(input.Attachment),
		Status:            "submitted",
		Revision:          1,
		CreatedBy:         actorID,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	s.appeals[item.ID] = item
	return s.withDetailsLocked(item), nil
}

func (s *MemoryStore) ListAppeals(_ context.Context, tenantID string, filter ListFilter) ([]Appeal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Appeal{}
	for _, item := range s.appeals {
		if item.TenantID != tenantID {
			continue
		}
		if filter.ExamID != "" && item.ExamID != filter.ExamID {
			continue
		}
		if filter.StudentID != "" && item.StudentID != filter.StudentID {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if filter.AssignedTo != "" && item.AssignedTo != filter.AssignedTo {
			continue
		}
		out = append(out, cloneAppeal(item))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) ||
				(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

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

func (s *MemoryStore) GetAppeal(_ context.Context, tenantID string, id string) (Appeal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.appeals[id]
	if !ok || item.TenantID != tenantID {
		return Appeal{}, ErrNotFound
	}
	return s.withDetailsLocked(item), nil
}

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
		finalID := input.FinalGradeID
		if finalID == "" {
			finalID = item.FinalGradeID
		}
		if finalID == "" || input.AdjustedScore == nil {
			return Appeal{}, nil, ErrInvalidInput
		}
		final, ok := s.finals[finalID]
		if !ok || final.ExamID != item.ExamID || final.SubmissionID != item.SubmissionID {
			return Appeal{}, nil, ErrInvalidInput
		}
		if *input.AdjustedScore < 0 || *input.AdjustedScore > final.MaxScore {
			return Appeal{}, nil, ErrInvalidInput
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
		adjustment = &created
		item.FinalGradeID = finalID
		item.QuestionID = final.QuestionID
		item.QuestionNo = final.QuestionNo
	}
	s.appeals[id] = item
	out := s.withDetailsLocked(item)
	return out, adjustment, nil
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

func (s *MemoryStore) Statistics(_ context.Context, tenantID string, filter StatisticsFilter) (AppealStatistics, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := AppealStatistics{ByStatus: map[string]int{}}
	var totalHours float64
	var handled int
	for _, item := range s.appeals {
		if item.TenantID != tenantID {
			continue
		}
		if filter.ExamID != "" && item.ExamID != filter.ExamID {
			continue
		}
		out.Total++
		out.ByStatus[item.Status]++
		if len(s.adjustments[item.ID]) > 0 {
			out.ScoreAdjustedCount++
		}
		if item.ReviewedAt != nil {
			totalHours += item.ReviewedAt.Sub(item.CreatedAt).Hours()
			handled++
		}
	}
	if handled > 0 {
		out.AverageHandleHours = totalHours / float64(handled)
	}
	return out, nil
}

func (s *MemoryStore) withDetailsLocked(item Appeal) Appeal {
	out := cloneAppeal(item)
	if item.FinalGradeID != "" {
		final := s.finals[item.FinalGradeID]
		out.Evidence = &AppealEvidence{
			RawAnswer:   final.RawAnswer,
			OCRText:     final.OCRText,
			AIGrades:    cloneMaps(final.AIGrades),
			HumanGrades: cloneMaps(final.HumanGrades),
			Rubric:      cloneMap(final.Rubric),
			FinalGrade: map[string]any{
				"id":          final.ID,
				"score":       final.Score,
				"max_score":   final.MaxScore,
				"status":      final.Status,
				"locked":      final.Locked,
				"question_no": final.QuestionNo,
			},
		}
	}
	out.Adjustments = append([]ScoreAdjustment(nil), s.adjustments[item.ID]...)
	return out
}

func normalizeCreateInput(input CreateAppealInput) CreateAppealInput {
	input.ExamID = strings.TrimSpace(input.ExamID)
	input.StudentID = strings.TrimSpace(input.StudentID)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.FinalGradeID = strings.TrimSpace(input.FinalGradeID)
	input.DeductionPointID = strings.TrimSpace(input.DeductionPointID)
	input.Reason = strings.TrimSpace(input.Reason)
	return input
}

func validateCreateInput(input CreateAppealInput) error {
	if input.ExamID == "" || input.StudentID == "" || input.Reason == "" || !validTargetType(input.TargetType) {
		return ErrInvalidInput
	}
	if input.TargetType != "exam" && input.FinalGradeID == "" {
		return ErrInvalidInput
	}
	if input.TargetType == "deduction_point" && input.DeductionPointID == "" {
		return ErrInvalidInput
	}
	return nil
}

func normalizeReviewInput(input ReviewAppealInput) ReviewAppealInput {
	input.Status = strings.TrimSpace(input.Status)
	input.Reason = strings.TrimSpace(input.Reason)
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	input.FinalGradeID = strings.TrimSpace(input.FinalGradeID)
	return input
}

func normalizeRecommendationInput(input SubmitRecommendationInput) SubmitRecommendationInput {
	input.Recommendation = strings.TrimSpace(input.Recommendation)
	input.Reason = strings.TrimSpace(input.Reason)
	return input
}

func validateRecommendationInput(input SubmitRecommendationInput) error {
	switch input.Recommendation {
	case "accept", "reject", "need_more_info":
		if input.RecommendedScore != nil {
			return ErrInvalidInput
		}
	case "adjust_score":
		if input.RecommendedScore == nil || *input.RecommendedScore < 0 {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	if input.Reason == "" {
		return ErrInvalidInput
	}
	return nil
}

func validateReviewInput(input ReviewAppealInput) error {
	if !validStatus(input.Status) || input.Status == "submitted" || input.Status == "closed" {
		return ErrInvalidInput
	}
	if input.Status != "under_review" && input.Reason == "" {
		return ErrInvalidInput
	}
	if input.Status == "score_adjusted" && input.AdjustedScore == nil {
		return ErrInvalidInput
	}
	return nil
}

func canReviewTransition(current string, next string) bool {
	switch current {
	case "submitted":
		return next == "under_review" || next == "need_more_info" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	case "under_review":
		return next == "under_review" || next == "need_more_info" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	case "need_more_info":
		return next == "need_more_info" || next == "under_review" || next == "accepted" || next == "rejected" || next == "score_adjusted"
	default:
		return false
	}
}

func isTerminalStatus(status string) bool {
	return status == "accepted" || status == "rejected" || status == "score_adjusted" || status == "closed"
}

func reviewerParticipated(final FinalGradeSeed, userID string) bool {
	for _, grade := range final.HumanGrades {
		if reviewerID, ok := grade["reviewer_id"].(string); ok && reviewerID == userID {
			return true
		}
	}
	return false
}

func validTargetType(value string) bool {
	switch value {
	case "exam", "question", "deduction_point":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	for _, status := range Statuses() {
		if value == status {
			return true
		}
	}
	return false
}

func cloneAppeal(in Appeal) Appeal {
	in.Attachment = cloneMap(in.Attachment)
	in.ReviewedAt = cloneTime(in.ReviewedAt)
	in.RecommendationAt = cloneTime(in.RecommendationAt)
	in.RecommendedScore = cloneFloat(in.RecommendedScore)
	in.ClosedAt = cloneTime(in.ClosedAt)
	in.Adjustments = append([]ScoreAdjustment(nil), in.Adjustments...)
	if in.Evidence != nil {
		ev := *in.Evidence
		ev.AIGrades = cloneMaps(ev.AIGrades)
		ev.HumanGrades = cloneMaps(ev.HumanGrades)
		ev.Rubric = cloneMap(ev.Rubric)
		ev.FinalGrade = cloneMap(ev.FinalGrade)
		in.Evidence = &ev
	}
	return in
}

func cloneFloat(in *float64) *float64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneMaps(in []map[string]any) []map[string]any {
	out := make([]map[string]any, len(in))
	for i := range in {
		out[i] = cloneMap(in[i])
	}
	return out
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func key(left string, right string) string {
	return left + "|" + right
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

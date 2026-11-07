package appeal

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"fmt"
	"sort"
	"sync"
	"time"
)

type SubmissionGradeSeed struct {
	ID            string
	SchoolID      string
	ClassIDs      []string
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

// ListAppeals 在同一读锁内按租户与调用方收窄的条件取列表，再按严格游标分页。
func (s *MemoryStore) ListAppeals(_ context.Context, tenantID string, filter ListFilter) ([]Appeal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Appeal{}
	for _, item := range s.appeals {
		if !appealMatchesListFilter(item, tenantID, filter) || !s.appealWithinAccessScope(item, filter.AccessScope) {
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
			if appealFollowsListCursor(item, filter) {
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

func (s *MemoryStore) appealWithinAccessScope(item Appeal, scope *auth.AccessScope) bool {
	if scope == nil {
		return true
	}
	if scope.TenantID != item.TenantID {
		return false
	}
	// 受派人员仍需通过 handler 的本人受派筛选，不能借管理权限扩大组织边界。
	if scope.AssignedOnly && scope.ActorID != "" && item.AssignedTo == scope.ActorID {
		return true
	}
	seed := s.submissions[key(item.ExamID, item.StudentID)]
	return auth.AllowsResourceBoundary(*scope, auth.ResourceBoundary{
		ResourceType: "appeal", ResourceID: item.ID, TenantID: item.TenantID,
		SchoolID: seed.SchoolID, ClassIDs: seed.ClassIDs, ExamID: item.ExamID,
		StudentID: item.StudentID, SubmissionID: item.SubmissionID,
	})
}

func appealMatchesListFilter(item Appeal, tenantID string, filter ListFilter) bool {
	if item.TenantID != tenantID {
		return false
	}
	if filter.ExamID != "" && item.ExamID != filter.ExamID {
		return false
	}
	if filter.StudentID != "" && item.StudentID != filter.StudentID {
		return false
	}
	if filter.Status != "" && item.Status != filter.Status {
		return false
	}
	if filter.AssignedTo != "" && item.AssignedTo != filter.AssignedTo {
		return false
	}
	return true
}

func appealFollowsListCursor(item Appeal, filter ListFilter) bool {
	// 列表按时间和 ID 降序；相同时间也必须严格越过游标 ID，不能重复上一页末项。
	return item.CreatedAt.Before(filter.CursorCreatedAt) ||
		(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID)
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
		if !s.appealWithinAccessScope(item, filter.AccessScope) {
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

func key(left string, right string) string {
	return left + "|" + right
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

package segment

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu       sync.RWMutex
	next     int
	segments map[string]Segment
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, segments: map[string]Segment{}}
}

func (s *MemoryStore) CreateSegments(_ context.Context, inputs []CreateSegmentInput) ([]Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Segment{}
	for _, input := range inputs {
		if err := ValidateBBox(input.BBox); err != nil {
			return nil, err
		}
		if existing, ok := s.findExistingLocked(input.TenantID, input.SubmissionID, input.QuestionID); ok {
			// 以租户、提交物和题目作为幂等键，重复生成只返回原片段，不覆盖人工修改。
			out = append(out, existing)
			continue
		}
		segment := Segment{
			ID:               s.id("segment"),
			TenantID:         input.TenantID,
			SubmissionID:     input.SubmissionID,
			SubmissionPageID: input.SubmissionPageID,
			QuestionID:       input.QuestionID,
			QuestionNo:       input.QuestionNo,
			BBox:             append([]float64{}, input.BBox...),
			Source:           input.Source,
			Status:           input.Status,
			CreatedAt:        time.Now().UTC(),
		}
		s.segments[segment.ID] = segment
		out = append(out, segment)
	}
	sortSegments(out)
	return out, nil
}

func (s *MemoryStore) ListBySubmission(_ context.Context, tenantID string, submissionID string) ([]Segment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Segment{}
	for _, item := range s.segments {
		if item.TenantID == tenantID && item.SubmissionID == submissionID {
			out = append(out, item)
		}
	}
	sortSegments(out)
	return out, nil
}

func (s *MemoryStore) Update(_ context.Context, tenantID string, id string, actorID string, input UpdateSegmentInput) (Segment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.segments[id]
	if !ok || item.TenantID != tenantID {
		return Segment{}, ErrNotFound
	}
	if input.BBox != nil {
		if err := ValidateBBox(*input.BBox); err != nil {
			return Segment{}, err
		}
		item.BBox = append([]float64{}, (*input.BBox)...)
		item.Source = "manual"
		// 只要人工调整坐标，就标记来源为 manual，避免后续自动生成误认为仍是模板坐标。
	}
	if input.Status != nil {
		if !IsValidStatus(*input.Status) {
			return Segment{}, ErrInvalidInput
		}
		item.Status = *input.Status
	}
	if input.ReviewNotes != nil {
		item.ReviewNotes = *input.ReviewNotes
	}
	now := time.Now().UTC()
	item.ReviewedAt = &now
	item.ReviewedBy = actorID
	s.segments[id] = item
	return item, nil
}

func (s *MemoryStore) GetEvidence(context.Context, string, string) (SegmentEvidence, error) {
	return SegmentEvidence{}, ErrNotFound
}

func (s *MemoryStore) GetEvidenceForQuestion(context.Context, string, string, string) (SegmentEvidence, error) {
	return SegmentEvidence{}, ErrNotFound
}

func (s *MemoryStore) findExistingLocked(tenantID string, submissionID string, questionID string) (Segment, bool) {
	for _, item := range s.segments {
		if item.TenantID == tenantID && item.SubmissionID == submissionID && item.QuestionID == questionID {
			return item, true
		}
	}
	return Segment{}, false
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func sortSegments(items []Segment) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].QuestionNo == items[j].QuestionNo {
			return items[i].ID < items[j].ID
		}
		return items[i].QuestionNo < items[j].QuestionNo
	})
}

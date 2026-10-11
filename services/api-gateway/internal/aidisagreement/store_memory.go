package aidisagreement

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore 用于测试夹具。生产采集始终从 PostgreSQL 读取来源事实；测试直接登记比较结果，不通过公开 HTTP 端点接收任意分数。
type MemoryStore struct {
	mu          sync.RWMutex
	comparisons map[string]ActualComparison
	items       map[string]Disagreement
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{comparisons: map[string]ActualComparison{}, items: map[string]Disagreement{}}
}

func (s *MemoryStore) AddActualComparison(tenantID string, value ActualComparison) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comparisons[sourceKey(tenantID, value.AICandidateID, value.HumanGradeID)] = value
}

func (s *MemoryStore) LoadActualComparison(_ context.Context, tenantID string, input CaptureInput) (ActualComparison, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.comparisons[sourceKey(tenantID, input.AICandidateID, input.HumanGradeID)]
	if !ok {
		return ActualComparison{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStore) Upsert(_ context.Context, tenantID string, value Disagreement) (Disagreement, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sourceKey(tenantID, value.AICandidateID, value.HumanGradeID)
	if existing, ok := s.items[key]; ok {
		return clone(existing), false, nil
	}
	value.TenantID = tenantID
	s.items[key] = clone(value)
	return clone(value), true, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, id string) (Disagreement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, item := range s.items {
		if item.TenantID == tenantID && item.ID == id {
			return clone(item), nil
		}
	}
	return Disagreement{}, ErrNotFound
}

func (s *MemoryStore) List(_ context.Context, tenantID string, filter Filter) ([]Disagreement, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]Disagreement, 0)
	for _, item := range s.items {
		if item.TenantID != tenantID || (filter.ExamID != "" && item.ExamID != filter.ExamID) ||
			(filter.QuestionID != "" && item.QuestionID != filter.QuestionID) ||
			(filter.Status != "" && item.Status != filter.Status) || (filter.Severity != "" && item.Severity != filter.Severity) {
			continue
		}
		items = append(items, clone(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Severity != items[j].Severity {
			return items[i].Severity == SeveritySevere
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if len(items) > filter.Limit && filter.Limit > 0 {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) Classify(_ context.Context, tenantID, id, reviewerID string, input ClassifyInput, at time.Time) (Disagreement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, item, ok := s.findLocked(tenantID, id)
	if !ok {
		return Disagreement{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return Disagreement{}, ErrConflict
	}
	if item.Status != StatusNeedsReview && item.Status != StatusClassified {
		return Disagreement{}, ErrInvalidState
	}
	item.Status, item.Taxonomy, item.Notes, item.ReviewerID = StatusClassified, input.Taxonomy, input.Notes, reviewerID
	item.ReviewedAt, item.UpdatedAt, item.Revision = timePtr(at), at, item.Revision+1
	s.items[key] = item
	return clone(item), nil
}

func (s *MemoryStore) Route(_ context.Context, tenantID, id, _ string, input RouteInput, at time.Time) (Disagreement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, item, ok := s.findLocked(tenantID, id)
	if !ok {
		return Disagreement{}, ErrNotFound
	}
	if item.Revision != input.ExpectedRevision {
		return Disagreement{}, ErrConflict
	}
	if item.Status != StatusNeedsReview && item.Status != StatusClassified {
		return Disagreement{}, ErrInvalidState
	}
	item.Status, item.RoutedReviewTaskID, item.UpdatedAt, item.Revision = StatusRouted, input.ReviewTaskID, at, item.Revision+1
	s.items[key] = item
	return clone(item), nil
}

func (s *MemoryStore) findLocked(tenantID, id string) (string, Disagreement, bool) {
	for key, item := range s.items {
		if item.TenantID == tenantID && item.ID == id {
			return key, item, true
		}
	}
	return "", Disagreement{}, false
}

func sourceKey(tenantID, aiID, humanID string) string {
	return tenantID + "\x00" + aiID + "\x00" + humanID
}

func timePtr(value time.Time) *time.Time { copy := value; return &copy }

func clone(item Disagreement) Disagreement {
	item.TriggerRules = append([]string(nil), item.TriggerRules...)
	if item.ReviewedAt != nil {
		item.ReviewedAt = timePtr(*item.ReviewedAt)
	}
	return item
}

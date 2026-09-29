package exam

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type MemoryStore struct {
	mu                 sync.RWMutex
	sessionMu          sync.Mutex
	next               int
	items              map[string]Exam
	sessionsByCommand  map[string]ExamSession
	sessionCommandHash map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, items: map[string]Exam{}, sessionsByCommand: map[string]ExamSession{}, sessionCommandHash: map[string]string{}}
}

func (s *MemoryStore) CreateExam(_ context.Context, scope auth.AccessScope, createdBy string, input CreateInput) (Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !scopeAllowsRequestedClasses(scope, input.SchoolID, input.ClassIDs) {
		return Exam{}, ErrScopeForbidden
	}
	appealEnabled := true
	if input.AppealEnabled != nil {
		appealEnabled = *input.AppealEnabled
	}
	item := Exam{
		ID:            s.id(),
		TenantID:      scope.TenantID,
		SchoolID:      input.SchoolID,
		Name:          input.Name,
		Subject:       input.Subject,
		ExamType:      input.ExamType,
		TotalScore:    input.TotalScore,
		Status:        "draft",
		GradingMode:   input.GradingMode,
		AppealEnabled: appealEnabled,
		PublishPolicy: input.PublishPolicy,
		CreatedBy:     createdBy,
		ClassIDs:      cloneStrings(input.ClassIDs),
		Revision:      1,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	s.items[item.ID] = item
	return item, nil
}

func (s *MemoryStore) ListExams(_ context.Context, scope auth.AccessScope, filter ListFilter) ([]Exam, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Exam{}
	for _, item := range s.items {
		if item.TenantID != scope.TenantID || !scopeAllowsExam(scope, item) {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if filter.SchoolID != "" && item.SchoolID != filter.SchoolID {
			continue
		}
		if !filter.CursorAt.IsZero() && filter.CursorID != "" && (item.CreatedAt.After(filter.CursorAt) || (item.CreatedAt.Equal(filter.CursorAt) && item.ID >= filter.CursorID)) {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) GetExam(_ context.Context, scope auth.AccessScope, id string) (Exam, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok || item.TenantID != scope.TenantID || !scopeAllowsExam(scope, item) {
		return Exam{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) ListSessionExams(_ context.Context, scope auth.AccessScope, sessionID string) ([]Exam, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Exam{}
	for _, item := range s.items {
		if item.SessionID == sessionID && item.TenantID == scope.TenantID && scopeAllowsExam(scope, item) {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Subject == out[j].Subject {
			return out[i].ID < out[j].ID
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

func (s *MemoryStore) UpdateExam(_ context.Context, scope auth.AccessScope, id string, input UpdateInput) (Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok || item.TenantID != scope.TenantID || !scopeAllowsExam(scope, item) {
		return Exam{}, ErrNotFound
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Exam{}, ErrRevisionConflict
	}
	if IsCoreLocked(item.Status) {
		return Exam{}, ErrLocked
	}
	classIDsChanged := input.ClassIDs != nil && !classIDSetsEqual(item.ClassIDs, *input.ClassIDs)
	if classIDsChanged && !CanChangeCandidateRoster(item.Status) {
		return Exam{}, ErrCandidatesFrozen
	}
	if input.SchoolID != nil {
		item.SchoolID = *input.SchoolID
	}
	if input.Name != nil {
		item.Name = *input.Name
	}
	if input.Subject != nil {
		item.Subject = *input.Subject
	}
	if input.ExamType != nil {
		item.ExamType = *input.ExamType
	}
	if input.TotalScore != nil {
		item.TotalScore = *input.TotalScore
	}
	if input.GradingMode != nil {
		item.GradingMode = *input.GradingMode
	}
	if input.AppealEnabled != nil {
		item.AppealEnabled = *input.AppealEnabled
	}
	if input.PublishPolicy != nil {
		item.PublishPolicy = *input.PublishPolicy
	}
	if input.ClassIDs != nil {
		item.ClassIDs = cloneStrings(*input.ClassIDs)
	}
	if !scopeAllowsRequestedClasses(scope, item.SchoolID, item.ClassIDs) {
		return Exam{}, ErrScopeForbidden
	}
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	s.items[id] = item
	return item, nil
}

func (s *MemoryStore) UpdateStatus(_ context.Context, scope auth.AccessScope, id string, status string, expectedRevision int64) (Exam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok || item.TenantID != scope.TenantID || !scopeAllowsExam(scope, item) {
		return Exam{}, ErrNotFound
	}
	if expectedRevision <= 0 || item.Revision != expectedRevision {
		return Exam{}, ErrRevisionConflict
	}
	if !CanTransition(item.Status, status) {
		return Exam{}, ErrInvalidTransition
	}
	item.Status = status
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	s.items[id] = item
	return item, nil
}

func (s *MemoryStore) RefreshCandidateSnapshot(_ context.Context, scope auth.AccessScope, id string) (CandidateRefreshResult, error) {
	// 内存实现只模拟权限和冻结状态检查，名册重建数量由 PostgreSQL 实现提供。
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok || item.TenantID != scope.TenantID || !scopeAllowsExam(scope, item) {
		return CandidateRefreshResult{}, ErrNotFound
	}
	if item.Status != "draft" && item.Status != "configured" {
		return CandidateRefreshResult{}, ErrCandidatesFrozen
	}
	return CandidateRefreshResult{}, nil
}

func (s *MemoryStore) SetStatusForTest(tenantID string, id string, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[id]
	if !ok || item.TenantID != tenantID {
		return ErrNotFound
	}
	item.Status = status
	item.Revision++
	item.UpdatedAt = time.Now().UTC()
	s.items[id] = item
	return nil
}

func scopeAllowsExam(scope auth.AccessScope, item Exam) bool {
	if scope.TenantWide || scope.AllowsExam(item.ID) || scope.AllowsSchool(item.SchoolID) {
		return true
	}
	for _, classID := range item.ClassIDs {
		if scope.AllowsClass(classID) {
			return true
		}
	}
	return false
}

func (s *MemoryStore) id() string {
	id := fmt.Sprintf("exam-%d", s.next)
	s.next++
	return id
}

func cloneStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	return out
}

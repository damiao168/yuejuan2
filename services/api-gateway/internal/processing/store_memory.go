package processing

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"

	"github.com/google/uuid"
)

// MemoryStore is intentionally a projection-only test adapter. Production
// composes PostgresStore, which reads the actual capture/OCR/quality facts.
type MemoryStore struct {
	mu          sync.RWMutex
	states      map[string]PageState
	exceptions  map[string]Exception
	projectedAt map[string]time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{states: map[string]PageState{}, exceptions: map[string]Exception{}, projectedAt: map[string]time.Time{}}
}

func stateKey(tenantID, pageID string) string { return tenantID + ":" + pageID }

// PutState is useful to isolate service/handler tests. It does not exist in
// the Store interface because runtime production projection is source-driven.
func (s *MemoryStore) PutState(tenantID string, state PageState) {
	// 写入状态同时维护对应异常的幂等投影；同一来源更新原异常，来源变化才新建记录。
	s.mu.Lock()
	defer s.mu.Unlock()
	state = cloneState(state)
	if state.SourceObservedAt.IsZero() {
		state.SourceObservedAt = time.Now().UTC()
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = state.SourceObservedAt
	}
	s.states[stateKey(tenantID, state.PageID)] = state
	projectionKey := tenantID + ":" + state.ExamID
	if current := s.projectedAt[projectionKey]; state.UpdatedAt.After(current) {
		s.projectedAt[projectionKey] = state.UpdatedAt
	}
	if state.IssueCode == "" {
		return
	}
	for id, item := range s.exceptions {
		if item.PageID == state.PageID && item.Code == state.IssueCode && item.SourceType == retrySourceType(state) && item.SourceID == retrySourceID(state) {
			item.UpdatedAt = state.SourceObservedAt
			item.Details = stateDetails(state)
			s.exceptions[id] = item
			return
		}
	}
	now := state.SourceObservedAt
	item := Exception{ID: uuid.NewString(), ExamID: state.ExamID, PageID: state.PageID, SourceType: retrySourceType(state), SourceID: retrySourceID(state), Code: state.IssueCode, Severity: severityFor(state), Blocking: state.Blocking, Status: ExceptionOpen, Details: stateDetails(state), CreatedAt: now, UpdatedAt: now, RetrySourceType: state.RetrySourceType, RetrySourceID: state.RetrySourceID}
	s.exceptions[item.ID] = item
}

func retrySourceType(state PageState) string {
	if state.RetrySourceType != "" {
		return state.RetrySourceType
	}
	return "submission_page"
}
func retrySourceID(state PageState) string {
	if state.RetrySourceID != "" {
		return state.RetrySourceID
	}
	return state.PageID
}
func severityFor(state PageState) Severity {
	if state.Blocking {
		return SeverityP0
	}
	if state.IssueCode == IssueOCRLowConfidence {
		return SeverityP1
	}
	return SeverityP2
}
func stateDetails(state PageState) map[string]any {
	return map[string]any{"current_stage": state.CurrentStage, "retryable": state.Retryable, "retry_source_type": state.RetrySourceType, "retry_source_id": state.RetrySourceID, "source_observed_at": state.SourceObservedAt.UTC().Format(time.RFC3339Nano)}
}

func (s *MemoryStore) Summary(_ context.Context, tenantID, examID string) (Summary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := Summary{ExamID: examID, ByStage: []StageCount{}, Issues: []IssueCount{}, GeneratedAt: s.projectedAt[tenantID+":"+examID]}
	stages := map[Stage]int{}
	issues := map[IssueCode]int{}
	for key, state := range s.states {
		if len(key) < len(tenantID)+1 || key[:len(tenantID)+1] != tenantID+":" || state.ExamID != examID {
			continue
		}
		result.TotalPages++
		if state.CurrentStage == StageReady {
			result.ReadyPages++
		} else if state.Blocking {
			result.BlockedPages++
		} else {
			result.PendingPages++
		}
		stages[state.CurrentStage]++
		if state.IssueCode != "" {
			issues[state.IssueCode]++
		}
	}
	for stage, count := range stages {
		result.ByStage = append(result.ByStage, StageCount{Stage: stage, Count: count})
	}
	for code, count := range issues {
		result.Issues = append(result.Issues, IssueCount{Code: code, Count: count})
	}
	sort.Slice(result.ByStage, func(i, j int) bool { return result.ByStage[i].Stage < result.ByStage[j].Stage })
	sort.Slice(result.Issues, func(i, j int) bool { return result.Issues[i].Code < result.Issues[j].Code })
	return result, nil
}

func (s *MemoryStore) ListExceptions(_ context.Context, tenantID string, filter ExceptionFilter) (ListResult, error) {
	// 此测试实现按时间和 ID 倒序截取首页，尚未应用传入的游标或学科筛选。
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []Exception{}
	for _, item := range s.exceptions {
		state, ok := s.states[stateKey(tenantID, item.PageID)]
		if !ok || (filter.ExamID != "" && item.ExamID != filter.ExamID) || (filter.Severity != "" && item.Severity != filter.Severity) || (filter.Status != "" && item.Status != filter.Status) || (filter.Stage != "" && state.CurrentStage != filter.Stage) {
			continue
		}
		items = append(items, cloneException(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 25
	}
	result := ListResult{Exceptions: items}
	var latest time.Time
	for key, projectedAt := range s.projectedAt {
		if (filter.ExamID == "" && strings.HasPrefix(key, tenantID+":")) || key == tenantID+":"+filter.ExamID {
			if projectedAt.After(latest) {
				latest = projectedAt
			}
		}
	}
	if !latest.IsZero() {
		value := latest
		result.ProjectedAt = &value
	}
	if len(result.Exceptions) > filter.Limit {
		result.HasMore = true
		result.Exceptions = result.Exceptions[:filter.Limit]
	}
	if result.HasMore && len(result.Exceptions) > 0 {
		last := result.Exceptions[len(result.Exceptions)-1]
		result.NextCursor = last.CreatedAt.Format(time.RFC3339Nano) + "|" + last.ID
	}
	return result, nil
}

func (s *MemoryStore) GetException(_ context.Context, tenantID, exceptionID string) (Exception, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.exceptions[exceptionID]
	if !ok {
		return Exception{}, ErrNotFound
	}
	if _, ok = s.states[stateKey(tenantID, item.PageID)]; !ok {
		return Exception{}, ErrNotFound
	}
	return cloneException(item), nil
}

func (s *MemoryStore) AssignException(ctx context.Context, tenantID, exceptionID, _ string, input AssignInput) (Exception, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.exceptionForTenant(tenantID, exceptionID)
	if !ok {
		return Exception{}, ErrNotFound
	}
	if item.Status == ExceptionResolved {
		return Exception{}, ErrStateConflict
	}
	item.AssignedTo, item.Status, item.UpdatedAt = input.AssigneeID, ExceptionAssigned, time.Now().UTC()
	s.exceptions[item.ID] = item
	return cloneException(item), nil
}

func (s *MemoryStore) ResolveException(_ context.Context, tenantID, exceptionID, _ string, input ResolveInput) (Exception, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.exceptionForTenant(tenantID, exceptionID)
	if !ok {
		return Exception{}, ErrNotFound
	}
	if item.Status == ExceptionResolved {
		return cloneException(item), nil
	}
	now := time.Now().UTC()
	item.Status, item.Resolution, item.UpdatedAt, item.ResolvedAt = ExceptionResolved, input.Resolution, now, &now
	s.exceptions[item.ID] = item
	return cloneException(item), nil
}

func (s *MemoryStore) RetryTarget(_ context.Context, tenantID, exceptionID string) (RetryTarget, error) {
	// 已解决或来源不可重试的异常不能重新投递，避免把人工结案重新打开。
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.exceptionForTenant(tenantID, exceptionID)
	if !ok {
		return RetryTarget{}, ErrNotFound
	}
	if item.Status == ExceptionResolved || item.RetrySourceType == "" || item.RetrySourceID == "" || item.Details["retryable"] != true {
		return RetryTarget{}, ErrRetryForbidden
	}
	return RetryTarget{ExceptionID: item.ID, SourceType: item.RetrySourceType, SourceID: item.RetrySourceID}, nil
}

func (s *MemoryStore) ParserQualityForSegment(_ context.Context, tenantID, segmentID string, subject assessment.SubjectCode, archetype string) (*float64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for key, state := range s.states {
		if key == stateKey(tenantID, segmentID) {
			return state.ParserQuality.For(subject, archetype), nil
		}
	}
	return nil, nil
}

func (s *MemoryStore) exceptionForTenant(tenantID, exceptionID string) (Exception, bool) {
	item, ok := s.exceptions[exceptionID]
	if !ok {
		return Exception{}, false
	}
	_, ok = s.states[stateKey(tenantID, item.PageID)]
	return item, ok
}

func cloneState(value PageState) PageState { return value }
func cloneException(value Exception) Exception {
	copy := value
	copy.Details = map[string]any{}
	for key, item := range value.Details {
		copy.Details[key] = item
	}
	return copy
}

var _ Store = (*MemoryStore)(nil)

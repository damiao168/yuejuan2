package platformschools

import (
	"context"
	"sync"
)

// MemoryStore keeps the same read model available to the synthetic router.
// Tests may seed its exported Put helpers without inventing cross-tenant auth.
type MemoryStore struct {
	mu         sync.RWMutex
	schools    map[string]PlatformSchoolSummary
	members    map[string][]Member
	usage      map[string]UsageResult
	models     map[string]ModelHealthResult
	activities map[string]ActivityResult
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{schools: map[string]PlatformSchoolSummary{}, members: map[string][]Member{}, usage: map[string]UsageResult{}, models: map[string]ModelHealthResult{}, activities: map[string]ActivityResult{}}
}

func (s *MemoryStore) PutSchool(item PlatformSchoolSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schools[item.TenantID] = item
}
func (s *MemoryStore) PutMembers(tenantID string, items []Member) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[tenantID] = append([]Member(nil), items...)
}
func (s *MemoryStore) PutUsage(tenantID string, item UsageResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage[tenantID] = item
}
func (s *MemoryStore) PutModelHealth(tenantID string, item ModelHealthResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.models[tenantID] = item
}
func (s *MemoryStore) PutActivity(tenantID string, item ActivityResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activities[tenantID] = item
}

// 内存实现按租户汇总测试数据，字段含义与平台学校运营页保持一致。
func (s *MemoryStore) ListSummaries(_ context.Context, days int) ([]PlatformSchoolSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]PlatformSchoolSummary, 0, len(s.schools))
	for _, item := range s.schools {
		item.Usage.WindowDays = days
		items = append(items, item)
	}
	return items, nil
}
func (s *MemoryStore) ListMembers(_ context.Context, tenantID string) ([]Member, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Member(nil), s.members[tenantID]...), nil
}
func (s *MemoryStore) GetUsage(_ context.Context, tenantID string, _ UsageRange) (UsageResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.usage[tenantID]
	if !ok {
		return UsageResult{Trend: []UsageTrendPoint{}, ByFeature: []UsageBreakdown{}, ByModel: []UsageBreakdown{}}, nil
	}
	return item, nil
}
func (s *MemoryStore) GetModelHealth(_ context.Context, tenantID string) (ModelHealthResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.models[tenantID]
	if !ok {
		return ModelHealthResult{DefaultModel: ModelHealth{Status: ModelUnconfigured}, Roles: []ModelRole{}}, nil
	}
	return item, nil
}
func (s *MemoryStore) GetActivity(_ context.Context, tenantID string, _ int) (ActivityResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.activities[tenantID]
	if !ok {
		return ActivityResult{Activities: []ActivityItem{}}, nil
	}
	return item, nil
}

var _ Store = (*MemoryStore)(nil)

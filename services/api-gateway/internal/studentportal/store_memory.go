package studentportal

import (
	"context"
	"sort"
	"sync"
)

// MemoryStore 只保留路由和 handler 测试使用的发现索引，不保存另一份已发布成绩副本。
type MemoryStore struct {
	mu    sync.Mutex
	items map[string][]PublishedExam
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{items: map[string][]PublishedExam{}} }

func (s *MemoryStore) SeedPublishedExams(tenantID, studentID string, items []PublishedExam) {
	s.mu.Lock()
	defer s.mu.Unlock()
	copy := append([]PublishedExam(nil), items...)
	s.items[tenantID+":"+studentID] = copy
}

func (s *MemoryStore) ListPublishedExams(_ context.Context, tenantID, studentID string) ([]PublishedExam, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := append([]PublishedExam(nil), s.items[tenantID+":"+studentID]...)
	sort.Slice(items, func(i, j int) bool { return items[i].PublishedAt.After(items[j].PublishedAt) })
	return items, nil
}

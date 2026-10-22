package exam

import "context"

// BookmarkExamArchived 只读取归档事实；父考试归档后仍可回看，但不能新增或编辑个人说明。
func (s *MemoryStore) BookmarkExamArchived(_ context.Context, tenantID, examID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.items[examID]
	if !ok || e.TenantID != tenantID {
		return false, ErrNotFound
	}
	return s.memoryExamArchivedLocked(e), nil
}

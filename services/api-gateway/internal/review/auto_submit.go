package review

import "context"

// AutoSubmitBookmarkGuard 在内存标记锁内执行正式提交，避免检查后又新增标记。
// 标记存储先持锁、回调再锁任务，与标记读取任务来源的锁序一致；回调仍先核验已成功命令的回执。
type AutoSubmitBookmarkGuard interface {
	WithReviewTaskBookmarkLock(ctx context.Context, tenantID, reviewerID, taskID string, operation func(marked bool) error) error
}

func (s *MemoryStore) WithAutoSubmitBookmarkGuard(guard AutoSubmitBookmarkGuard) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoSubmitBookmarks = guard
	return s
}

func (s *MemoryStore) SubmitGrade(ctx context.Context, tenantID, id, reviewerID string, input SubmitGradeInput) (SubmitResult, error) {
	s.mu.RLock()
	guard := s.autoSubmitBookmarks
	s.mu.RUnlock()
	if !input.AutoSubmit || guard == nil {
		return s.submitGrade(ctx, tenantID, id, reviewerID, input, false)
	}
	var result SubmitResult
	err := guard.WithReviewTaskBookmarkLock(ctx, tenantID, reviewerID, id, func(marked bool) error {
		var err error
		result, err = s.submitGrade(ctx, tenantID, id, reviewerID, input, marked)
		return err
	})
	return result, err
}

func submittedReviewStatus(status string) bool {
	return status == "submitted" || status == "completed"
}

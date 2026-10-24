package seedquality

import "context"

// 与普通阅卷共用个人标记锁；质量任务仍只写自己的观察，不生成正式学生成绩。
type AutoSubmitBookmarkGuard interface {
	WithReviewTaskBookmarkLock(ctx context.Context, tenantID, reviewerID, taskID string, operation func(marked bool) error) error
}

func (s *MemoryStore) WithAutoSubmitBookmarkGuard(guard AutoSubmitBookmarkGuard) *MemoryStore {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoSubmitBookmarks = guard
	return s
}

func (s *MemoryStore) CompleteTask(ctx context.Context, tenantID, taskID, graderID string, input SubmitInput, observation Observation) (Task, Observation, error) {
	s.mu.RLock()
	guard := s.autoSubmitBookmarks
	s.mu.RUnlock()
	if !input.AutoSubmit || guard == nil {
		return s.completeTask(ctx, tenantID, taskID, graderID, input, observation, false)
	}
	var task Task
	var saved Observation
	err := guard.WithReviewTaskBookmarkLock(ctx, tenantID, graderID, taskID, func(marked bool) error {
		var err error
		task, saved, err = s.completeTask(ctx, tenantID, taskID, graderID, input, observation, marked)
		return err
	})
	return task, saved, err
}

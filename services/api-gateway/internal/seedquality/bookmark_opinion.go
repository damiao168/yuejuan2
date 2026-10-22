package seedquality

import (
	"context"
	"time"
)

// BookmarkOpinion 只返回当前任务的本人意见，不暴露参考分和质量观察。
type BookmarkOpinion struct {
	Score            float64
	RubricSelections map[string]any
	SubmittedAt      time.Time
}

func (s *MemoryStore) GetBookmarkOpinion(_ context.Context, tenantID, taskID, ownerID string) (BookmarkOpinion, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskKey(tenantID, taskID)]
	if !ok || task.task.AssignedTo != ownerID || task.task.Status != "completed" {
		return BookmarkOpinion{}, ErrNotFound
	}
	for _, entry := range s.observations {
		if entry.tenantID == tenantID && entry.taskID == taskID && entry.value.GraderID == ownerID {
			return BookmarkOpinion{Score: entry.value.SubmittedScore, RubricSelections: cloneObject(entry.value.RubricSelections), SubmittedAt: entry.value.ObservedAt}, nil
		}
	}
	return BookmarkOpinion{}, ErrNotFound
}

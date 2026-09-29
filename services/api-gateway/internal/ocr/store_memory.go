package ocr

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu         sync.RWMutex
	next       int
	tasks      map[string]Task
	results    map[string][]Result
	idempotent map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, tasks: map[string]Task{}, results: map[string][]Result{}, idempotent: map[string]string{}}
}

func (s *MemoryStore) CreateTask(ctx context.Context, tenantID string, submissionID string, actorID string, input CreateTaskInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createTaskLocked(ctx, tenantID, submissionID, actorID, input)
}

// 幂等键按租户隔离；同键同参数返回原任务，同键改参数直接冲突，防止悄悄改变 OCR 配置。
func (s *MemoryStore) createTaskLocked(_ context.Context, tenantID string, submissionID string, actorID string, input CreateTaskInput) (Task, error) {
	input, err := PrepareCreateInput(submissionID, input)
	if err != nil {
		return Task{}, err
	}
	key := tenantID + "|" + input.IdempotencyKey
	if id := s.idempotent[key]; id != "" {
		task := s.tasks[id]
		if !sameCreateRequest(task, submissionID, input) {
			return Task{}, ErrIdempotencyConflict
		}
		return cloneOCRTask(task), nil
	}
	task := Task{
		ID:            s.id("ocr-task"),
		TenantID:      tenantID,
		SubmissionID:  submissionID,
		Status:        "queued",
		Engine:        input.Engine,
		EngineVersion: input.EngineVersion,
		MinConfidence: input.MinConfidence,
		RequestedBy:   actorID,
		CreatedAt:     time.Now().UTC(),
	}
	s.tasks[task.ID] = task
	s.idempotent[key] = task.ID
	return task, nil
}

func (s *MemoryStore) ListPending(_ context.Context, tenantID string, limit int) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	out := []Task{}
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.Status == "queued" {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *MemoryStore) ListBySubmission(_ context.Context, tenantID string, submissionID string, filter TaskListFilter) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Task{}
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.SubmissionID == submissionID {
			task.Results = cloneOCRResults(s.results[task.ID])
			out = append(out, cloneOCRTask(task))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if !filter.CursorCreatedAt.IsZero() && filter.CursorID != "" {
		start := 0
		for start < len(out) && (out[start].CreatedAt.After(filter.CursorCreatedAt) ||
			(out[start].CreatedAt.Equal(filter.CursorCreatedAt) && out[start].ID >= filter.CursorID)) {
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (s *MemoryStore) GetTask(_ context.Context, tenantID string, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getTaskLocked(tenantID, id)
}

func (s *MemoryStore) getTaskLocked(tenantID string, id string) (Task, error) {
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	task.Results = cloneOCRResults(s.results[id])
	return cloneOCRTask(task), nil
}

func (s *MemoryStore) StartTask(_ context.Context, tenantID string, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if IsStarted(task.Status) {
		return task, nil
	}
	if !CanStart(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = "processing"
	task.StartedAt = &now
	task.CompletedAt = nil
	task.ErrorMessage = ""
	s.tasks[id] = task
	return task, nil
}

func (s *MemoryStore) CompleteTask(ctx context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeTaskLocked(ctx, tenantID, id, input)
}

// 已完成任务只接受完全相同的结果重试；不同结果必须报冲突，不能覆盖已有 OCR 证据。
func (s *MemoryStore) completeTaskLocked(_ context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if task.Status == "completed" {
		task.Results = cloneOCRResults(s.results[id])
		if sameCompletion(task, input) {
			return cloneOCRTask(task), nil
		}
		return Task{}, ErrResultConflict
	}
	if !CanComplete(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	if len(input.Results) == 0 {
		return Task{}, ErrInvalidInput
	}
	results := make([]Result, 0, len(input.Results))
	requiresReview := false
	for _, item := range input.Results {
		if err := ValidateResultInput(item); err != nil {
			return Task{}, err
		}
		if item.Confidence < task.MinConfidence {
			requiresReview = true
		}
		results = append(results, Result{
			ID:                s.id("ocr-result"),
			TenantID:          tenantID,
			TaskID:            id,
			SubmissionID:      task.SubmissionID,
			SubmissionPageID:  item.SubmissionPageID,
			Text:              item.Text,
			BBox:              append([]float64{}, item.BBox...),
			Confidence:        item.Confidence,
			OCREngine:         task.Engine,
			OCRVersion:        task.EngineVersion,
			ModelVersion:      input.ModelVersion,
			ConfigHash:        input.ConfigHash,
			InputHash:         input.InputHash,
			PreprocessProfile: input.PreprocessProfile,
			SourceImageFileID: item.SourceImageFileID,
			CreatedAt:         time.Now().UTC(),
		})
	}
	now := time.Now().UTC()
	task.Status = "completed"
	task.ModelVersion = input.ModelVersion
	task.ConfigHash = input.ConfigHash
	task.InputHash = input.InputHash
	task.DurationMS = input.DurationMS
	task.WorkerID = input.WorkerID
	task.AttemptCount++
	task.ResultCount = len(results)
	task.RequiresHumanReview = requiresReview
	task.CompletedAt = &now
	task.Results = results
	s.tasks[id] = task
	s.results[id] = results
	return cloneOCRTask(task), nil
}

func (s *MemoryStore) FailTask(ctx context.Context, tenantID string, id string, errorMessage string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failTaskLocked(ctx, tenantID, id, errorMessage)
}

func (s *MemoryStore) failTaskLocked(_ context.Context, tenantID string, id string, errorMessage string) (Task, error) {
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if !CanFail(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = "failed"
	task.ErrorMessage = errorMessage
	task.CompletedAt = &now
	s.tasks[id] = task
	return cloneOCRTask(task), nil
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

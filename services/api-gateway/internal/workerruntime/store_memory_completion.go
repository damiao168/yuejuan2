package workerruntime

import (
	"context"
	"time"
)

func (s *MemoryStore) Complete(_ context.Context, tenantID string, taskID string, input CompleteInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completeLocked(tenantID, taskID, input)
}

func (tx *MemoryTx) Complete(_ context.Context, tenantID string, taskID string, input CompleteInput) (Task, error) {
	return tx.store.completeLocked(tenantID, taskID, input)
}

func (s *MemoryStore) completeLocked(tenantID string, taskID string, input CompleteInput) (Task, error) {
	if input.LeaseToken == "" || input.ResultSchemaVersion == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	hash := payloadHash(input.ResultSchemaVersion, input.Result)
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if task.Status == StatusSucceeded {
		// 完成回调可能重发；只允许同一租约和同一结果返回原成功记录，不能覆盖已接受结果。
		if task.LeaseToken != input.LeaseToken {
			return Task{}, ErrLeaseMismatch
		}
		if task.ResultPayloadHash == hash {
			return cloneTask(task), nil
		}
		return Task{}, ErrConflict
	}
	var err error
	task, err = s.validateLease(task, input.LeaseToken)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = StatusSucceeded
	task.Result = cloneMap(input.Result)
	task.ResultSchemaVersion = input.ResultSchemaVersion
	task.ResultPayloadHash = hash
	task.DurationMS = input.DurationMS
	task.ErrorCode = ""
	task.ErrorDetail = map[string]any{}
	task.CompletedAt = &now
	task.UpdatedAt = now
	task.Revision++
	s.finishAttempt(&task, StatusSucceeded, input.DurationMS, "", map[string]any{})
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) Fail(_ context.Context, tenantID string, taskID string, input FailInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failLocked(tenantID, taskID, input)
}

func (tx *MemoryTx) Fail(_ context.Context, tenantID string, taskID string, input FailInput) (Task, error) {
	return tx.store.failLocked(tenantID, taskID, input)
}

func (s *MemoryStore) failLocked(tenantID string, taskID string, input FailInput) (Task, error) {
	if input.LeaseToken == "" || input.ErrorCode == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	var err error
	task, err = s.validateLease(task, input.LeaseToken)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.ErrorCode = input.ErrorCode
	task.ErrorDetail = cloneMap(input.ErrorDetail)
	task.DurationMS = input.DurationMS
	s.finishAttempt(&task, StatusFailed, input.DurationMS, input.ErrorCode, input.ErrorDetail)
	if input.Retryable && task.AttemptCount < task.MaxAttempts {
		delay := retryDelay(task.RetryBackoffSeconds, task.AttemptCount)
		notBefore := now.Add(delay)
		task.Status = StatusQueued
		task.NotBefore = &notBefore
		task.LeaseToken = ""
		task.LeaseExpiresAt = nil
		task.LeasedBy = ""
	} else if input.Retryable {
		task.Status = StatusDeadLetter
		task.CompletedAt = &now
	} else {
		task.Status = StatusFailed
		task.CompletedAt = &now
	}
	task.UpdatedAt = now
	task.Revision++
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) Cancel(_ context.Context, tenantID string, taskID string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if task.Status != StatusQueued && task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	if task.Status == StatusLeased || task.Status == StatusRunning {
		s.finishAttempt(&task, StatusCancelled, 0, "cancelled", map[string]any{})
	}
	task.Status = StatusCancelled
	task.CancelledAt = &now
	task.CompletedAt = &now
	task.UpdatedAt = now
	task.Revision++
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) finishAttempt(task *Task, status string, durationMS int, errorCode string, detail map[string]any) {
	if len(task.Attempts) == 0 {
		return
	}
	now := time.Now().UTC()
	attempt := &task.Attempts[len(task.Attempts)-1]
	attempt.Status = status
	attempt.DurationMS = durationMS
	attempt.ErrorCode = errorCode
	attempt.ErrorDetail = cloneMap(detail)
	attempt.CompletedAt = &now
}

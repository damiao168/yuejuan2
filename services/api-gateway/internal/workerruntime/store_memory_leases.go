package workerruntime

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) Claim(_ context.Context, tenantID string, input ClaimInput) ([]Task, error) {
	return s.claim(tenantID, false, input)
}

func (s *MemoryStore) ClaimAcrossTenants(_ context.Context, platformTenantID string, input ClaimInput) ([]Task, error) {
	return s.claim(platformTenantID, true, input)
}

func (s *MemoryStore) claim(heartbeatTenantID string, acrossTenants bool, input ClaimInput) ([]Task, error) {
	input = normalizeClaimInput(input)
	if validateClaimInput(input) != nil {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.recordWorkerHeartbeat(heartbeatTenantID, input.WorkerService, input.WorkerInstanceID, input.QueueName, now, map[string]any{"state": "polling"})
	candidates := s.claimCandidatesLocked(heartbeatTenantID, acrossTenants, input.QueueName, now)
	out := make([]Task, 0, input.Limit)
	for _, candidate := range candidates {
		if len(out) >= input.Limit {
			break
		}
		task := s.tasks[candidate.ID]
		activeLease := task.Status == StatusLeased || task.Status == StatusRunning
		if activeLease {
			s.finishAttempt(&task, "lease_expired", 0, "lease_expired", map[string]any{})
		}
		// 耗尽项转入死信但不占返回名额；后续候选仍可领取。
		if deadLetterExhaustedClaim(&task, activeLease) {
			task.CompletedAt = &now
			task.UpdatedAt = now
			task.Revision++
			s.tasks[task.ID] = task
			continue
		}
		token := newLeaseToken()
		expires := now.Add(time.Duration(input.LeaseSeconds) * time.Second)
		task.Status = StatusLeased
		task.AttemptCount++
		task.LeaseToken = token
		task.LeaseExpiresAt = &expires
		task.LeasedBy = input.WorkerService + ":" + input.WorkerInstanceID
		task.WorkerService = input.WorkerService
		task.WorkerInstanceID = input.WorkerInstanceID
		task.NotBefore = nil
		task.UpdatedAt = now
		task.Revision++
		task.Attempts = append(task.Attempts, Attempt{
			ID: s.id("worker-attempt"), TenantID: task.TenantID, TaskID: task.ID, AttemptNo: task.AttemptCount,
			WorkerService: input.WorkerService, WorkerInstanceID: input.WorkerInstanceID, LeaseToken: token,
			Status: StatusLeased, StartedAt: now, ErrorDetail: map[string]any{},
		})
		s.tasks[task.ID] = task
		out = append(out, cloneTask(task))
	}
	return out, nil
}

// 调用方持有 claim 的写锁；筛选与排序后仍在同一锁内关闭旧尝试或生成新租约。
func (s *MemoryStore) claimCandidatesLocked(tenantID string, acrossTenants bool, queueName string, now time.Time) []Task {
	candidates := make([]Task, 0)
	for _, task := range s.tasks {
		if (!acrossTenants && task.TenantID != tenantID) || task.QueueName != queueName {
			continue
		}
		ready := task.Status == StatusQueued && (task.NotBefore == nil || !task.NotBefore.After(now))
		expired := (task.Status == StatusLeased || task.Status == StatusRunning) && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.Before(now)
		if ready || expired {
			candidates = append(candidates, task)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return claimPrecedes(candidates[i], candidates[j])
	})
	return candidates
}

func claimPrecedes(left, right Task) bool {
	if left.Priority != right.Priority {
		return left.Priority < right.Priority
	}
	if left.CreatedAt.Equal(right.CreatedAt) {
		return left.ID < right.ID
	}
	return left.CreatedAt.Before(right.CreatedAt)
}

func deadLetterExhaustedClaim(task *Task, activeLease bool) bool {
	if task.AttemptCount < task.MaxAttempts {
		return false
	}
	// 防御性处理已超过配置上限的旧行时，保留已观察到的历史。
	if task.AttemptCount > task.MaxAttempts {
		task.MaxAttempts = task.AttemptCount
	}
	errorCode := "max_attempts_exhausted"
	if activeLease {
		errorCode = "lease_expired"
	}
	task.Status = StatusDeadLetter
	task.ErrorCode = errorCode
	task.ErrorDetail = map[string]any{"reason": "max_attempts_exhausted"}
	task.LeaseToken = ""
	task.LeaseExpiresAt = nil
	task.LeasedBy = ""
	return true
}

func (s *MemoryStore) Heartbeat(_ context.Context, tenantID string, taskID string, input HeartbeatInput) (Task, error) {
	input = normalizeHeartbeatInput(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	task, err := s.activeLease(tenantID, taskID, input.LeaseToken)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	if input.WorkerService == "" || input.WorkerInstanceID == "" || (input.State != "" && input.State != StatusRunning) {
		return Task{}, ErrInvalidInput
	}
	if task.WorkerService != input.WorkerService || task.WorkerInstanceID != input.WorkerInstanceID {
		return Task{}, ErrLeaseMismatch
	}
	now := time.Now().UTC()
	expires := now.Add(time.Duration(input.LeaseSeconds) * time.Second)
	if task.Status == StatusLeased {
		task.Status = StatusRunning
		task.StartedAt = &now
	}
	task.UpdatedAt = now
	task.Revision++
	task.LeaseExpiresAt = &expires
	task.Progress = cloneMap(input.Progress)
	if len(task.Attempts) > 0 {
		attempt := &task.Attempts[len(task.Attempts)-1]
		attempt.Status = StatusRunning
		attempt.HeartbeatAt = &now
	}
	metadata := cloneMap(input.Progress)
	metadata["state"] = StatusRunning
	s.recordWorkerHeartbeat(tenantID, input.WorkerService, input.WorkerInstanceID, task.QueueName, now, metadata)
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) recordWorkerHeartbeat(tenantID string, workerService string, workerInstanceID string, queueName string, now time.Time, metadata map[string]any) {
	key := tenantID + "|" + workerService + "|" + workerInstanceID + "|" + queueName
	s.heartbeats[key] = WorkerHeartbeat{
		TenantID: tenantID, WorkerService: workerService, WorkerInstanceID: workerInstanceID,
		QueueName: queueName, LastSeenAt: now, Metadata: cloneMap(metadata),
	}
}

func (s *MemoryStore) activeLease(tenantID string, taskID string, token string) (Task, error) {
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	return s.validateLease(task, token)
}

func (s *MemoryStore) validateLease(task Task, token string) (Task, error) {
	// 内存与 PostgreSQL 共用到期边界，避免相同令牌在执行授权与落库检查中得到相反结论。
	if err := validateTaskLeaseAt(task, token, time.Now().UTC()); err != nil {
		return Task{}, err
	}
	return task, nil
}

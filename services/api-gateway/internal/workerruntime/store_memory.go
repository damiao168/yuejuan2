package workerruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu         sync.RWMutex
	next       int
	tasks      map[string]Task
	idempotent map[string]string
	heartbeats map[string]WorkerHeartbeat
}

// MemoryTx exposes lock-safe operations while MemoryStore.RunAtomic holds the
// store lock. Callers must not invoke the parent store's public methods from an
// atomic callback because those methods acquire the same lock.
type MemoryTx struct {
	store *MemoryStore
}

type memorySnapshot struct {
	next       int
	tasks      map[string]Task
	idempotent map[string]string
	heartbeats map[string]WorkerHeartbeat
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, tasks: map[string]Task{}, idempotent: map[string]string{}, heartbeats: map[string]WorkerHeartbeat{}}
}

// RunAtomic executes runtime mutations under one lock and restores the store
// snapshot if the callback returns an error or panics. Source-domain memory
// stores can nest this transaction while holding their own lock to keep source
// facts and runtime state consistent in tests and local development.
func (s *MemoryStore) RunAtomic(fn func(*MemoryTx) error) (err error) {
	if fn == nil {
		return ErrInvalidInput
	}
	s.mu.Lock()
	snapshot := s.snapshotLocked()
	committed := false
	defer func() {
		if !committed {
			s.restoreLocked(snapshot)
		}
		s.mu.Unlock()
	}()
	if err := fn(&MemoryTx{store: s}); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *MemoryStore) CreateTask(_ context.Context, tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createTaskLocked(tenantID, actorID, input)
}

func (tx *MemoryTx) CreateTask(_ context.Context, tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	return tx.store.createTaskLocked(tenantID, actorID, input)
}

func (s *MemoryStore) createTaskLocked(tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	input = normalizeCreateInput(input)
	if tenantID == "" || validateCreateInput(input) != nil {
		return Task{}, ErrInvalidInput
	}
	key := tenantID + "|" + input.TaskType + "|" + input.IdempotencyKey
	if id := s.idempotent[key]; id != "" {
		return cloneTask(s.tasks[id]), nil
	}
	now := time.Now().UTC()
	task := Task{
		ID: s.id("worker-task"), TenantID: tenantID, TaskType: input.TaskType, QueueName: input.QueueName,
		SourceType: input.SourceType, SourceID: input.SourceID, Status: StatusQueued, Priority: input.Priority,
		Payload: cloneMap(input.Payload), PayloadSchemaVersion: input.PayloadSchemaVersion, Result: map[string]any{},
		IdempotencyKey: input.IdempotencyKey, DedupeKey: input.DedupeKey, MaxAttempts: input.MaxAttempts,
		RetryBackoffSeconds: input.RetryBackoffSeconds, ErrorDetail: map[string]any{}, CreatedBy: actorID,
		Revision: 1, CreatedAt: now, UpdatedAt: now, Attempts: []Attempt{},
	}
	s.tasks[task.ID] = task
	s.idempotent[key] = task.ID
	return cloneTask(task), nil
}

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
	candidates := make([]Task, 0)
	for _, task := range s.tasks {
		if (!acrossTenants && task.TenantID != heartbeatTenantID) || task.QueueName != input.QueueName {
			continue
		}
		ready := task.Status == StatusQueued && (task.NotBefore == nil || !task.NotBefore.After(now))
		expired := (task.Status == StatusLeased || task.Status == StatusRunning) && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.Before(now)
		if ready || expired {
			candidates = append(candidates, task)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority < candidates[j].Priority
		}
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
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
		if task.AttemptCount >= task.MaxAttempts {
			// Preserve observed history for defensive handling of legacy rows that
			// already exceeded their configured maximum.
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

// CreateSucceededTask creates (or idempotently reuses) a runtime task and
// records a trusted imported result without manufacturing a worker lease.
func (s *MemoryStore) CreateSucceededTask(ctx context.Context, tenantID string, actorID string, create CreateTaskInput, complete CompleteInput) (Task, error) {
	var task Task
	err := s.RunAtomic(func(tx *MemoryTx) error {
		var err error
		task, err = tx.CreateSucceededTask(ctx, tenantID, actorID, create, complete)
		return err
	})
	return task, err
}

func (tx *MemoryTx) CreateSucceededTask(_ context.Context, tenantID string, actorID string, create CreateTaskInput, complete CompleteInput) (Task, error) {
	return tx.store.createSucceededTaskLocked(tenantID, actorID, create, complete)
}

func (s *MemoryStore) createSucceededTaskLocked(tenantID string, actorID string, create CreateTaskInput, complete CompleteInput) (Task, error) {
	if complete.ResultSchemaVersion == "" || complete.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	task, err := s.createTaskLocked(tenantID, actorID, create)
	if err != nil {
		return Task{}, err
	}
	hash := payloadHash(complete.ResultSchemaVersion, complete.Result)
	if task.Status == StatusSucceeded {
		if task.ResultPayloadHash != hash {
			return Task{}, ErrConflict
		}
		return task, nil
	}
	if task.Status != StatusQueued || task.AttemptCount != 0 {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = StatusSucceeded
	task.Result = cloneMap(complete.Result)
	task.ResultSchemaVersion = complete.ResultSchemaVersion
	task.ResultPayloadHash = hash
	task.DurationMS = complete.DurationMS
	task.ErrorCode = ""
	task.ErrorDetail = map[string]any{}
	task.CompletedAt = &now
	task.UpdatedAt = now
	task.Revision++
	s.tasks[task.ID] = task
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

func (s *MemoryStore) Requeue(_ context.Context, tenantID string, taskID string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if task.Status != StatusDeadLetter && task.Status != StatusFailed {
		return Task{}, ErrInvalidTransition
	}
	task.Status = StatusQueued
	if task.MaxAttempts <= task.AttemptCount {
		task.MaxAttempts = task.AttemptCount + 1
	}
	task.NotBefore = nil
	task.LeaseToken = ""
	task.LeaseExpiresAt = nil
	task.CompletedAt = nil
	task.UpdatedAt = time.Now().UTC()
	task.Revision++
	s.tasks[taskID] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID string, taskID string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getLocked(tenantID, taskID)
}

func (s *MemoryStore) AuthorizeLease(_ context.Context, taskID string, leaseToken string, workerService string, workerInstanceID string, now time.Time) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok || task.LeaseToken == "" || task.LeaseToken != leaseToken ||
		task.WorkerService != workerService || task.WorkerInstanceID != workerInstanceID ||
		(task.Status != StatusLeased && task.Status != StatusRunning) ||
		task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(now) {
		return Task{}, ErrLeaseMismatch
	}
	return cloneTask(task), nil
}

func (tx *MemoryTx) Get(_ context.Context, tenantID string, taskID string) (Task, error) {
	return tx.store.getLocked(tenantID, taskID)
}

func (s *MemoryStore) getLocked(tenantID string, taskID string) (Task, error) {
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	return cloneTask(task), nil
}

func (s *MemoryStore) GetBySource(_ context.Context, tenantID string, sourceType string, sourceID string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getBySourceLocked(tenantID, sourceType, sourceID)
}

func (tx *MemoryTx) GetBySource(_ context.Context, tenantID string, sourceType string, sourceID string) (Task, error) {
	return tx.store.getBySourceLocked(tenantID, sourceType, sourceID)
}

func (s *MemoryStore) getBySourceLocked(tenantID string, sourceType string, sourceID string) (Task, error) {
	var found *Task
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.SourceType == sourceType && task.SourceID == sourceID {
			copy := task
			if found == nil || copy.CreatedAt.After(found.CreatedAt) {
				found = &copy
			}
		}
	}
	if found == nil {
		return Task{}, ErrNotFound
	}
	return cloneTask(*found), nil
}

func (s *MemoryStore) Metrics(_ context.Context, tenantID string) (Metrics, error) {
	return s.metrics(tenantID, false), nil
}

func (s *MemoryStore) MetricsAcrossTenants(_ context.Context, platformTenantID string) (Metrics, error) {
	return s.metrics(platformTenantID, true), nil
}

func (s *MemoryStore) metrics(tenantID string, acrossTenants bool) Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byQueue := map[string]*QueueMetrics{}
	durations := map[string][]int{}
	cutoff := time.Now().UTC().Add(-time.Hour)
	for _, task := range s.tasks {
		if !acrossTenants && task.TenantID != tenantID {
			continue
		}
		metric := byQueue[task.QueueName]
		if metric == nil {
			metric = &QueueMetrics{QueueName: task.QueueName}
			byQueue[task.QueueName] = metric
		}
		switch task.Status {
		case StatusQueued:
			metric.Queued++
		case StatusLeased:
			metric.Leased++
		case StatusRunning:
			metric.Running++
		case StatusSucceeded:
			metric.Succeeded++
		case StatusFailed:
			metric.Failed++
		case StatusDeadLetter:
			metric.DeadLetter++
		case StatusCancelled:
			metric.Cancelled++
		}
		if (task.Status == StatusFailed || task.Status == StatusDeadLetter) && task.UpdatedAt.After(cutoff) {
			metric.FailedLastHour++
		}
		if task.DurationMS > 0 {
			durations[task.QueueName] = append(durations[task.QueueName], task.DurationMS)
		}
		for _, attempt := range task.Attempts {
			if attempt.Status == StatusFailed && attempt.CompletedAt != nil && attempt.CompletedAt.After(cutoff) && task.AttemptCount < task.MaxAttempts {
				metric.RetryLastHour++
			}
		}
	}
	out := Metrics{Queues: []QueueMetrics{}, Workers: []WorkerHeartbeat{}}
	for queue, metric := range byQueue {
		metric.P95DurationMS = percentile95(durations[queue])
		out.Queues = append(out.Queues, *metric)
	}
	sort.Slice(out.Queues, func(i, j int) bool { return out.Queues[i].QueueName < out.Queues[j].QueueName })
	for _, worker := range s.heartbeats {
		if acrossTenants || worker.TenantID == tenantID {
			out.Workers = append(out.Workers, worker)
		}
	}
	sort.Slice(out.Workers, func(i, j int) bool { return out.Workers[i].LastSeenAt.After(out.Workers[j].LastSeenAt) })
	return out
}

func (s *MemoryStore) ForceExpireLeaseForTest(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	expired := time.Now().UTC().Add(-time.Second)
	task.LeaseExpiresAt = &expired
	s.tasks[taskID] = task
}

func (s *MemoryStore) MakeRunnableForTest(taskID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	past := time.Now().UTC().Add(-time.Second)
	task.NotBefore = &past
	s.tasks[taskID] = task
}

func (s *MemoryStore) snapshotLocked() memorySnapshot {
	tasks := make(map[string]Task, len(s.tasks))
	for id, task := range s.tasks {
		tasks[id] = cloneTask(task)
	}
	idempotent := make(map[string]string, len(s.idempotent))
	for key, id := range s.idempotent {
		idempotent[key] = id
	}
	heartbeats := make(map[string]WorkerHeartbeat, len(s.heartbeats))
	for key, heartbeat := range s.heartbeats {
		heartbeat.Metadata = cloneMap(heartbeat.Metadata)
		heartbeats[key] = heartbeat
	}
	return memorySnapshot{next: s.next, tasks: tasks, idempotent: idempotent, heartbeats: heartbeats}
}

func (s *MemoryStore) restoreLocked(snapshot memorySnapshot) {
	s.next = snapshot.next
	s.tasks = snapshot.tasks
	s.idempotent = snapshot.idempotent
	s.heartbeats = snapshot.heartbeats
}

func (s *MemoryStore) activeLease(tenantID string, taskID string, token string) (Task, error) {
	task, ok := s.tasks[taskID]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	return s.validateLease(task, token)
}

func (s *MemoryStore) validateLease(task Task, token string) (Task, error) {
	if token == "" || task.LeaseToken != token {
		return Task{}, ErrLeaseMismatch
	}
	if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.Before(time.Now().UTC()) {
		return Task{}, ErrLeaseExpired
	}
	return task, nil
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

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func newLeaseToken() string {
	var value [24]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("lease-%d", time.Now().UnixNano())
}

func retryDelay(base int, attempt int) time.Duration {
	multiplier := math.Pow(2, float64(max(attempt-1, 0)))
	seconds := math.Min(float64(base)*multiplier, 86400)
	return time.Duration(seconds) * time.Second
}

func percentile95(values []int) int {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]int{}, values...)
	sort.Ints(copyValues)
	index := int(math.Ceil(float64(len(copyValues))*0.95)) - 1
	if index < 0 {
		index = 0
	}
	return copyValues[index]
}

func cloneTask(task Task) Task {
	task.Payload = cloneMap(task.Payload)
	task.Result = cloneMap(task.Result)
	task.Progress = cloneMap(task.Progress)
	task.ErrorDetail = cloneMap(task.ErrorDetail)
	task.Attempts = append([]Attempt{}, task.Attempts...)
	for i := range task.Attempts {
		task.Attempts[i].ErrorDetail = cloneMap(task.Attempts[i].ErrorDetail)
	}
	return task
}

func cloneMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	raw, _ := json.Marshal(input)
	var output map[string]any
	if json.Unmarshal(raw, &output) != nil {
		return map[string]any{}
	}
	return output
}

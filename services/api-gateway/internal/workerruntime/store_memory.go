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

// MemoryTx 在 MemoryStore.RunAtomic 持有存储锁时提供无需重复加锁的操作。
// 调用方不能在原子回调中调用父存储的公开方法，因为这些方法会获取同一把锁。
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

// RunAtomic 在一把锁内执行运行时变更，回调返回错误或 panic 时恢复存储快照。
// 来源领域内存存储可在持有自身锁时嵌套此事务，使测试和本地开发中的来源事实与运行时状态保持一致。
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

// CreateSucceededTask 创建或幂等复用运行时任务，记录可信导入结果，不虚构 worker 租约。
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

func (s *MemoryStore) Requeue(_ context.Context, tenantID string, taskID string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requeueLocked(tenantID, taskID)
}

// Requeue 在来源协调器持有两侧事务锁时恢复任务，失败由 RunAtomic 回滚。
func (tx *MemoryTx) Requeue(_ context.Context, tenantID, taskID string) (Task, error) {
	return tx.store.requeueLocked(tenantID, taskID)
}

func (s *MemoryStore) requeueLocked(tenantID, taskID string) (Task, error) {
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

// LockTaskForCompletion 在内存事务内检查租约；成功记录只允许进入后续的同结果回放检查。
func (tx *MemoryTx) LockTaskForCompletion(_ context.Context, tenantID, taskID, leaseToken string) (Task, error) {
	task, err := tx.store.getLocked(tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status == StatusSucceeded {
		if leaseToken == "" || task.LeaseToken != leaseToken {
			return Task{}, ErrLeaseMismatch
		}
		return task, nil
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	return tx.store.validateLease(task, leaseToken)
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

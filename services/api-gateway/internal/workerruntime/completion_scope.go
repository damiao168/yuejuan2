package workerruntime

import (
	"context"
	"errors"
)

// AuthorizeCompletedLease 只验证已完成回调的身份；调用方必须继续核对来源、版本和结果哈希。
func (s *MemoryStore) AuthorizeCompletedLease(_ context.Context, taskID, leaseToken, workerService, workerInstanceID string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[taskID]
	if !ok || task.Status != StatusSucceeded || task.AttemptCount <= 0 || leaseToken == "" || task.LeaseToken != leaseToken ||
		workerService == "" || task.WorkerService != workerService || workerInstanceID == "" || task.WorkerInstanceID != workerInstanceID {
		return Task{}, ErrLeaseMismatch
	}
	return cloneTask(task), nil
}

// AuthorizeCompletedLease 保留成功尝试的 worker 归属，不授予任何新的租约。
func (s *PostgresStore) AuthorizeCompletedLease(ctx context.Context, taskID, leaseToken, workerService, workerInstanceID string) (Task, error) {
	if taskID == "" || leaseToken == "" || workerService == "" || workerInstanceID == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := scanTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM agent_worker_task
WHERE id=$1::uuid AND tenant_id IN (SELECT id FROM tenant WHERE status='active' AND deleted_at IS NULL)
  AND status='succeeded' AND attempt_count>0 AND lease_token=$2 AND worker_service=$3 AND worker_instance_id=$4`,
		taskID, leaseToken, workerService, workerInstanceID))
	if errors.Is(err, ErrNotFound) {
		return Task{}, ErrLeaseMismatch
	}
	return task, err
}

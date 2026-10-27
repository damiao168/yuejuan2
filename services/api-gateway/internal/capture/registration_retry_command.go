package capture

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"errors"
)

// RegistrationRetryStore 对工作台和配准专用入口提供同一恢复协议。
type RegistrationRetryStore interface {
	RetryRegistrationTask(context.Context, string, string, string, workerruntime.Store) (workerruntime.Task, error)
}

// RetryRegistrationTask 按来源→运行时锁顺序同时恢复配准记录及任务，拒绝错误来源或已结束批次。
func (s *PostgresStore) RetryRegistrationTask(ctx context.Context, tenantID, runID, taskID string, _ workerruntime.Store) (out workerruntime.Task, resultErr error) {
	defer func() { resultErr = registrationRetryError(resultErr) }()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerruntime.Task{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationRun(tx.QueryRowContext(ctx, `SELECT `+registrationColumns+` FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return workerruntime.Task{}, err
	}
	if current.RuntimeTaskID != taskID || (current.ProcessingStatus != "terminal_error" && current.ProcessingStatus != "retryable_error" && current.ProcessingStatus != "processing") {
		return workerruntime.Task{}, ErrInvalidTransition
	}
	var batchID string
	if err := tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, current.CapturePageID).Scan(&batchID); err != nil {
		return workerruntime.Task{}, mapNotFound(err)
	}
	if err := ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return workerruntime.Task{}, err
	}
	task, err := workerruntime.LockTaskForRetryInTx(ctx, tx, tenantID, taskID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	if task.SourceType != "page_registration_run" || task.SourceID != runID {
		return workerruntime.Task{}, workerruntime.ErrConflict
	}
	if task.Status != workerruntime.StatusQueued && task.Status != workerruntime.StatusLeased && task.Status != workerruntime.StatusRunning {
		task, err = workerruntime.RequeueTaskInTx(ctx, tx, tenantID, taskID)
		if err != nil {
			return workerruntime.Task{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='processing',match_status=NULL,error_code=NULL,error_detail='{}',started_at=now(),completed_at=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID); err != nil {
		return workerruntime.Task{}, err
	}
	return task, tx.Commit()
}

func (s *MemoryStore) RetryRegistrationTask(ctx context.Context, tenantID, runID, taskID string, runtime workerruntime.Store) (task workerruntime.Task, err error) {
	defer func() { err = registrationRetryError(err) }()
	atomic, ok := runtime.(*workerruntime.MemoryStore)
	if !ok {
		return task, ErrInvalidTransition
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.registrations[runID]
	if !ok {
		return task, ErrNotFound
	}
	page, ok := s.pages[current.CapturePageID]
	if !ok || page.TenantID != tenantID {
		return task, ErrNotFound
	}
	batch, ok := s.batches[page.CaptureBatchID]
	if !ok || batch.TenantID != tenantID {
		return task, ErrNotFound
	}
	if batch.Status == "completed" || batch.Status == "cancelled" || current.RuntimeTaskID != taskID || (current.ProcessingStatus != "terminal_error" && current.ProcessingStatus != "retryable_error" && current.ProcessingStatus != "processing") {
		return task, ErrInvalidTransition
	}
	err = atomic.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		var e error
		task, e = tx.Get(ctx, tenantID, taskID)
		if e != nil {
			return e
		}
		if task.SourceType != "page_registration_run" || task.SourceID != runID {
			return workerruntime.ErrConflict
		}
		if task.Status != workerruntime.StatusQueued && task.Status != workerruntime.StatusLeased && task.Status != workerruntime.StatusRunning {
			task, e = tx.Requeue(ctx, tenantID, taskID)
			if e != nil {
				return e
			}
		}
		current.ProcessingStatus, current.MatchStatus, current.ErrorCode = "processing", "", ""
		s.registrations[runID] = current
		return nil
	})
	return task, err
}

func registrationRetryError(err error) error {
	if errors.Is(err, ErrNotFound) {
		return workerruntime.ErrNotFound
	}
	if errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrConflict) {
		return workerruntime.ErrInvalidTransition
	}
	return err
}

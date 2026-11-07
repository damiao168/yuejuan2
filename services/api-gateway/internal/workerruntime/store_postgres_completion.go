package workerruntime

import (
	"context"
	"database/sql"
	"encoding/json"
)

func (s *PostgresStore) Complete(ctx context.Context, tenantID string, taskID string, input CompleteInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := CompleteTaskInTx(ctx, tx, tenantID, taskID, input)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// CompleteTaskInTx 在调用方的 PostgreSQL 事务内校验并完成 Worker Runtime 任务。
// 来源适配器在任务结果与来源领域事实必须一起提交时使用它。
func CompleteTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, taskID string, input CompleteInput) (Task, error) {
	if tx == nil || input.LeaseToken == "" || input.ResultSchemaVersion == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	hash := payloadHash(input.ResultSchemaVersion, input.Result)
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status == StatusSucceeded {
		if task.LeaseToken != input.LeaseToken {
			return Task{}, ErrLeaseMismatch
		}
		if task.ResultPayloadHash != hash {
			return Task{}, ErrConflict
		}
		return task, nil
	}
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, input.LeaseToken, dbNow); err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	result, _ := json.Marshal(input.Result)
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'succeeded', result = $3, result_schema_version = $4,
  result_payload_hash = $5, duration_ms = $6, completed_at = $7, updated_at = $7
  , error_code = NULL, error_detail = '{}', revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, result, input.ResultSchemaVersion, hash, input.DurationMS, dbNow)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	attemptResult, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'succeeded', duration_ms = $4, completed_at = $5, error_code = NULL, error_detail = '{}'
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, input.DurationMS, dbNow)
	if err != nil {
		return Task{}, err
	}
	if rows, rowsErr := attemptResult.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return Task{}, rowsErr
		}
		return Task{}, ErrConflict
	}
	return task, nil
}

// LockTaskForCompletionInTx 先锁定当前任务再写来源事实，避免另一尝试在提交期间接管租约。
// 成功任务仍须由调用方校验已接受结果，并在 CompleteTaskInTx 中完成严格回放校验。
func LockTaskForCompletionInTx(ctx context.Context, tx *sql.Tx, tenantID, taskID, leaseToken string) (Task, error) {
	if tx == nil || leaseToken == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status == StatusSucceeded {
		if task.LeaseToken != leaseToken {
			return Task{}, ErrLeaseMismatch
		}
		return task, nil
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, leaseToken, now); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *PostgresStore) Fail(ctx context.Context, tenantID string, taskID string, input FailInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := FailTaskInTx(ctx, tx, tenantID, taskID, input)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// FailTaskInTx 在调用方的 PostgreSQL 事务内校验 Worker Runtime 任务并标记失败或安排重试。
func FailTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, taskID string, input FailInput) (Task, error) {
	if input.LeaseToken == "" || input.ErrorCode == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	if tx == nil {
		return Task{}, ErrInvalidInput
	}
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, input.LeaseToken, dbNow); err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	status := StatusFailed
	var notBefore any
	if input.Retryable && task.AttemptCount < task.MaxAttempts {
		status = StatusQueued
		notBefore = dbNow.Add(retryDelay(task.RetryBackoffSeconds, task.AttemptCount))
	} else if input.Retryable {
		status = StatusDeadLetter
	}
	detail, _ := json.Marshal(input.ErrorDetail)
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = $3, not_before = $4, lease_token = CASE WHEN $3 = 'queued' THEN NULL ELSE lease_token END,
  lease_expires_at = CASE WHEN $3 = 'queued' THEN NULL ELSE lease_expires_at END,
  leased_by = CASE WHEN $3 = 'queued' THEN NULL ELSE leased_by END,
  error_code = $5, error_detail = $6, duration_ms = $7,
  completed_at = CASE WHEN $3 IN ('failed', 'dead_letter') THEN $8::timestamptz ELSE NULL END,
  updated_at = $8, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, status, notBefore, input.ErrorCode, detail, input.DurationMS, dbNow)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	attemptResult, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'failed', duration_ms = $4, error_code = $5, error_detail = $6, completed_at = $7
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, input.DurationMS, input.ErrorCode, detail, dbNow)
	if err != nil {
		return Task{}, err
	}
	if rows, rowsErr := attemptResult.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return Task{}, rowsErr
		}
		return Task{}, ErrConflict
	}
	return task, nil
}

// LockTaskForRetryInTx 供来源恢复协调器先检查已在队列中的任务，不另开事务或重复排队。
func LockTaskForRetryInTx(ctx context.Context, tx *sql.Tx, tenantID, taskID string) (Task, error) {
	if tx == nil {
		return Task{}, ErrInvalidInput
	}
	return getTaskForUpdate(ctx, tx, tenantID, taskID)
}

func (s *PostgresStore) Cancel(ctx context.Context, tenantID string, taskID string) (Task, error) {
	return s.setTerminalStatus(ctx, tenantID, taskID, StatusCancelled)
}

func (s *PostgresStore) setTerminalStatus(ctx context.Context, tenantID string, taskID string, status string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusQueued && task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = $3, cancelled_at = now(), completed_at = now(), updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, status)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt SET status = $4, completed_at = now(), error_code = $4
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, status)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

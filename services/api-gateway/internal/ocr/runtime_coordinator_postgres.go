package ocr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type postgresRuntimeCoordinator struct {
	source *PostgresStore
}

func (c *postgresRuntimeCoordinator) CreateTask(ctx context.Context, tenantID, submissionID, actorID string, input CreateTaskInput) (Task, error) {
	tx, err := c.source.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := createSourceTaskInTx(ctx, tx, tenantID, submissionID, actorID, input)
	if err != nil {
		return Task{}, err
	}
	if _, err = workerruntime.CreateTaskInTx(ctx, tx, tenantID, actorID, runtimeCreateInput(task, input.SourceFileAssetIDs...)); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

func (c *postgresRuntimeCoordinator) CompleteTask(ctx context.Context, tenantID, taskID string, input CompleteTaskInput) (Task, error) {
	if input.RuntimeTaskID == "" || input.RuntimeLeaseToken == "" {
		return Task{}, workerruntime.ErrInvalidInput
	}
	tx, err := c.source.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := completeSourceTaskInTx(ctx, tx, tenantID, taskID, input)
	if err != nil {
		return Task{}, err
	}
	// 与创建、失败处理及内存协调器保持相同的先来源、后运行时加锁顺序。运行时不匹配时回滚来源变更。
	if err = validateRuntimeSourceInTx(ctx, tx, tenantID, input.RuntimeTaskID, taskID); err != nil {
		return Task{}, err
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.RuntimeTaskID, runtimeCompleteInput(task, input.RuntimeLeaseToken)); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

func (c *postgresRuntimeCoordinator) FailTask(ctx context.Context, tenantID, taskID string, input FailTaskInput) (Task, error) {
	if input.RuntimeTaskID == "" || input.RuntimeLeaseToken == "" {
		return Task{}, workerruntime.ErrInvalidInput
	}
	tx, err := c.source.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := failSourceTaskInTx(ctx, tx, tenantID, taskID, input.ErrorMessage)
	if err != nil {
		return Task{}, err
	}
	if err = validateRuntimeSourceInTx(ctx, tx, tenantID, input.RuntimeTaskID, taskID); err != nil {
		return Task{}, err
	}
	if _, err = workerruntime.FailTaskInTx(ctx, tx, tenantID, input.RuntimeTaskID, workerruntime.FailInput{
		LeaseToken: input.RuntimeLeaseToken, Retryable: input.Retryable,
		ErrorCode: input.ErrorMessage, ErrorDetail: map[string]any{"source_type": "ocr_task"},
	}); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

func validateRuntimeSourceInTx(ctx context.Context, tx *sql.Tx, tenantID, runtimeTaskID, sourceTaskID string) error {
	var sourceType, sourceID string
	err := tx.QueryRowContext(ctx, `
SELECT source_type, source_id::text
FROM agent_worker_task
WHERE tenant_id = $1 AND id = $2::uuid
FOR UPDATE
`, tenantID, runtimeTaskID).Scan(&sourceType, &sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return workerruntime.ErrNotFound
	}
	if err != nil {
		return err
	}
	if sourceType != "ocr_task" || sourceID != sourceTaskID {
		return workerruntime.ErrInvalidInput
	}
	return nil
}

// SeedCompletedTask 是可信导入路径。它在一个事务中创建或复用 OCR 来源，
// 并同时记录来源结果与 succeeded 状态的运行时封装，避免重试看到只完成一侧的状态。
func (s *PostgresStore) SeedCompletedTask(ctx context.Context, tenantID, submissionID, actorID string, create CreateTaskInput, complete CompleteTaskInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := createSourceTaskInTx(ctx, tx, tenantID, submissionID, actorID, create)
	if err != nil {
		return Task{}, err
	}
	if task.Status == "queued" || task.Status == "failed" {
		task, err = startSourceTaskInTx(ctx, tx, tenantID, task.ID)
		if err != nil {
			return Task{}, err
		}
	}
	task, err = completeSourceTaskInTx(ctx, tx, tenantID, task.ID, complete)
	if err != nil {
		return Task{}, err
	}
	if _, err = workerruntime.CreateSucceededTaskInTx(
		ctx, tx, tenantID, actorID, runtimeCreateInput(task), runtimeCompleteInput(task, ""),
	); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// ReconcileCompletedRuntimeTask 为已完成但运行时封装未最终完成的历史 OCR 来源提供显式、幂等的恢复路径。
func (s *PostgresStore) ReconcileCompletedRuntimeTask(ctx context.Context, tenantID, actorID, taskID string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := getSourceTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status != "completed" {
		return Task{}, ErrInvalidTransition
	}
	task.Results, err = resultsFrom(ctx, tx, tenantID, task.ID)
	if err != nil {
		return Task{}, err
	}
	if err = reconcileCompletedRuntimeInTx(ctx, tx, tenantID, actorID, task); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// reconcileCompletedRuntimeInTx 比正常的可信导入路径更宽松。
// 在锁定并核验已完成的 OCR 来源后，它修复历史运行时封装，包括已耗尽或过期的尝试；
// 若之前存在不同的结果，仍保留冲突。
func reconcileCompletedRuntimeInTx(ctx context.Context, tx *sql.Tx, tenantID, actorID string, task Task) error {
	create := runtimeCreateInput(task)
	complete := runtimeCompleteInput(task, "")
	wantHash, err := workerruntime.ResultPayloadHash(complete.ResultSchemaVersion, complete.Result)
	if err != nil {
		return workerruntime.ErrInvalidInput
	}
	resultJSON, err := json.Marshal(complete.Result)
	if err != nil {
		return workerruntime.ErrInvalidInput
	}

	var runtimeTaskID, taskType, sourceType, sourceID, status, currentHash string
	err = tx.QueryRowContext(ctx, `
SELECT id::text, task_type, source_type, source_id::text, status,
       COALESCE(result_payload_hash, '')
FROM agent_worker_task
WHERE tenant_id = $1::uuid AND source_type = 'ocr_task' AND source_id = $2::uuid
ORDER BY created_at DESC
LIMIT 1
FOR UPDATE
`, tenantID, task.ID).Scan(&runtimeTaskID, &taskType, &sourceType, &sourceID, &status, &currentHash)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = workerruntime.CreateSucceededTaskInTx(ctx, tx, tenantID, actorID, create, complete)
		return err
	}
	if err != nil {
		return err
	}
	if taskType != "ocr" || sourceType != "ocr_task" || sourceID != task.ID {
		return workerruntime.ErrInvalidInput
	}
	if status == workerruntime.StatusSucceeded {
		if currentHash != wantHash {
			return workerruntime.ErrConflict
		}
		return nil
	}
	if !canReconcileRuntimeStatus(status) {
		return workerruntime.ErrInvalidTransition
	}

	result, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status = 'succeeded', result = $3::jsonb, result_schema_version = $4,
    result_payload_hash = $5, not_before = NULL, duration_ms = $6,
    completed_at = now(), cancelled_at = NULL, error_code = NULL,
    error_detail = '{}'::jsonb, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid
  AND source_type = 'ocr_task' AND source_id = $7::uuid
`, tenantID, runtimeTaskID, resultJSON, complete.ResultSchemaVersion, wantHash, complete.DurationMS, task.ID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return workerruntime.ErrInvalidTransition
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'succeeded', duration_ms = $3, completed_at = now(),
    error_code = NULL, error_detail = '{}'::jsonb
WHERE tenant_id = $1::uuid AND task_id = $2::uuid AND completed_at IS NULL
`, tenantID, runtimeTaskID, complete.DurationMS)
	return err
}

func canReconcileRuntimeStatus(status string) bool {
	switch status {
	case workerruntime.StatusQueued,
		workerruntime.StatusLeased,
		workerruntime.StatusRunning,
		workerruntime.StatusFailed,
		workerruntime.StatusDeadLetter:
		return true
	default:
		return false
	}
}

func startSourceTaskInTx(ctx context.Context, tx *sql.Tx, tenantID, taskID string) (Task, error) {
	row := tx.QueryRowContext(ctx, `
UPDATE ocr_task
SET status = 'processing', started_at = COALESCE(started_at, now()),
    completed_at = NULL, error_message = NULL, updated_at = now()
WHERE tenant_id = $1 AND id = $2::uuid AND status IN ('queued', 'failed') AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
`, tenantID, taskID)
	var task Task
	if err := scanTask(row, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

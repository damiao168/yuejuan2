package processing

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// Match the result coordinator's source-before-runtime lock order.
func (s *PostgresStore) RetryImageQuality(ctx context.Context, tenantID, runID, taskID string) (workerruntime.Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return workerruntime.Task{}, err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRowContext(ctx, `SELECT processing_status FROM submission_page_quality_run
WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return workerruntime.Task{}, ErrNotFound
	}
	if err != nil {
		return workerruntime.Task{}, err
	}
	if status != "terminal_error" && status != "retryable_error" {
		return workerruntime.Task{}, ErrRetryForbidden
	}
	task, err := workerruntime.RequeueTaskInTx(ctx, tx, tenantID, taskID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	if task.SourceType != "image_quality_run" || task.SourceID != runID {
		return workerruntime.Task{}, ErrRetryForbidden
	}
	_, err = tx.ExecContext(ctx, `UPDATE submission_page_quality_run
SET processing_status='retryable_error', lease_token=NULL, lease_expires_at=NULL,
worker_instance_id=NULL, completed_at=NULL, updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID)
	if err != nil {
		return workerruntime.Task{}, err
	}
	return task, tx.Commit()
}

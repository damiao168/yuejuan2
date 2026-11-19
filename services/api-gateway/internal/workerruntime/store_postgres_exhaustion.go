package workerruntime

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const exhaustionReason = "max_attempts_exhausted"

// ExhaustedSourceTask 是一次候选扫描结果。来源边界、最终期限和任务状态仍须在事务内重新核对。
type ExhaustedSourceTask struct {
	TenantID, ID, SourceType, SourceID string
	ReadyAt                            time.Time
}

// ListExhaustedSourceTasks 同时拾取到期任务和旧版已终结但来源仍活跃的记录。
// 维护调用方才可跨租户扫描。条件只覆盖采集解码及主观题评分。
func (s *PostgresStore) ListExhaustedSourceTasks(ctx context.Context, after ExhaustedSourceTask, limit int) ([]ExhaustedSourceTask, error) {
	// 扫描按逾期时间翻页，锁冲突或坏记录不会长期挡住后续考试。
	var cursorTime any
	if after.ID != "" {
		cursorTime = after.ReadyAt
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT task.tenant_id::text,task.id::text,task.source_type,task.source_id::text,
       COALESCE(task.lease_expires_at,task.not_before,task.updated_at)
FROM agent_worker_task task
JOIN tenant ON tenant.id=task.tenant_id AND tenant.status='active' AND tenant.deleted_at IS NULL
WHERE task.paper_import_run_id IS NULL AND task.source_type IN ('capture_file','subjective_grading_run')
  AND (
    (
      task.attempt_count >= task.max_attempts AND (
        (task.status='queued' AND (task.not_before IS NULL OR task.not_before <= statement_timestamp()))
        OR (task.status IN ('leased','running') AND task.lease_expires_at <= statement_timestamp())
      )
    )
    OR (task.status='dead_letter' AND task.error_detail->>'reason'=$2)
  )
  AND (
    (task.source_type='capture_file' AND task.task_type='capture_file_decode' AND EXISTS(
      SELECT 1 FROM capture_file f
      JOIN capture_batch b ON b.tenant_id=f.tenant_id AND b.id=f.capture_batch_id
      WHERE f.tenant_id=task.tenant_id AND f.id=task.source_id AND f.deleted_at IS NULL
        AND f.status IN ('queued','processing') AND b.deleted_at IS NULL AND b.status NOT IN ('cancelled','completed')
    ))
    OR (task.source_type='subjective_grading_run' AND task.task_type='ai_grade' AND EXISTS(
      SELECT 1 FROM subjective_grading_run r
      LEFT JOIN subjective_grading_batch b ON b.tenant_id=r.tenant_id AND b.id=r.batch_id
      WHERE r.tenant_id=task.tenant_id AND r.id=task.source_id AND r.deleted_at IS NULL AND r.status IN ('queued','processing')
        AND (r.batch_id IS NULL OR (b.status<>'cancelled' AND b.deleted_at IS NULL))
    ))
  )
  AND NOT EXISTS(
    SELECT 1 FROM agent_worker_task newer
    WHERE newer.tenant_id=task.tenant_id AND newer.source_type=task.source_type AND newer.source_id=task.source_id
      AND newer.task_type=task.task_type AND (newer.created_at,newer.id)>(task.created_at,task.id)
  )
  AND ($3::timestamptz IS NULL OR (COALESCE(task.lease_expires_at,task.not_before,task.updated_at),task.id)>($3,NULLIF($4,'')::uuid))
ORDER BY COALESCE(task.lease_expires_at,task.not_before,task.updated_at), task.id LIMIT $1
`, limit, exhaustionReason, cursorTime, after.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []ExhaustedSourceTask
	for rows.Next() {
		var task ExhaustedSourceTask
		if err := rows.Scan(&task.TenantID, &task.ID, &task.SourceType, &task.SourceID, &task.ReadyAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// FinishExhaustedSourceTaskInTx 由来源协调器在持有业务行锁后调用。已被别的操作取得的任务留到下一轮，
// 避免和心跳、重入队形成相反锁序。无错误码表示任务不需处理。
func FinishExhaustedSourceTaskInTx(ctx context.Context, tx *sql.Tx, candidate ExhaustedSourceTask) (code string, attempts int, err error) {
	if tx == nil {
		return "", 0, ErrInvalidInput
	}
	task, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+`
FROM agent_worker_task task
WHERE tenant_id=$1 AND id=$2::uuid AND source_type=$3 AND source_id=$4::uuid AND paper_import_run_id IS NULL
  AND NOT EXISTS(
    SELECT 1 FROM agent_worker_task newer
    WHERE newer.tenant_id=task.tenant_id AND newer.source_type=task.source_type AND newer.source_id=task.source_id
      AND newer.task_type=task.task_type AND (newer.created_at,newer.id)>(task.created_at,task.id)
  )
FOR UPDATE SKIP LOCKED`, candidate.TenantID, candidate.ID, candidate.SourceType, candidate.SourceID))
	if errors.Is(err, ErrNotFound) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if task.Status == StatusDeadLetter && task.ErrorDetail["reason"] == exhaustionReason {
		switch task.ErrorCode {
		case "lease_expired", exhaustionReason:
			return task.ErrorCode, task.AttemptCount, nil
		default:
			return exhaustionReason, task.AttemptCount, nil
		}
	}
	now, err := databaseNow(ctx, tx)
	if err != nil {
		return "", 0, err
	}
	queued := task.Status == StatusQueued && (task.NotBefore == nil || !task.NotBefore.After(now))
	expired := (task.Status == StatusLeased || task.Status == StatusRunning) && task.LeaseExpiresAt != nil && !task.LeaseExpiresAt.After(now)
	if task.AttemptCount < task.MaxAttempts || (!queued && !expired) {
		return "", 0, nil
	}
	code, err = deadLetterExhaustedInTx(ctx, tx, task.TenantID, task.ID, task.Status, now)
	return code, task.AttemptCount, err
}

func deadLetterExhaustedInTx(ctx context.Context, tx *sql.Tx, tenantID, taskID, status string, now time.Time) (string, error) {
	errorCode := exhaustionReason
	if status == StatusLeased || status == StatusRunning {
		errorCode = "lease_expired"
	}
	// 存量任务可能缺少未结束的尝试记录，仍需结算任务，以免阻塞同一队列。
	_, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'lease_expired', completed_at = $3, error_code = 'lease_expired',
  error_detail = '{"reason":"max_attempts_exhausted"}'::jsonb
WHERE tenant_id = $1 AND task_id = $2::uuid AND completed_at IS NULL
`, tenantID, taskID, now)
	if err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status = 'dead_letter', max_attempts = GREATEST(max_attempts, attempt_count),
  lease_token = NULL, lease_expires_at = NULL, leased_by = NULL,
  error_code = $3, error_detail = '{"reason":"max_attempts_exhausted"}'::jsonb,
  completed_at = $4, updated_at = $4, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
`, tenantID, taskID, errorCode, now)
	return errorCode, err
}

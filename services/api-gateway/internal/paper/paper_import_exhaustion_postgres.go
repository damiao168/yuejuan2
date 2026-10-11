package paper

import (
	"context"
	"database/sql"
	"errors"
)

// ReconcileExpiredPaperImportTask 将最后一次被放弃的尝试与所属业务运行一同结束。
// 通用运行时领取先锁任务后锁作业，无法安全执行此操作；此路径始终先锁作业和运行。
func (s *PostgresStore) ReconcileExpiredPaperImportTask(ctx context.Context) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if repaired, err := reconcileExpiredPaperImportDispatch(ctx, tx); err != nil {
		return false, err
	} else if repaired {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	var tenantID, importID, runID string
	err = tx.QueryRowContext(ctx, `SELECT j.tenant_id::text,j.id::text,r.id::text
FROM paper_import_job j JOIN paper_import_run r ON r.tenant_id=j.tenant_id AND r.paper_import_id=j.id AND r.generation=j.current_generation
WHERE j.status='processing' AND j.deleted_at IS NULL AND r.status='processing' AND EXISTS (
 SELECT 1 FROM agent_worker_task t WHERE t.tenant_id=r.tenant_id AND t.paper_import_run_id=r.id AND (
 t.status IN ('failed','dead_letter') OR (t.status IN ('leased','running') AND t.lease_expires_at<clock_timestamp() AND t.attempt_count>=t.max_attempts)))
ORDER BY j.created_at,j.id LIMIT 1 FOR UPDATE OF j,r SKIP LOCKED`).Scan(&tenantID, &importID, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var taskID, status string
	var attempt int
	err = tx.QueryRowContext(ctx, `SELECT id::text,status,attempt_count FROM agent_worker_task
WHERE tenant_id=$1 AND paper_import_run_id=$2::uuid AND (status IN ('failed','dead_letter') OR (status IN ('leased','running') AND lease_expires_at<clock_timestamp() AND attempt_count>=max_attempts))
ORDER BY created_at,id LIMIT 1 FOR UPDATE`, tenantID, runID).Scan(&taskID, &status, &attempt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if status != "failed" && status != "dead_letter" {
		result, err := tx.ExecContext(ctx, `UPDATE agent_worker_task_attempt SET status='lease_expired',completed_at=now(),error_code='lease_expired',error_detail='{"reason":"max_attempts_exhausted"}'::jsonb
WHERE tenant_id=$1 AND task_id=$2::uuid AND attempt_no=$3 AND completed_at IS NULL`, tenantID, taskID, attempt)
		if err != nil {
			return false, err
		}
		if rows, err := result.RowsAffected(); err != nil || rows != 1 {
			if err != nil {
				return false, err
			}
			return false, ErrConflict
		}
		if _, err := tx.ExecContext(ctx, `UPDATE agent_worker_task SET status='dead_letter',lease_token=NULL,lease_expires_at=NULL,leased_by=NULL,error_code='lease_expired',error_detail='{"reason":"max_attempts_exhausted"}'::jsonb,completed_at=now(),updated_at=now(),revision=revision+1 WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, taskID); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_job SET status='failed',error_code='paper_import_attempts_exhausted',issues='["处理执行者失联且重试次数已耗尽，请重新运行"]'::jsonb,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, importID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_run SET status='failed',error_code='paper_import_attempts_exhausted',error_detail=jsonb_build_object('task_id',$3::text),completed_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, runID, taskID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now() WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// reconcileExpiredPaperImportDispatch 处理 agent_worker_task 尚未创建的准备阶段。
// 崩溃进程无法调用正常失败路径，因此分发租约过期后必须安排有限重试，
// 或在最后一次尝试后原子地将当前业务运行标记为失败。
func reconcileExpiredPaperImportDispatch(ctx context.Context, tx *sql.Tx) (bool, error) {
	var tenantID, importID, runID string
	var generation int64
	var attempts int
	err := tx.QueryRowContext(ctx, `SELECT j.tenant_id::text,j.id::text,r.id::text,j.current_generation,r.dispatch_attempt_count
FROM paper_import_job j
JOIN paper_import_run r ON r.tenant_id=j.tenant_id AND r.paper_import_id=j.id AND r.generation=j.current_generation
WHERE j.status='processing' AND j.deleted_at IS NULL AND r.status='processing'
  AND r.dispatch_status IN ('pending','failed')
  AND r.dispatch_lease_owner IS NOT NULL AND r.dispatch_lease_expires_at<clock_timestamp()
ORDER BY r.dispatch_lease_expires_at,j.id
LIMIT 1 FOR UPDATE OF j,r SKIP LOCKED`).Scan(&tenantID, &importID, &runID, &generation, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if attempts < paperImportDispatchMaxAttempts {
		result, err := tx.ExecContext(ctx, `UPDATE paper_import_run
SET dispatch_status='failed',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,
    dispatch_available_at=now()+LEAST(dispatch_attempt_count,6)*interval '5 seconds',
    error_detail=jsonb_build_object('dispatch_error','preparation executor lease expired'),updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND status='processing'
  AND dispatch_lease_owner IS NOT NULL AND dispatch_lease_expires_at<clock_timestamp()`, tenantID, runID)
		if err != nil {
			return false, err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return false, err
		}
		if rows != 1 {
			return false, ErrConflict
		}
		return true, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_run
SET status='failed',dispatch_status='failed',dispatch_lease_owner=NULL,dispatch_lease_expires_at=NULL,
    error_code='paper_import_dispatch_exhausted',
    error_detail=jsonb_build_object('dispatch_error','preparation executor lease expired','attempts',$3::int),
    completed_at=now(),updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND status='processing'`, tenantID, runID, attempts); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE paper_import_job
SET status='failed',error_code='paper_import_dispatch_exhausted',
    issues='["资料准备执行者失联且重试次数已耗尽，请重新运行"]'::jsonb,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid AND current_generation=$3 AND status='processing'`, tenantID, importID, generation)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE paper_import_source SET processing_status='failed',updated_at=now()
WHERE tenant_id=$1 AND paper_import_id=$2::uuid AND deleted_at IS NULL`, tenantID, importID); err != nil {
		return false, err
	}
	return true, nil
}

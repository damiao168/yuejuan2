package subjective

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// ReconcileExhaustedGradingTask 只结束仍活跃的来源。先锁批次再锁运行，与整批取消的顺序一致；
// 单题结算用运行→任务锁序。任何一处写入失败都会撤销本次终结。
func (s *PostgresStore) ReconcileExhaustedGradingTask(ctx context.Context, candidate workerruntime.ExhaustedSourceTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var batchID, examID string
	err = tx.QueryRowContext(ctx, `
SELECT COALESCE(run.batch_id::text,''),sub.exam_id::text
FROM subjective_grading_run run
JOIN answer_segment seg ON seg.tenant_id=run.tenant_id AND seg.id=run.answer_segment_id
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
WHERE run.tenant_id=$1::uuid AND run.id=$2::uuid AND run.deleted_at IS NULL
`, candidate.TenantID, candidate.SourceID).Scan(&batchID, &examID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if batchID != "" {
		var status string
		err := tx.QueryRowContext(ctx, `
SELECT status FROM subjective_grading_batch
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE SKIP LOCKED
`, candidate.TenantID, batchID).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) || status == "cancelled" {
			return nil
		}
		if err != nil {
			return err
		}
	}
	var status string
	err = tx.QueryRowContext(ctx, `
SELECT status FROM subjective_grading_run
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE SKIP LOCKED
`, candidate.TenantID, candidate.SourceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || (status != RunQueued && status != RunProcessing) {
		return err
	}
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtext($1::uuid::text),hashtext($2::uuid::text))`, candidate.TenantID, examID).Scan(&locked); err != nil || !locked {
		return err
	}
	code, attempts, err := workerruntime.FinishExhaustedSourceTaskInTx(ctx, tx, candidate)
	if err != nil || code == "" {
		return err
	}
	if _, err = updateRunInDB(ctx, tx, candidate.TenantID, candidate.SourceID, UpdateRunInput{Status: RunFailed, ErrorCode: code, AttemptCount: attempts}); err != nil {
		return err
	}
	if batchID != "" {
		if _, err = refreshBatchInDB(ctx, tx, candidate.TenantID, batchID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

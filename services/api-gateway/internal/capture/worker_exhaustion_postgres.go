package capture

import (
	"context"
	"database/sql"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

// ReconcileExhaustedFileTask 同时结算耗尽的尝试、文件和批次，保持普通解码回执的批次→文件→任务锁序。
func (s *PostgresStore) ReconcileExhaustedFileTask(ctx context.Context, candidate workerruntime.ExhaustedSourceTask) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var examID, batchID, batchStatus string
	err = tx.QueryRowContext(ctx, `
SELECT b.exam_id::text,b.id::text,b.status
FROM capture_batch b JOIN capture_file f ON f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id
WHERE f.tenant_id=$1 AND f.id=$2::uuid AND f.deleted_at IS NULL AND b.deleted_at IS NULL
FOR UPDATE OF b SKIP LOCKED`, candidate.TenantID, candidate.SourceID).Scan(&examID, &batchID, &batchStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if batchStatus == "completed" || batchStatus == "cancelled" {
		return nil
	}
	var fileStatus string
	err = tx.QueryRowContext(ctx, `
SELECT status FROM capture_file
WHERE tenant_id=$1 AND id=$2::uuid AND capture_batch_id=$3::uuid AND deleted_at IS NULL
FOR UPDATE SKIP LOCKED`, candidate.TenantID, candidate.SourceID, batchID).Scan(&fileStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil || (fileStatus != "queued" && fileStatus != "processing") {
		return err
	}
	// 文件更新会触发考试归档检查。遇到收卷或归档持锁便交给下一轮，避免持有文件锁等待考试锁。
	if err := tryCollectionExamLock(ctx, tx, candidate.TenantID, examID); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil
		}
		return err
	}
	code, _, err := workerruntime.FinishExhaustedSourceTaskInTx(ctx, tx, candidate)
	if err != nil || code == "" {
		return err
	}
	if _, err = s.applyFileFailureInTx(ctx, tx, candidate.TenantID, candidate.SourceID, code, false); err != nil {
		return err
	}
	return tx.Commit()
}

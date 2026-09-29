package capture

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) createBatchCommand(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "capture-batch:"+tenantID+":"+actorID+":"+input.IdempotencyKey); err != nil {
		return Batch{}, err
	}
	// Include deleted rows and legacy commands. Ambiguous legacy history is a
	// conflict, never permission to create an additional resource.
	rows, err := tx.QueryContext(ctx, `SELECT id::text FROM capture_batch WHERE tenant_id=$1::uuid AND operator_id=$2::uuid AND idempotency_key=$3 ORDER BY created_at,id LIMIT 2`, tenantID, actorID, input.IdempotencyKey)
	if err != nil {
		return Batch{}, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return Batch{}, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return Batch{}, err
	}
	if len(ids) > 1 {
		return Batch{}, ErrConflict
	}
	hash := batchCommandHash(examID, input)
	// 先恢复已接受命令，再检查当前考试状态；考试后来离开采集阶段也应能找回原批次。
	if len(ids) == 1 {
		batch, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, ids[0]))
		if err != nil {
			return Batch{}, err
		}
		var storedHash string
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(command_request_hash,'') FROM capture_batch WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, batch.ID).Scan(&storedHash); err != nil {
			return Batch{}, err
		}
		if storedHash == "" {
			storedHash = batchCommandHash(batch.ExamID, CreateBatchInput{Name: batch.Name, SourceType: batch.SourceType, ScannerDevice: batch.ScannerDevice})
		}
		if storedHash != hash {
			return Batch{}, ErrConflict
		}
		return batch, tx.Commit()
	}
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Batch{}, ErrNotFound
		}
		return Batch{}, err
	}
	if status != "collecting" {
		return Batch{}, ErrInvalidTransition
	}
	batch, err := scanBatch(tx.QueryRowContext(ctx, `INSERT INTO capture_batch(tenant_id,exam_id,name,source_type,operator_id,scanner_device,idempotency_key,command_request_hash) VALUES($1::uuid,$2::uuid,$3,$4,$5::uuid,NULLIF($6,''),$7,$8) RETURNING `+batchColumns, tenantID, examID, input.Name, input.SourceType, actorID, input.ScannerDevice, input.IdempotencyKey, hash))
	if err != nil {
		return Batch{}, err
	}
	return batch, tx.Commit()
}

func (s *PostgresStore) RecoverBatchCommand(ctx context.Context, tenantID, examID, actorID, commandID string) (BatchCommandRecovery, error) {
	result := BatchCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	rows, err := s.db.QueryContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND operator_id=$3::uuid AND idempotency_key=$4 ORDER BY created_at,id LIMIT 2`, tenantID, examID, actorID, commandID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		if result.Batch != nil {
			return BatchCommandRecovery{}, ErrConflict
		}
		batch, err := scanBatch(rows)
		if err != nil {
			return result, err
		}
		result.Batch = &batch
		result.Status = "succeeded"
	}
	return result, rows.Err()
}

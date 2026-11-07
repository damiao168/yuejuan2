package capture

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (s *PostgresStore) RegisterFile(ctx context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error) {
	if validateRegisterFile(&input, asset) != nil {
		return File{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return File{}, err
	}
	var examID, status string
	if err := tx.QueryRowContext(ctx, `SELECT exam_id::text, status FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&examID, &status); err != nil {
		return File{}, mapNotFound(err)
	}
	if status == "completed" || status == "cancelled" {
		return File{}, ErrInvalidTransition
	}
	if asset.ExamID != "" && asset.ExamID != examID {
		return File{}, ErrInvalidInput
	}
	if err = validateUploadGroupTx(ctx, tx, tenantID, batchID, input); err != nil {
		return File{}, err
	}
	duplicateStatus := "uploaded"
	var duplicateID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM capture_file
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND sha256=$3
  AND status IN ('uploaded','queued','processing','completed')
  AND deleted_at IS NULL
LIMIT 1`, tenantID, batchID, asset.SHA256).Scan(&duplicateID)
	if err == nil {
		duplicateStatus = "duplicate"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return File{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO capture_file (tenant_id, capture_batch_id, file_asset_id, original_name, content_type, sha256, byte_size, status, idempotency_key, uploaded_by,upload_group_id,upload_group_file_count,upload_group_file_index,pages_per_student)
VALUES ($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10::uuid,NULLIF($11,'')::uuid,NULLIF($12,0),NULLIF($13,0),NULLIF($14,0))
ON CONFLICT (tenant_id, capture_batch_id, idempotency_key)
DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
RETURNING `+fileColumns, tenantID, batchID, asset.ID, asset.OriginalName, asset.ContentType, asset.SHA256, asset.SizeBytes, duplicateStatus, input.IdempotencyKey, actorID, input.UploadGroupID, input.UploadGroupFileCount, input.UploadGroupFileIndex, input.PagesPerStudent)
	item, err := scanFile(row)
	if err != nil {
		return File{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE capture_batch SET status=CASE WHEN status='draft' THEN 'uploading' ELSE status END,
file_count=(SELECT count(*) FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL),
revision=revision+1, updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, batchID)
	if err != nil {
		return File{}, err
	}
	return item, tx.Commit()
}

func (s *PostgresStore) ListFiles(ctx context.Context, tenantID, batchID string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at`, tenantID, batchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		item, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetFile(ctx context.Context, tenantID, fileID string) (File, error) {
	return scanFile(s.db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, fileID))
}

func (s *PostgresStore) ApplyFileFailure(ctx context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return File{}, err
	}
	defer tx.Rollback()
	if err = lockFileBatchTx(ctx, tx, tenantID, fileID); err != nil {
		return File{}, err
	}
	out, err := s.applyFileFailureInTx(ctx, tx, tenantID, fileID, errorCode, retryable)
	if err != nil {
		return File{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) applyFileFailureInTx(ctx context.Context, tx *sql.Tx, tenantID, fileID, errorCode string, retryable bool) (File, error) {
	status := "failed"
	if retryable {
		status = "queued"
	}
	var batchID string
	if err := tx.QueryRowContext(ctx, `SELECT capture_batch_id::text FROM capture_file WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, fileID).Scan(&batchID); err != nil {
		return File{}, mapNotFound(err)
	}
	out, err := scanFile(tx.QueryRowContext(ctx, `UPDATE capture_file SET status=$3,error_code=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL RETURNING `+fileColumns, tenantID, fileID, status, strings.TrimSpace(errorCode)))
	if err != nil {
		return File{}, err
	}
	if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return File{}, err
	}
	return out, nil
}

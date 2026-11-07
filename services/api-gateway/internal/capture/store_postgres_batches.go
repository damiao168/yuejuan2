package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) CreateBatch(ctx context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error) {
	if tenantID == "" || examID == "" || actorID == "" || validateCreateBatch(&input) != nil {
		return Batch{}, ErrInvalidInput
	}
	if input.IdempotencyKey != "" {
		return s.createBatchCommand(ctx, tenantID, examID, actorID, input)
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO capture_batch (tenant_id, exam_id, name, source_type, operator_id, scanner_device, idempotency_key,purpose,supplement_reason)
VALUES ($1, $2::uuid, $3, $4, $5::uuid, NULLIF($6, ''), NULLIF($7, ''),$8,NULLIF($9,''))
ON CONFLICT (tenant_id, exam_id, idempotency_key) WHERE idempotency_key IS NOT NULL AND idempotency_key <> '' AND deleted_at IS NULL
DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
RETURNING `+batchColumns, tenantID, examID, input.Name, input.SourceType, actorID, input.ScannerDevice, input.IdempotencyKey, input.Purpose, input.Reason)
	return scanBatch(row)
}

func (s *PostgresStore) ListBatches(ctx context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+batchColumns+` FROM capture_batch
WHERE tenant_id = $1 AND exam_id = $2::uuid AND deleted_at IS NULL
  AND ($4 = '' OR created_at < $3 OR (created_at = $3 AND id::text < $4))
ORDER BY created_at DESC, id::text DESC
LIMIT NULLIF($5, 0)`, tenantID, examID, filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Batch{}
	for rows.Next() {
		item, err := scanBatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetBatch(ctx context.Context, tenantID, batchID string) (Batch, error) {
	return scanBatch(s.db.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch
WHERE tenant_id = $1 AND id = $2::uuid AND deleted_at IS NULL`, tenantID, batchID))
}

// 事务锁住批次和待处理文件；失败文件会重置其任务租约，避免重试留下旧租约或重复页面。
func (s *PostgresStore) QueueBatch(ctx context.Context, tenantID, batchID, actorID string) (Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return Batch{}, err
	}
	var current, examID string
	if err := tx.QueryRowContext(ctx, `SELECT status, exam_id::text FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&current, &examID); err != nil {
		return Batch{}, mapNotFound(err)
	}
	if current == "completed" || current == "cancelled" {
		return Batch{}, ErrInvalidTransition
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+fileColumns+` FROM capture_file
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND status IN ('uploaded','failed')
  AND deleted_at IS NULL
ORDER BY created_at
FOR UPDATE`, tenantID, batchID)
	if err != nil {
		return Batch{}, err
	}
	files := []File{}
	for rows.Next() {
		item, err := scanFile(rows)
		if err != nil {
			rows.Close()
			return Batch{}, err
		}
		files = append(files, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Batch{}, err
	}
	if err := rows.Close(); err != nil {
		return Batch{}, err
	}
	if len(files) == 0 {
		return Batch{}, ErrInvalidTransition
	}
	for _, item := range files {
		payload, _ := json.Marshal(map[string]any{"capture_file_id": item.ID, "file_asset_id": item.FileAssetID, "exam_id": examID, "download_url": "/api/v1/files/" + item.FileAssetID + "/download", "content_type": item.ContentType, "sha256": item.SHA256, "max_pages": 500, "render_dpi": 300})
		key := "capture-decode:" + item.ID + ":v1"
		if item.Status == "failed" {
			result, retryErr := tx.ExecContext(ctx, `UPDATE agent_worker_task
SET status='queued',
    not_before=NULL,
    lease_token=NULL,
    lease_expires_at=NULL,
    leased_by=NULL,
    completed_at=NULL,
    max_attempts=GREATEST(max_attempts,attempt_count+1),
    updated_at=now()
WHERE tenant_id=$1
  AND source_type='capture_file'
  AND source_id=$2::uuid
  AND status IN ('failed','dead_letter')`, tenantID, item.ID)
			if retryErr != nil {
				return Batch{}, retryErr
			}
			affected, retryErr := result.RowsAffected()
			if retryErr != nil {
				return Batch{}, retryErr
			}
			if affected != 1 {
				return Batch{}, ErrConflict
			}
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1,'capture_file_decode','page-processing','capture_file',$2::uuid,50,$3,'capture-file-decode-v1',$4,$4,5,5,NULLIF($5,'')::uuid)
ON CONFLICT (tenant_id,task_type,idempotency_key) DO NOTHING`, tenantID, item.ID, payload, key, actorID)
			if err != nil {
				return Batch{}, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE capture_file SET status='queued',error_code=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, item.ID); err != nil {
			return Batch{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `UPDATE capture_batch SET status='processing',started_at=COALESCE(started_at,now()),revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+batchColumns, tenantID, batchID)
	out, err := scanBatch(row)
	if err != nil {
		return Batch{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) SetBatchStatus(ctx context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	current, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID))
	if err != nil {
		return Batch{}, err
	}
	// 完成时重算当前事实，不能仅相信先前缓存的 ready 状态。
	if status == "completed" {
		if err = aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
			return Batch{}, err
		}
		current, err = scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, batchID))
		if err != nil {
			return Batch{}, err
		}
	}
	if !canSetBatchStatus(current.Status, status) {
		return Batch{}, ErrInvalidTransition
	}
	out, err := scanBatch(tx.QueryRowContext(ctx, `UPDATE capture_batch SET status=$3,revision=revision+1,completed_at=CASE WHEN $3='completed' THEN now() ELSE NULL END,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+batchColumns, tenantID, batchID, status))
	if err != nil {
		return Batch{}, err
	}
	before, _ := json.Marshal(current)
	after, _ := json.Marshal(out)
	op := status
	if status == "completed" {
		op = "complete"
	}
	if status == "draft" {
		op = "reopen"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO capture_operation (tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES ($1,$2::uuid,$3,'capture_batch',$2::uuid,$4::uuid,NULLIF($5,''),$6,$7)`, tenantID, batchID, op, actorID, strings.TrimSpace(reason), before, after)
	if err != nil {
		return Batch{}, err
	}
	return out, tx.Commit()
}

// 聚合状态按文件失败、页面质量、身份匹配和页面就绪的优先级计算，供批次列表统一展示。
func aggregateBatchTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE capture_batch b SET
file_count=(SELECT count(*) FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.deleted_at IS NULL),
page_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL),
submission_count=(SELECT count(DISTINCT p.submission_id) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL),
normal_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status='ready' AND p.deleted_at IS NULL),
failed_count=(SELECT count(*) FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status='failed' AND f.deleted_at IS NULL),
review_count=(SELECT count(*) FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status IN ('needs_review','quality_rejected','failed') AND p.deleted_at IS NULL),
status=CASE
 WHEN EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status IN ('uploaded','queued','processing') AND f.deleted_at IS NULL) THEN 'processing'
 WHEN EXISTS(SELECT 1 FROM capture_file f WHERE f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id AND f.status='failed' AND f.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status IN ('needs_review','quality_rejected','failed') AND p.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND s.identity_status IN ('unknown','conflict') AND p.deleted_at IS NULL) THEN 'needs_review'
 WHEN EXISTS(SELECT 1 FROM capture_page p JOIN submission s ON s.tenant_id=p.tenant_id AND s.id=p.submission_id WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND s.identity_status<>'matched' AND p.deleted_at IS NULL) THEN 'matching'
 WHEN EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status<>'deleted' AND p.deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM capture_page p WHERE p.tenant_id=b.tenant_id AND p.capture_batch_id=b.id AND p.status NOT IN ('ready','deleted') AND p.deleted_at IS NULL) THEN 'ready'
 ELSE 'matching' END,
revision=revision+1,updated_at=now() WHERE b.tenant_id=$1 AND b.id=$2::uuid AND b.status NOT IN ('completed','cancelled')`, tenantID, batchID)
	return err
}

func (s *PostgresStore) aggregateBatchTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	return aggregateBatchTx(ctx, tx, tenantID, batchID)
}

func ensureBatchWritableTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID).Scan(&status); err != nil {
		return mapNotFound(err)
	}
	if status == "completed" || status == "cancelled" {
		return ErrInvalidTransition
	}
	return nil
}

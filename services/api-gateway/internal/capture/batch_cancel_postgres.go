package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

const batchCancellationCode = "capture_batch_cancelled"

func (s *PostgresStore) cancelBatch(ctx context.Context, tenantID, batchID, actorID, reason string) (Batch, bool, error) {
	reason = strings.TrimSpace(reason)
	if actorID == "" || len(reason) > 500 {
		return Batch{}, false, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, false, err
	}
	defer tx.Rollback()
	batch, err := scanBatch(tx.QueryRowContext(ctx, `SELECT `+batchColumns+` FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, batchID))
	if err != nil {
		return Batch{}, false, err
	}
	if batch.Status == "cancelled" {
		// 没有独立命令键；仅确认当前取消修订的同一操作者和理由，且不重复记录审计或变更材料。
		var replay bool
		err = tx.QueryRowContext(ctx, `
SELECT EXISTS(SELECT 1 FROM capture_operation
 WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND target_type='capture_batch' AND target_id=$2::uuid
   AND operation='cancel' AND actor_id=$3::uuid AND after_state->>'revision'=$4::int::text
   AND COALESCE(reason,'')=$5)`, tenantID, batchID, actorID, batch.Revision, reason).Scan(&replay)
		if err != nil {
			return Batch{}, false, err
		}
		if !replay {
			return Batch{}, false, ErrConflict
		}
		return batch, false, nil
	}
	if !canSetBatchStatus(batch.Status, "cancelled") {
		return Batch{}, false, ErrInvalidTransition
	}
	if err = cancelBatchMaterialsTx(ctx, tx, batch, actorID, reason); err != nil {
		return Batch{}, false, err
	}
	out, err := scanBatch(tx.QueryRowContext(ctx, `
UPDATE capture_batch SET status='cancelled',file_count=0,page_count=0,submission_count=0,
 normal_count=0,review_count=0,failed_count=0,revision=revision+1,completed_at=NULL,updated_at=now()
WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+batchColumns, tenantID, batchID))
	if err != nil {
		return Batch{}, false, err
	}
	before, _ := json.Marshal(batch)
	after, _ := json.Marshal(out)
	// 业务状态是 cancelled，操作台账枚举用 cancel。回执和材料退出必须同一事务提交。
	if _, err = tx.ExecContext(ctx, `
INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state)
VALUES($1,$2::uuid,'cancel','capture_batch',$2::uuid,$3::uuid,NULLIF($4,''),$5,$6)`, tenantID, batchID, actorID, reason, before, after); err != nil {
		return Batch{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return Batch{}, false, err
	}
	return out, true, nil
}

// 持有批次锁。分块登记和页面回执可能已经锁住自己的行，先尝试取锁再写入，避免跟它们反向等待。
func cancelBatchMaterialsTx(ctx context.Context, tx *sql.Tx, batch Batch, actorID, reason string) error {
	args := []any{batch.TenantID, batch.ID}
	for _, query := range []string{
		`SELECT id FROM capture_upload_session WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND status IN ('uploading','finalizing','failed') ORDER BY id FOR UPDATE NOWAIT`,
		`SELECT id FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE NOWAIT`,
		`SELECT id FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted' ORDER BY id FOR UPDATE NOWAIT`,
		`SELECT s.id FROM submission s WHERE s.tenant_id=$1 AND s.deleted_at IS NULL AND s.id IN (SELECT p.submission_id FROM (` + batchCancellationActivePages + `) p) ORDER BY s.id FOR UPDATE NOWAIT`,
		`SELECT sp.id FROM submission_page sp WHERE sp.tenant_id=$1 AND sp.deleted_at IS NULL AND sp.id IN (SELECT p.submission_page_id FROM (` + batchCancellationActivePages + `) p) ORDER BY sp.id FOR UPDATE NOWAIT`,
	} {
		if err := lockBatchCancellationRows(ctx, tx, query, args...); err != nil {
			return err
		}
	}
	// 成绩、归档保护和收卷修订也使用考试事务锁。遇忙回滚，让已经取得业务行的写入先完成。
	if err := tryCollectionExamLock(ctx, tx, batch.TenantID, batch.ExamID); err != nil {
		return err
	}

	// 只保护实际退出的提交页。若同一提交页仍由另一批引用，保留其评分和关联任务。
	if err := ensureBatchCancellationUngradedTx(ctx, tx, args...); err != nil {
		return err
	}
	if err := cancelBatchWorkersTx(ctx, tx, args...); err != nil {
		return err
	}
	// 同一答卷可能含另一批次或直接上传的页面。只撤销本批独有的提交页，再按剩余页面更新页数。
	if _, err := tx.ExecContext(ctx, `
WITH retired_pages AS (`+batchCancellationPageIDs+`), retired_segments AS (
  UPDATE answer_segment SET processing_status='invalidated',deleted_at=now(),updated_at=now()
  WHERE tenant_id=$1 AND submission_page_id IN (SELECT id FROM retired_pages) AND deleted_at IS NULL
)
UPDATE submission_page SET deleted_at=now(),updated_at=now()
WHERE tenant_id=$1 AND id IN (SELECT id FROM retired_pages) AND deleted_at IS NULL`, args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission s SET actual_page_count=remaining.pages,
  deleted_at=CASE WHEN remaining.pages=0 THEN now() ELSE s.deleted_at END,updated_at=now()
FROM (
  SELECT p.submission_id,count(DISTINCT sp.id)::int pages
  FROM (`+batchCancellationActivePages+`) p
  LEFT JOIN submission_page sp ON sp.tenant_id=$1 AND sp.submission_id=p.submission_id AND sp.deleted_at IS NULL
  WHERE p.submission_id IS NOT NULL GROUP BY p.submission_id
) remaining
WHERE s.tenant_id=$1 AND s.id=remaining.submission_id AND s.deleted_at IS NULL
  AND (remaining.pages=0 OR s.actual_page_count IS DISTINCT FROM remaining.pages)`, args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE capture_page SET status='deleted',deleted_at=now(),revision=revision+1,updated_at=now()
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted'`, args...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE capture_file SET exclusion_reason=NULLIF($3,''),excluded_by=$4::uuid,deleted_at=now(),updated_at=now()
WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL`, batch.TenantID, batch.ID, reason, actorID); err != nil {
		return err
	}

	// 完成的会话保留原回执。其余会话停止续传，旧令牌不得在批次重开后继续使用。
	_, err := tx.ExecContext(ctx, `
WITH stopped AS (
  UPDATE capture_upload_session SET status='failed',error_code=$3,completion_token=NULL,
    completion_lease_until=NULL,updated_at=now()
  WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND status IN ('uploading','finalizing','failed')
    AND error_code IS DISTINCT FROM $3
  RETURNING id
)
DELETE FROM capture_upload_chunk WHERE tenant_id=$1 AND capture_upload_session_id IN (SELECT id FROM stopped)`, batch.TenantID, batch.ID, batchCancellationCode)
	return err
}

// 旧取消只改批次状态，仍有效的文件和采集页需要补偿退出。已经独立排除的历史页不再拥有提交页。
const batchCancellationActivePages = `
  SELECT p.submission_id,p.submission_page_id FROM capture_page p
  JOIN capture_file f ON f.tenant_id=p.tenant_id AND f.id=p.capture_file_id AND f.deleted_at IS NULL
  WHERE p.tenant_id=$1 AND p.capture_batch_id=$2::uuid AND p.deleted_at IS NULL AND p.status<>'deleted'
`

const batchCancellationPageIDs = `
  SELECT DISTINCT p.submission_page_id id FROM (` + batchCancellationActivePages + `) p
  JOIN submission_page sp ON sp.tenant_id=$1 AND sp.id=p.submission_page_id AND sp.deleted_at IS NULL
  WHERE NOT EXISTS(
      SELECT 1 FROM capture_page other JOIN capture_file f ON f.tenant_id=other.tenant_id AND f.id=other.capture_file_id AND f.deleted_at IS NULL
      JOIN capture_batch b ON b.tenant_id=other.tenant_id AND b.id=other.capture_batch_id AND b.deleted_at IS NULL AND b.status<>'cancelled'
      WHERE other.tenant_id=$1 AND other.submission_page_id=p.submission_page_id
        AND other.capture_batch_id<>$2::uuid AND other.deleted_at IS NULL AND other.status<>'deleted'
    )
`

func ensureBatchCancellationUngradedTx(ctx context.Context, tx *sql.Tx, args ...any) error {
	byPage := ` FROM answer_segment a WHERE a.tenant_id=$1 AND a.submission_page_id IN (` + batchCancellationPageIDs + `)`
	// 成绩写入触发器会锁定有效题块；先锁后检查，防止刚通过校验又产生新成绩。
	if err := lockBatchCancellationRows(ctx, tx, `SELECT a.id`+byPage+` ORDER BY a.id FOR UPDATE NOWAIT`, args...); err != nil {
		return err
	}
	var graded bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+byPage+` AND (`+answerSegmentHasGradingUse+`))`, args...).Scan(&graded)
	if err != nil {
		return err
	}
	if graded {
		return ErrFileHasGrades
	}
	return nil
}

const batchCancellationTasks = `
  SELECT f.id AS source_id,'capture_file' AS source_type FROM capture_file f WHERE f.tenant_id=$1 AND f.capture_batch_id=$2::uuid
  UNION ALL
  SELECT r.id,'page_registration_run' FROM page_registration_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE p.tenant_id=$1 AND p.capture_batch_id=$2::uuid
  UNION ALL
  SELECT r.id,'page_template_match_run' FROM page_template_match_run r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE p.tenant_id=$1 AND p.capture_batch_id=$2::uuid
  UNION ALL
  SELECT r.id,'page_registration_correction' FROM page_registration_correction r JOIN capture_page p ON p.tenant_id=r.tenant_id AND p.id=r.capture_page_id WHERE p.tenant_id=$1 AND p.capture_batch_id=$2::uuid
  UNION ALL
  SELECT r.id,'image_quality_run' FROM submission_page_quality_run r
  WHERE r.tenant_id=$1 AND r.submission_page_id IN (` + batchCancellationPageIDs + `)`

func cancelBatchWorkersTx(ctx context.Context, tx *sql.Tx, args ...any) error {
	rows, err := tx.QueryContext(ctx, `
SELECT t.id FROM agent_worker_task t
WHERE t.tenant_id=$1 AND t.status IN ('queued','leased','running')
  AND (t.source_id,t.source_type) IN (`+batchCancellationTasks+`)
ORDER BY t.id FOR UPDATE NOWAIT`, args...)
	if err != nil {
		return batchCancellationLockError(err)
	}
	var tasks []string
	for rows.Next() {
		var task string
		if err = rows.Scan(&task); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, task)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(tasks) == 0 {
		return batchCancellationLockError(err)
	}
	// 租约和尝试记录一同关闭。迟到回执即使已经开始写库，也无法通过最后的租约校验。
	_, err = tx.ExecContext(ctx, `
WITH stopped AS (
  UPDATE agent_worker_task SET status='cancelled',lease_token=NULL,lease_expires_at=NULL,leased_by=NULL,
    completed_at=now(),cancelled_at=now(),updated_at=now(),revision=revision+1,error_code=$3
  WHERE tenant_id=$1 AND id=ANY($2::uuid[]) RETURNING id
)
UPDATE agent_worker_task_attempt SET status='cancelled',completed_at=now(),error_code=$3
WHERE tenant_id=$1 AND task_id IN (SELECT id FROM stopped) AND completed_at IS NULL`, args[0], tasks, batchCancellationCode)
	return err
}

func lockBatchCancellationRows(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return batchCancellationLockError(err)
	}
	defer rows.Close()
	for rows.Next() {
	}
	return batchCancellationLockError(rows.Err())
}

func batchCancellationLockError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "55P03" {
		return ErrConflict
	}
	return err
}

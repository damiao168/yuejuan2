package paper

import (
	"context"
	"database/sql"
	"errors"
)

// 任务创建、领取和结果提交先取得与整场归档相同的考试锁，再锁任务行。
// 业务 status 不包含管理归档，必须同时读取学科及父场次；等待锁之后用新语句读取当前事实。
func lockRubricDraftExamTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) (string, bool, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, tenantID, examID); err != nil {
		return "", false, err
	}
	var status string
	var archived bool
	err := tx.QueryRowContext(ctx, `SELECT e.status,e.archived_at IS NOT NULL OR e.status='archived'
  OR COALESCE(parent.archived_at IS NOT NULL OR parent.status='archived',false)
FROM exam e LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL`, tenantID, examID).Scan(&status, &archived)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, ErrNotFound
	}
	return status, archived, err
}

func (s *PostgresStore) cleanupExpiredRubricDraftJobs(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(674218519)`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT tenant_id::text,exam_id::text FROM question_rubric_draft_job
WHERE status IN ('running','cancelling') AND lease_until<=now() ORDER BY tenant_id::text,exam_id::text`)
	if err != nil {
		return err
	}
	var exams [][2]string
	for rows.Next() {
		var item [2]string
		if err := rows.Scan(&item[0], &item[1]); err != nil {
			rows.Close()
			return err
		}
		exams = append(exams, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, item := range exams {
		_, archived, err := lockRubricDraftExamTx(ctx, tx, item[0], item[1])
		if err != nil {
			return err
		}
		if archived {
			continue
		}
		// 可能已计费的调用不能重放。清理单独提交，避免持有多个考试锁时再领取另一场考试造成锁序反转。
		if _, err := tx.ExecContext(ctx, `UPDATE question_rubric_draft_job SET status=CASE WHEN status='cancelling' THEN 'cancelled' ELSE 'failed' END,
payload=jsonb_set(payload,'{Job,error}',to_jsonb('执行中断，请手动重试；上次调用可能已计费'::text)),lease_token='',lease_until=NULL,updated_at=now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status IN ('running','cancelling') AND lease_until<=now()`, item[0], item[1]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

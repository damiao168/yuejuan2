package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const rubricDraftJobColumns = `payload,status,lease_token,lease_until,available_at,updated_at`

func scanRubricDraftJob(scanner interface{ Scan(...any) error }) (rubricDraftJobRecord, error) {
	var record rubricDraftJobRecord
	var payload []byte
	var status, token string
	var lease sql.NullTime
	var available, updated time.Time
	err := scanner.Scan(&payload, &status, &token, &lease, &available, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	if err = json.Unmarshal(payload, &record); err != nil {
		return record, err
	}
	record.Job.Status, record.Job.UpdatedAt = status, updated
	record.LeaseToken, record.LeaseUntil, record.AvailableAt = token, lease.Time, available
	return record, nil
}

func (s *PostgresStore) createRubricDraftJob(ctx context.Context, record rubricDraftJobRecord, regenerate bool) (RubricDraftJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RubricDraftJob{}, err
	}
	defer tx.Rollback()
	stage, archived, err := lockRubricDraftExamTx(ctx, tx, record.TenantID, record.Job.ExamID)
	if err != nil {
		return RubricDraftJob{}, err
	}
	if archived || (stage != "draft" && stage != "configured") {
		return RubricDraftJob{}, ErrExamFrozen
	}
	// 所有副本在同一事务中完成去重、排队额度检查和创建，重复点击不会重复付费。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(674218518)`); err != nil {
		return RubricDraftJob{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+rubricDraftJobColumns+` FROM question_rubric_draft_job WHERE tenant_id=$1 AND actor_id=$2 AND (question_id=$3 OR request_id=$4) ORDER BY created_at DESC`, record.TenantID, record.ActorID, record.Job.QuestionID, record.Job.RequestID)
	if err != nil {
		return RubricDraftJob{}, err
	}
	var records []rubricDraftJobRecord
	for rows.Next() {
		item, scanErr := scanRubricDraftJob(rows)
		if scanErr != nil {
			rows.Close()
			return RubricDraftJob{}, scanErr
		}
		records = append(records, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return RubricDraftJob{}, err
	}
	if existing, findErr := existingRubricDraftJob(records, record, regenerate); existing != nil || findErr != nil {
		if existing != nil {
			return *existing, tx.Commit()
		}
		return RubricDraftJob{}, findErr
	}
	var waiting int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM question_rubric_draft_job WHERE tenant_id=$1 AND actor_id=$2 AND status='queued'`, record.TenantID, record.ActorID).Scan(&waiting); err != nil {
		return RubricDraftJob{}, err
	}
	if waiting >= rubricDraftQueueLimit {
		return RubricDraftJob{}, ErrRubricDraftQueueFull
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return RubricDraftJob{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO question_rubric_draft_job(id,tenant_id,actor_id,exam_id,question_id,request_id,fingerprint,status,local_model,payload,available_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'queued',$8,$9,$10,$10,$10)`, record.Job.ID, record.TenantID, record.ActorID, record.Job.ExamID, record.Job.QuestionID, record.Job.RequestID, record.Fingerprint, record.Local, payload, record.Job.CreatedAt)
	if err != nil {
		return RubricDraftJob{}, err
	}
	return record.Job, tx.Commit()
}

func (s *PostgresStore) listRubricDraftJobs(ctx context.Context, tenantID, actorID, examID string) ([]RubricDraftJob, error) {
	// 活跃任务优先，总数最多 100 条；单个用户的等待额度加并发上限小于这个窗口。
	rows, err := s.db.QueryContext(ctx, `SELECT `+rubricDraftJobColumns+` FROM question_rubric_draft_job WHERE tenant_id=$1 AND actor_id=$2 AND exam_id=$3 ORDER BY (status IN ('queued','running','cancelling')) DESC,created_at DESC LIMIT 100`, tenantID, actorID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RubricDraftJob{}
	for rows.Next() {
		record, err := scanRubricDraftJob(rows)
		if err != nil {
			return nil, err
		}
		record.Job.Result = nil
		items = append(items, record.Job)
	}
	return items, rows.Err()
}

func (s *PostgresStore) getRubricDraftJob(ctx context.Context, tenantID, actorID, examID, id string) (rubricDraftJobRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return rubricDraftJobRecord{}, err
	}
	defer tx.Rollback()
	_, archived, err := lockRubricDraftExamTx(ctx, tx, tenantID, examID)
	if err != nil {
		return rubricDraftJobRecord{}, err
	}
	record, err := scanRubricDraftJob(tx.QueryRowContext(ctx, `SELECT `+rubricDraftJobColumns+` FROM question_rubric_draft_job WHERE tenant_id=$1 AND actor_id=$2 AND exam_id=$3 AND id=$4`, tenantID, actorID, examID, id))
	if err != nil {
		return record, err
	}
	if archived {
		record.Job.Status, record.Job.Result, record.Job.Error = "stale", nil, "考试已归档，不能采用该建议"
	}
	return record, tx.Commit()
}

func (s *PostgresStore) cancelRubricDraftJob(ctx context.Context, tenantID, actorID, examID, id string) (RubricDraftJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RubricDraftJob{}, err
	}
	defer tx.Rollback()
	_, archived, err := lockRubricDraftExamTx(ctx, tx, tenantID, examID)
	if err != nil {
		return RubricDraftJob{}, err
	}
	if archived {
		return RubricDraftJob{}, ErrExamFrozen
	}
	// 取消运行任务只禁止采用结果，调用结束前仍计入并发，以免连续取消绕过上限。
	record, err := scanRubricDraftJob(tx.QueryRowContext(ctx, `UPDATE question_rubric_draft_job SET status=CASE WHEN status='queued' THEN 'cancelled' WHEN status='running' THEN 'cancelling' ELSE status END,updated_at=now() WHERE tenant_id=$1 AND actor_id=$2 AND exam_id=$3 AND id=$4 RETURNING `+rubricDraftJobColumns, tenantID, actorID, examID, id))
	if err != nil {
		return RubricDraftJob{}, err
	}
	return record.Job, tx.Commit()
}

func (s *PostgresStore) claimRubricDraftJob(ctx context.Context, lease time.Duration, id string) (*rubricDraftJobRecord, error) {
	if err := s.cleanupExpiredRubricDraftJobs(ctx); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// 此入口仅由进程维护上下文调用；共享领取锁覆盖跨租户的全局计数与占位。
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(674218519)`); err != nil {
		return nil, err
	}
	var running, local int
	if err = tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE j.local_model) FROM question_rubric_draft_job j
JOIN exam e ON e.tenant_id=j.tenant_id AND e.id=j.exam_id
LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id
WHERE j.status IN ('running','cancelling') AND e.archived_at IS NULL AND e.status<>'archived'
  AND COALESCE(parent.archived_at IS NULL AND parent.status<>'archived',true)`).Scan(&running, &local); err != nil {
		return nil, err
	}
	if running >= RubricDraftConcurrency {
		return nil, tx.Commit()
	}
	// 先选择候选身份，再锁考试和任务；全局领取锁让其他副本不能同时占用该候选。
	var tenantID, examID, jobID string
	err = tx.QueryRowContext(ctx, `SELECT j.tenant_id::text,j.exam_id::text,j.id::text FROM question_rubric_draft_job j
JOIN exam e ON e.tenant_id=j.tenant_id AND e.id=j.exam_id
LEFT JOIN exam_session parent ON parent.tenant_id=e.tenant_id AND parent.id=e.exam_session_id
WHERE j.status='queued' AND j.available_at<=now() AND (NOT j.local_model OR $1=0) AND ($2='' OR j.id::text=$2)
  AND e.archived_at IS NULL AND e.status<>'archived' AND COALESCE(parent.archived_at IS NULL AND parent.status<>'archived',true)
ORDER BY j.created_at,j.id LIMIT 1`, local, id).Scan(&tenantID, &examID, &jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	_, archived, err := lockRubricDraftExamTx(ctx, tx, tenantID, examID)
	if err != nil {
		return nil, err
	}
	if archived {
		return nil, tx.Commit()
	}
	record, err := scanRubricDraftJob(tx.QueryRowContext(ctx, `SELECT `+rubricDraftJobColumns+` FROM question_rubric_draft_job
WHERE tenant_id=$1 AND id=$2 AND status='queued' FOR UPDATE`, tenantID, jobID))
	if errors.Is(err, ErrNotFound) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	record.Job.Status, record.Job.UpdatedAt = "running", time.Now().UTC()
	record.Job.Attempt++
	record.LeaseToken, record.LeaseUntil = uuid.NewString(), time.Now().UTC().Add(lease)
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE question_rubric_draft_job SET status='running',payload=$2,lease_token=$3,lease_until=$4,updated_at=now() WHERE id=$1`, record.Job.ID, payload, record.LeaseToken, record.LeaseUntil)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *PostgresStore) finishRubricDraftJob(ctx context.Context, record rubricDraftJobRecord, status string, result *MathRubricDraftResponse, message string, retryAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, archived, err := lockRubricDraftExamTx(ctx, tx, record.TenantID, record.Job.ExamID)
	if err != nil {
		return err
	}
	if archived {
		return ErrExamFrozen
	}
	current, err := scanRubricDraftJob(tx.QueryRowContext(ctx, `SELECT `+rubricDraftJobColumns+` FROM question_rubric_draft_job WHERE tenant_id=$1 AND id=$2 AND lease_token=$3 AND lease_until>now() AND status IN ('running','cancelling') FOR UPDATE`, record.TenantID, record.Job.ID, record.LeaseToken))
	if errors.Is(err, ErrNotFound) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if current.Job.Status == "cancelling" {
		status, result, message = "cancelled", nil, ""
	}
	current.Job.Status, current.Job.Result, current.Job.Error = status, result, message
	payload, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if retryAt.IsZero() {
		retryAt = time.Now().UTC()
	}
	_, err = tx.ExecContext(ctx, `UPDATE question_rubric_draft_job SET status=$2,payload=$3,lease_token='',lease_until=NULL,available_at=$4,updated_at=now() WHERE id=$1`, record.Job.ID, status, payload, retryAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) staleRubricDraftJob(ctx context.Context, tenantID, actorID, examID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, archived, err := lockRubricDraftExamTx(ctx, tx, tenantID, examID)
	if err != nil {
		return err
	}
	if archived {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE question_rubric_draft_job SET status='stale',payload=jsonb_set(jsonb_set(payload,'{Job,result}','null'::jsonb),'{Job,error}',to_jsonb('题目、答案、细则或模型已变更，请重新生成'::text)),updated_at=now() WHERE tenant_id=$1 AND actor_id=$2 AND exam_id=$3 AND id=$4 AND status='succeeded'`, tenantID, actorID, examID, id)
	if err != nil {
		return err
	}
	return tx.Commit()
}

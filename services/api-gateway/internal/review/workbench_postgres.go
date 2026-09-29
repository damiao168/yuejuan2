package review

import (
	"context"
	"database/sql"
	"errors"
)

var _ WorkbenchStore = (*PostgresStore)(nil)

func (s *PostgresStore) ClaimNextTask(ctx context.Context, tenantID, reviewerID string, input NextTaskInput, options ClaimTaskOptions) (ReviewTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewTask{}, err
	}
	defer tx.Rollback()
	var id string
	// 先续接本人已分配的任务，优先最近打开的；选中行一直锁到领取完成，并跳过其他事务已锁的行。
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM review_task WHERE tenant_id=$1::uuid AND assigned_to=$2::uuid AND status IN ('assigned','in_progress','returned') AND deleted_at IS NULL AND ($3='' OR exam_id::text=$3) AND ($4='' OR question_id::text=$4) ORDER BY last_opened_at DESC NULLS LAST,priority DESC,created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, tenantID, reviewerID, input.ExamID, input.QuestionID).Scan(&id)
	// 仅内部调用显式允许时，才接手未分配或租约过期的任务；普通 HTTP 领取接口关闭此选项。
	if errors.Is(err, sql.ErrNoRows) && options.AllowUnassigned {
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM review_task WHERE tenant_id=$1::uuid AND ((assigned_to IS NULL AND status='pending') OR (status IN ('assigned','in_progress') AND claim_expires_at<now())) AND deleted_at IS NULL AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3) ORDER BY priority DESC,created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, tenantID, input.ExamID, input.QuestionID).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewTask{}, ErrNotFound
	}
	if err != nil {
		return ReviewTask{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_task SET status=CASE WHEN assigned_to IS DISTINCT FROM $3::uuid OR status='pending' THEN 'assigned' ELSE status END,assigned_to=$3::uuid,claimed_at=now(),claim_expires_at=now()+interval '30 minutes',last_opened_at=now(),revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, reviewerID)
	if err != nil {
		return ReviewTask{}, err
	}
	task, err := scanTask(tx.QueryRowContext(ctx, `SELECT `+reviewTaskColumns+` FROM review_task WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id))
	if err != nil {
		return task, err
	}
	return task, tx.Commit()
}

func (s *PostgresStore) GetWorkspace(ctx context.Context, tenantID, taskID string) (Workspace, error) {
	task, err := s.GetTask(ctx, tenantID, taskID)
	if err != nil {
		return Workspace{}, err
	}
	contextValue, err := s.loadContext(ctx, tenantID, task.AnswerSegmentID)
	if err != nil {
		return Workspace{}, err
	}
	var originalID, status, cropHash string
	var confidence sql.NullFloat64
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(sp.file_asset_id::text,''),seg.processing_status,seg.confidence,COALESCE(seg.crop_sha256,'') FROM answer_segment seg LEFT JOIN submission_page sp ON sp.tenant_id=seg.tenant_id AND sp.id=seg.submission_page_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL`, tenantID, task.AnswerSegmentID).Scan(&originalID, &status, &confidence, &cropHash)
	if err != nil {
		return Workspace{}, err
	}
	out := Workspace{Task: task, Context: contextValue, SegmentImageURL: "/api/v1/review-tasks/" + task.ID + "/segment-image", SegmentStatus: status}
	if cropHash != "" {
		out.SegmentImageURL += "?v=" + cropHash
	}
	if originalID != "" {
		out.OriginalImageURL = "/api/v1/review-tasks/" + task.ID + "/original-image"
		out.OriginalFileID = originalID
	}
	if confidence.Valid {
		value := confidence.Float64
		out.SegmentConfidence = &value
	}
	return out, nil
}

// RenewTaskClaim 允许当前负责人重新建立或延长租约，不要求旧租约仍有效，也不增加任务版本。
func (s *PostgresStore) RenewTaskClaim(ctx context.Context, tenantID, taskID, reviewerID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE review_task SET claimed_at=COALESCE(claimed_at,now()),claim_expires_at=now()+interval '30 minutes',last_opened_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND assigned_to=$3::uuid AND status IN ('assigned','in_progress','returned') AND deleted_at IS NULL`, tenantID, taskID, reviewerID)
	if err != nil {
		return err
	}
	count, _ := result.RowsAffected()
	if count != 1 {
		return ErrForbidden
	}
	return nil
}

// ReleaseTaskClaim ends the short-lived workbench lease. assigned_to is the
// durable manager assignment used for queue visibility and progress totals, so
// releasing a browser session must not return the task to the pending pool.
func (s *PostgresStore) ReleaseTaskClaim(ctx context.Context, tenantID, taskID, reviewerID string) (ReviewTask, error) {
	task, err := scanTask(s.db.QueryRowContext(ctx, `
UPDATE review_task
SET claimed_at=NULL,claim_expires_at=NULL,last_opened_at=now(),revision=revision+1,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND assigned_to=$3::uuid
  AND status IN ('assigned','in_progress','returned') AND deleted_at IS NULL
RETURNING `+reviewTaskColumns+`
`, tenantID, taskID, reviewerID))
	if errors.Is(err, ErrNotFound) {
		return ReviewTask{}, ErrForbidden
	}
	return task, err
}

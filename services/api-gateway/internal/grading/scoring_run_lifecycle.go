package grading

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) BeginScoringRunCancellation(ctx context.Context, tenantID, runID string) (ScoringRun, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, nil, err
	}
	defer tx.Rollback()
	run, err := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, nil, err
	}
	if run.Status == "cancelled" {
		return run, []string{}, tx.Commit()
	}
	if run.Status != "cancelling" {
		if run.Status != "queued" && run.Status != "processing" && run.Status != "needs_review" && run.Status != "failed" {
			return ScoringRun{}, nil, ErrInvalidTransition
		}
		if _, err = tx.ExecContext(ctx, `UPDATE scoring_run SET status='cancelling',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID); err != nil {
			return ScoringRun{}, nil, err
		}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT o.runtime_task_id::text
FROM omr_run o
JOIN agent_worker_task wt ON wt.tenant_id=o.tenant_id AND wt.id=o.runtime_task_id
WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.runtime_task_id IS NOT NULL AND o.deleted_at IS NULL
  AND wt.status IN ('queued','leased','running')
UNION
SELECT wt.id::text
FROM subjective_grading_batch batch
JOIN subjective_grading_run ai_run ON ai_run.tenant_id=batch.tenant_id AND ai_run.batch_id=batch.id AND ai_run.deleted_at IS NULL
JOIN agent_worker_task wt ON wt.tenant_id=ai_run.tenant_id AND wt.source_type='subjective_grading_run' AND wt.source_id=ai_run.id
WHERE batch.tenant_id=$1::uuid AND batch.scoring_run_id=$2::uuid AND batch.deleted_at IS NULL
  AND wt.status IN ('queued','leased','running')`, tenantID, runID)
	if err != nil {
		return ScoringRun{}, nil, err
	}
	taskIDs := []string{}
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			rows.Close()
			return ScoringRun{}, nil, err
		}
		taskIDs = append(taskIDs, taskID)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ScoringRun{}, nil, err
	}
	if err = rows.Close(); err != nil {
		return ScoringRun{}, nil, err
	}
	run, err = scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, nil, err
	}
	return run, taskIDs, tx.Commit()
}

func (s *PostgresStore) FinalizeScoringRunCancellation(ctx context.Context, tenantID, runID string) (ScoringRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, err
	}
	defer tx.Rollback()
	run, err := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, err
	}
	if run.Status == "cancelled" {
		return run, tx.Commit()
	}
	if run.Status != "cancelling" {
		return ScoringRun{}, ErrInvalidTransition
	}
	var active int
	if err = tx.QueryRowContext(ctx, `
SELECT count(*)
FROM omr_run o
JOIN agent_worker_task wt ON wt.tenant_id=o.tenant_id AND wt.id=o.runtime_task_id
WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.deleted_at IS NULL
  AND wt.status IN ('queued','leased','running')`, tenantID, runID).Scan(&active); err != nil {
		return ScoringRun{}, err
	}
	if active > 0 {
		return ScoringRun{}, ErrInvalidTransition
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM subjective_grading_batch batch
JOIN subjective_grading_run ai_run ON ai_run.tenant_id=batch.tenant_id AND ai_run.batch_id=batch.id AND ai_run.deleted_at IS NULL
JOIN agent_worker_task wt ON wt.tenant_id=ai_run.tenant_id AND wt.source_type='subjective_grading_run' AND wt.source_id=ai_run.id
WHERE batch.tenant_id=$1::uuid AND batch.scoring_run_id=$2::uuid AND batch.deleted_at IS NULL
  AND wt.status IN ('queued','leased','running')`, tenantID, runID).Scan(&active); err != nil {
		return ScoringRun{}, err
	}
	if active > 0 {
		return ScoringRun{}, ErrInvalidTransition
	}
	if _, err = tx.ExecContext(ctx, `UPDATE subjective_grading_batch SET status='cancelled',updated_at=now()
WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND deleted_at IS NULL AND status <> 'cancelled'`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET status='invalidated',completed_at=COALESCE(completed_at,now()),updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status IN ('queued','processing','retryable_error','terminal_error') AND deleted_at IS NULL`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE review_task SET status='cancelled',assigned_to=NULL,claimed_at=NULL,claim_expires_at=NULL,return_reason='scoring_run_cancelled',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE arbitration_task arb SET status='cancelled',assigned_to=NULL,revision=arb.revision+1,updated_at=now()
FROM double_mark_session dm JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
WHERE arb.tenant_id=$1::uuid AND arb.double_mark_session_id=dm.id AND rt.scoring_run_id=$2::uuid
AND arb.status IN ('pending','assigned') AND arb.deleted_at IS NULL`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE double_mark_session dm SET status='cancelled',updated_at=now()
FROM review_task rt WHERE dm.tenant_id=$1::uuid AND rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
AND rt.scoring_run_id=$2::uuid AND dm.status IN ('pending','first_submitted','second_submitted','needs_arbitration') AND dm.deleted_at IS NULL`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='invalidated'
WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND is_current AND deleted_at IS NULL`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE final_grade fg SET deleted_at=now(),updated_at=now()
FROM double_mark_session dm JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
WHERE fg.tenant_id=$1::uuid AND fg.double_mark_session_id=dm.id AND rt.scoring_run_id=$2::uuid AND fg.deleted_at IS NULL AND NOT fg.locked`, tenantID, runID); err != nil {
		return ScoringRun{}, err
	}
	run, err = scanScoringRun(tx.QueryRowContext(ctx, `
UPDATE scoring_run
SET status='cancelled',cancelled_at=now(),completed_at=now(),queued_count=0,review_count=0,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
RETURNING id::text,tenant_id::text,exam_id::text,idempotency_key,status,total_count,queued_count,auto_confirmed_count,human_confirmed_count,review_count,failed_count,started_by::text,started_at,completed_at,created_at,updated_at`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, err
	}
	return run, tx.Commit()
}

func (s *PostgresStore) ListFailedOMRTasks(ctx context.Context, tenantID, runID string) ([]FailedOMRTask, error) {
	if _, err := scanScoringRun(s.db.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, runID)); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT o.id::text,o.runtime_task_id::text
FROM omr_run o
JOIN agent_worker_task wt ON wt.tenant_id=o.tenant_id AND wt.id=o.runtime_task_id
JOIN scoring_run sr ON sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id
WHERE o.tenant_id=$1::uuid AND o.scoring_run_id=$2::uuid AND o.status='terminal_error' AND o.deleted_at IS NULL
  AND sr.status NOT IN ('cancelling','cancelled') AND wt.status IN ('failed','dead_letter')
ORDER BY o.created_at`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []FailedOMRTask{}
	for rows.Next() {
		var item FailedOMRTask
		if err := rows.Scan(&item.OMRRunID, &item.RuntimeTaskID); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) PrepareOMRRetry(ctx context.Context, tenantID, omrRunID string) (FailedOMRTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return FailedOMRTask{}, err
	}
	defer tx.Rollback()
	var item FailedOMRTask
	var runStatus string
	err = tx.QueryRowContext(ctx, `
SELECT o.id::text,o.runtime_task_id::text,sr.status
FROM omr_run o
JOIN scoring_run sr ON sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.deleted_at IS NULL
FOR UPDATE OF o,sr`, tenantID, omrRunID).Scan(&item.OMRRunID, &item.RuntimeTaskID, &runStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return FailedOMRTask{}, ErrNotFound
	}
	if err != nil {
		return FailedOMRTask{}, err
	}
	if runStatus == "cancelling" || runStatus == "cancelled" {
		return FailedOMRTask{}, ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `UPDATE omr_run SET status='queued',completed_at=NULL,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='terminal_error'`, tenantID, omrRunID)
	if err != nil {
		return FailedOMRTask{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return FailedOMRTask{}, ErrInvalidTransition
	}
	return item, tx.Commit()
}

func (s *PostgresStore) RestoreOMRRetry(ctx context.Context, tenantID, omrRunID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE omr_run SET status='terminal_error',completed_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='queued'`, tenantID, omrRunID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return ErrInvalidTransition
	}
	return nil
}

func (s *PostgresStore) RefreshScoringRun(ctx context.Context, tenantID, runID string) (ScoringRun, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, err
	}
	defer tx.Rollback()
	run, err := s.refreshScoringRunTx(ctx, tx, tenantID, runID)
	if err != nil {
		return ScoringRun{}, err
	}
	return run, tx.Commit()
}

func (s *PostgresStore) refreshScoringRunTx(ctx context.Context, tx *sql.Tx, tenantID, runID string) (ScoringRun, error) {
	run, err := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, err
	}
	if run.Status == "cancelling" || run.Status == "cancelled" {
		return run, nil
	}
	var queued, review, failed, autoConfirmed, humanConfirmed int
	err = tx.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status IN ('queued','processing','retryable_error') AND deleted_at IS NULL),
  (SELECT count(DISTINCT pending.answer_segment_id) FROM (
     SELECT answer_segment_id FROM review_task WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
     UNION ALL
     SELECT arb.answer_segment_id FROM arbitration_task arb JOIN double_mark_session dm ON dm.tenant_id=arb.tenant_id AND dm.id=arb.double_mark_session_id
       JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
       WHERE arb.tenant_id=$1::uuid AND rt.scoring_run_id=$2::uuid AND arb.status IN ('pending','assigned') AND arb.deleted_at IS NULL
   ) pending),
  (SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status='terminal_error' AND deleted_at IS NULL),
  (SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND source='rule_confirmed' AND is_current AND status='confirmed' AND deleted_at IS NULL),
  (SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND source='human' AND is_current AND status='confirmed' AND deleted_at IS NULL)
`, tenantID, runID).Scan(&queued, &review, &failed, &autoConfirmed, &humanConfirmed)
	if err != nil {
		return ScoringRun{}, err
	}
	status := "processing"
	if run.TotalCount == 0 {
		status = "failed"
	} else if failed > 0 {
		status = "failed"
	} else if queued == 0 && review > 0 {
		status = "needs_review"
	} else if queued == 0 && review == 0 && autoConfirmed+humanConfirmed >= run.TotalCount {
		status = "completed"
	}
	return scanScoringRun(tx.QueryRowContext(ctx, `
UPDATE scoring_run
SET status=$3,queued_count=$4,review_count=$5,failed_count=$6,auto_confirmed_count=$7,human_confirmed_count=$8,
  completed_at=CASE WHEN $3='completed' THEN now() ELSE NULL END,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
RETURNING id::text,tenant_id::text,exam_id::text,idempotency_key,status,total_count,queued_count,auto_confirmed_count,human_confirmed_count,review_count,failed_count,started_by::text,started_at,completed_at,created_at,updated_at`, tenantID, runID, status, queued, review, failed, autoConfirmed, humanConfirmed))
}

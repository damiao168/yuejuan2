package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func lockScoringRunForArbitrationTx(ctx context.Context, tx *sql.Tx, tenantID, arbitrationID string) (sql.NullString, error) {
	var runID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT rt.scoring_run_id::text FROM arbitration_task arb
JOIN double_mark_session dm ON dm.tenant_id=arb.tenant_id AND dm.id=arb.double_mark_session_id
JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
WHERE arb.tenant_id=$1::uuid AND arb.id=$2::uuid AND arb.deleted_at IS NULL`, tenantID, arbitrationID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return runID, ErrNotFound
	}
	if err != nil || !runID.Valid {
		return runID, err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID.String).Scan(&status); err != nil {
		return runID, err
	}
	if status != "queued" && status != "processing" && status != "needs_review" && status != "failed" {
		return runID, ErrInvalidTransition
	}
	return runID, nil
}

func lockScoringRunForSessionTx(ctx context.Context, tx *sql.Tx, tenantID, sessionID string) error {
	var runID sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT rt.scoring_run_id::text FROM double_mark_session dm
JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
WHERE dm.tenant_id=$1::uuid AND dm.id=$2::uuid AND dm.deleted_at IS NULL`, tenantID, sessionID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil || !runID.Valid {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID.String).Scan(&status); err != nil {
		return err
	}
	if status != "queued" && status != "processing" && status != "needs_review" && status != "failed" {
		return ErrInvalidTransition
	}
	return nil
}

// A double-mark session contributes one final question grade only after both
// blind marks agree or an arbitrator resolves their disagreement.
func (s *PostgresStore) recordScoringDoubleMarkGradeTx(ctx context.Context, tx *sql.Tx, tenantID, runID, reviewTaskID, actorID string, final *FinalGrade) (string, error) {
	if final == nil {
		return "", nil
	}
	var priorID string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL FOR UPDATE`, tenantID, final.AnswerSegmentID).Scan(&priorID)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if priorID != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='superseded' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, priorID); err != nil {
			return "", err
		}
	}
	evidence, err := json.Marshal(map[string]any{"final_grade_id": final.ID, "double_mark_session_id": final.DoubleMarkSessionID, "resolution_strategy": final.ResolutionStrategy})
	if err != nil {
		return "", err
	}
	var gradeID string
	err = tx.QueryRowContext(ctx, `INSERT INTO question_grade
(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,review_task_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
SELECT rt.tenant_id,rt.exam_id,rt.submission_id,rt.question_id,rt.answer_segment_id,$3::uuid,eqs.id,rt.id,'human','confirmed',$4,$5,$6::jsonb,
 (SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=rt.answer_segment_id),NULLIF($7,'')::uuid,true,$8::uuid
FROM review_task rt JOIN exam_question_snapshot eqs ON eqs.tenant_id=rt.tenant_id AND eqs.exam_id=rt.exam_id AND eqs.question_id=rt.question_id
WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND rt.scoring_run_id=$3::uuid RETURNING id::text`, tenantID, reviewTaskID, runID, final.Score, final.MaxScore, evidence, priorID, actorID).Scan(&gradeID)
	return gradeID, err
}

func (s *PostgresStore) syncScoringDoubleMarkRunTx(ctx context.Context, tx *sql.Tx, tenantID, runID string) error {
	var review, confirmed int
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT count(DISTINCT pending.answer_segment_id) FROM (
    SELECT answer_segment_id FROM review_task WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL
    UNION ALL
    SELECT arb.answer_segment_id FROM arbitration_task arb JOIN double_mark_session dm ON dm.tenant_id=arb.tenant_id AND dm.id=arb.double_mark_session_id
      JOIN review_task rt ON rt.tenant_id=dm.tenant_id AND rt.id=dm.first_review_task_id
      WHERE arb.tenant_id=$1::uuid AND rt.scoring_run_id=$2::uuid AND arb.status IN ('pending','assigned') AND arb.deleted_at IS NULL
  ) pending),
 (SELECT count(*) FROM question_grade WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND source='human' AND is_current AND status='confirmed' AND deleted_at IS NULL)`, tenantID, runID).Scan(&review, &confirmed)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run SET review_count=$3,human_confirmed_count=$4,
 status=CASE WHEN queued_count=0 AND $3=0 AND failed_count=0 AND auto_confirmed_count+$4>=total_count THEN 'completed'
             WHEN queued_count=0 AND failed_count=0 THEN 'needs_review' ELSE status END,
 completed_at=CASE WHEN queued_count=0 AND $3=0 AND failed_count=0 AND auto_confirmed_count+$4>=total_count THEN now() ELSE completed_at END,
 updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('queued','processing','needs_review','failed')`, tenantID, runID, review, confirmed)
	return err
}

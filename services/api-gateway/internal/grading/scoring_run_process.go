package grading

import (
	"context"
)

func (s *PostgresStore) ProcessRuleCandidates(ctx context.Context, tenantID, runID, actorID string, engine *Engine) error {
	// Replaying creation must not restart a deleted or terminal run.
	var active bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL AND status IN ('queued','processing','needs_review'))`, tenantID, runID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ac.answer_segment_id::text FROM answer_candidate ac WHERE ac.tenant_id=$1::uuid AND ac.scoring_run_id=$2::uuid AND ac.is_current AND ac.decision='confirmed' AND ac.source IN ('ocr','manual','imported') AND ac.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=ac.tenant_id AND g.answer_segment_id=ac.answer_segment_id AND g.answer_candidate_id=ac.id AND g.is_current AND g.deleted_at IS NULL) ORDER BY ac.created_at`, tenantID, runID)
	if err != nil {
		return err
	}
	segmentIDs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		segmentIDs = append(segmentIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, segmentID := range segmentIDs {
		contextValue, loadErr := s.LoadContext(ctx, tenantID, segmentID)
		if loadErr != nil {
			return loadErr
		}
		grade, gradeErr := engine.Grade(contextValue)
		if gradeErr != nil {
			return gradeErr
		}
		if grade.AutoPass && !grade.NeedsHumanReview {
			if _, confirmErr := s.ConfirmRuleGrade(ctx, tenantID, segmentID, actorID, grade); confirmErr != nil {
				return confirmErr
			}
			continue
		}
		result, insertErr := s.db.ExecContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by) SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),'rule_review_required','pending',60,'single','rule_not_auto_confirmed',$3::uuid,$4::uuid FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, segmentID, runID, actorID)
		if insertErr != nil {
			return insertErr
		}
		count, _ := result.RowsAffected()
		if count == 1 {
			if _, err = s.db.ExecContext(ctx, `UPDATE scoring_run SET review_count=review_count+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID); err != nil {
				return err
			}
		}
	}
	_, err = s.RefreshScoringRun(ctx, tenantID, runID)
	return err
}

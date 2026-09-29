package grading

import (
	"context"
	"errors"
)

func (s *PostgresStore) ProcessRuleCandidates(ctx context.Context, tenantID, runID, actorID string, engine *Engine) error {
	// 重放创建命令不能重新启动已删除或已进入终态的评分运行。
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
		source, reason := reviewSourceRuleReview, reviewReasonRuleNotConfirmed
		grade, gradeErr := engine.Grade(contextValue)
		switch {
		case gradeErr != nil:
			route, routable := manualReviewRouteForGradeError(gradeErr, contextValue.Question.QuestionType)
			if !routable {
				return gradeErr
			}
			source, reason = route.Source, route.Reason
		case grade.AutoPass && !grade.NeedsHumanReview:
			// 引擎通过只表示候选满足确认条件；ConfirmRuleGrade 会在事务内锁定运行状态，拒绝已取消任务的迟到结果。
			if _, confirmErr := s.ConfirmRuleGrade(ctx, tenantID, segmentID, actorID, grade); confirmErr != nil {
				if errors.Is(confirmErr, ErrInvalidTransition) {
					return nil
				}
				return confirmErr
			}
			continue
		}
		if err := s.createRuleReviewTask(ctx, tenantID, segmentID, runID, actorID, source, reason); err != nil {
			if errors.Is(err, ErrInvalidTransition) {
				return nil
			}
			return err
		}
	}
	_, err = s.RefreshScoringRun(ctx, tenantID, runID)
	return err
}

func (s *PostgresStore) createRuleReviewTask(ctx context.Context, tenantID, segmentID, runID, actorID, source, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID).Scan(&status); err != nil {
		return err
	}
	if status != "queued" && status != "processing" && status != "needs_review" && status != "failed" {
		return ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by) SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),$5,'pending',60,'single',$6,$3::uuid,$4::uuid FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, segmentID, runID, actorID, source, reason)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE scoring_run SET review_count=review_count+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

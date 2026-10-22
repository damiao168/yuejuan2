package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) ProcessRuleCandidates(ctx context.Context, tenantID, runID, actorID string, engine *Engine) error {
	// 重放创建命令不能重新启动已删除或已进入终态的评分运行。
	var active bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL AND status IN ('queued','processing','needs_review','failed'))`, tenantID, runID).Scan(&active); err != nil {
		return err
	}
	if !active {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT ac.answer_segment_id::text FROM answer_candidate ac WHERE ac.tenant_id=$1::uuid AND ac.scoring_run_id=$2::uuid AND ac.is_current AND ac.decision='confirmed' AND ac.source IN ('ocr','manual','imported') AND ac.deleted_at IS NULL AND NOT EXISTS(SELECT 1 FROM question_grade g WHERE g.tenant_id=ac.tenant_id AND g.answer_segment_id=ac.answer_segment_id AND g.answer_candidate_id=ac.id AND g.is_current AND g.deleted_at IS NULL) AND NOT EXISTS(SELECT 1 FROM scoring_run_item item WHERE item.tenant_id=ac.tenant_id AND item.scoring_run_id=ac.scoring_run_id AND item.answer_segment_id=ac.answer_segment_id AND item.decision_status IN ('review_needed','manual_only')) ORDER BY ac.created_at`, tenantID, runID)
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
		stopped, err := s.processRuleCandidate(ctx, tenantID, runID, segmentID, actorID, engine)
		if err != nil {
			return err
		}
		if stopped {
			return nil
		}
	}
	_, err = s.RefreshScoringRun(ctx, tenantID, runID)
	return err
}

// 只有写入阶段拒绝运行状态时才停止整轮处理；加载和评分失败仍作为错误返回。
func (s *PostgresStore) processRuleCandidate(ctx context.Context, tenantID, runID, segmentID, actorID string, engine *Engine) (bool, error) {
	contextValue, err := s.loadFrozenCandidateContext(ctx, tenantID, runID, segmentID)
	if errors.Is(err, ErrStaleScoringRule) {
		// 旧运行缺少可证明的冻结内容时保留历史，不能临时采用当前规则补分。
		err = s.createRuleReviewTask(ctx, tenantID, segmentID, runID, actorID, reviewSourceRuleReview, "frozen_scoring_rule_unavailable")
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='review_needed',reason_code='frozen_scoring_rule_unavailable',next_action='teacher_review',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, runID, segmentID)
		}
		return false, err
	}
	if err != nil {
		return false, err
	}
	source, reason := reviewSourceRuleReview, reviewReasonRuleNotConfirmed
	grade, gradeErr := engine.Grade(contextValue)
	// blank 是有图像证据的空白事实；数值解析失败不能把清晰空白误当识别故障。
	if contextValue.Answer.AnswerPayload["presence"] == "blank" && (contextValue.Question.QuestionType == "fill_blank" || contextValue.Question.QuestionType == "numeric") {
		grade = applyInputConfidence(contextValue, finalize(engine.baseGrade(contextValue, "observed_blank")))
		gradeErr = nil
	}
	switch {
	case gradeErr != nil:
		route, routable := manualReviewRouteForGradeError(gradeErr, contextValue.Question.QuestionType)
		if !routable {
			return false, gradeErr
		}
		source, reason = route.Source, route.Reason
	case grade.AutoPass && !grade.NeedsHumanReview:
		// 引擎通过只表示候选满足确认条件；事务内核对运行状态，拒绝已取消任务的迟到结果。
		_, err := s.ConfirmRuleGrade(ctx, tenantID, segmentID, actorID, grade)
		if errors.Is(err, ErrInvalidTransition) {
			var inactive bool
			if stateErr := s.db.QueryRowContext(ctx, `SELECT status IN ('cancelling','cancelled','completed') FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID).Scan(&inactive); stateErr != nil {
				return false, stateErr
			}
			if inactive {
				return true, nil
			}
			_, err = s.db.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='review_needed',reason_code='manual_work_in_progress',next_action='teacher_review',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, runID, segmentID)
			return false, err
		}
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='auto_confirmed',reason_code='',next_action='none',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, runID, segmentID)
		}
		return false, err
	}
	err = s.createRuleReviewTask(ctx, tenantID, segmentID, runID, actorID, source, reason)
	if errors.Is(err, ErrInvalidTransition) {
		return true, nil
	}
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='review_needed',reason_code=$4,next_action='teacher_review',revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid`, tenantID, runID, segmentID, reason)
	}
	return false, err
}

// 规则比较读取本轮候选和冻结配置。新观察没有兼容正文时也可评分，清晰空白由 presence 表达。
func (s *PostgresStore) loadFrozenCandidateContext(ctx context.Context, tenantID, runID, segmentID string) (Context, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Context{}, err
	}
	defer tx.Rollback()
	var current Context
	var payload []byte
	var confidence sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT seg.question_id::text,ac.id::text,ac.display_text,ac.payload,ac.confidence::float8,ac.source,ac.created_by::text FROM answer_segment seg JOIN answer_candidate ac ON ac.tenant_id=seg.tenant_id AND ac.answer_segment_id=seg.id AND ac.scoring_run_id=$3::uuid AND ac.is_current AND ac.decision='confirmed' AND ac.deleted_at IS NULL WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL`, tenantID, segmentID, runID).Scan(&current.Question.ID, &current.Answer.ID, &current.Answer.AnswerText, &payload, &confidence, &current.Answer.Source, &current.Answer.RecordedBy)
	if err != nil {
		return Context{}, err
	}
	current.SegmentID = segmentID
	current.Answer.TenantID = tenantID
	current.Answer.AnswerSegmentID = segmentID
	if current.Answer.Source == "manual" {
		current.Answer.Source = "manual_entry"
	}
	if current.Answer.Source == "imported" {
		current.Answer.Source = "imported_answer"
	}
	if confidence.Valid {
		current.Answer.Confidence = &confidence.Float64
	}
	if err = json.Unmarshal(payload, &current.Answer.AnswerPayload); err != nil {
		return Context{}, err
	}
	current, _, err = applyFrozenScoringRunContextTx(ctx, tx, tenantID, runID, current)
	if err != nil {
		return Context{}, err
	}
	return current, tx.Commit()
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
	result, err := tx.ExecContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by) SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),sub.id::text),$5,'pending',60,'single',$6,$3::uuid,$4::uuid FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, segmentID, runID, actorID, source, reason)
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

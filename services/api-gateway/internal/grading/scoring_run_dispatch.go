package grading

import (
	"context"
	"database/sql"
	"strings"
)

func loadScoringDualReviewersTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, segments []scoringRunSegment) ([]string, error) {
	// 在与任务创建相同的事务中解析学校名单；DUAL_HUMAN 不得静默回退为单个未分配任务。
	dualNeeded := false
	for _, segment := range segments {
		if segment.scoringMode == "DUAL_HUMAN" {
			dualNeeded = true
			break
		}
	}
	dualReviewers := []string{}
	if dualNeeded {
		graderRows, graderErr := tx.QueryContext(ctx, `SELECT DISTINCT u.id::text FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id
JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.code='grader' AND r.deleted_at IS NULL
WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND u.status='active' AND u.deleted_at IS NULL
ORDER BY u.id::text`, tenantID, examID)
		if graderErr != nil {
			return nil, graderErr
		}
		for graderRows.Next() {
			var reviewerID string
			if err := graderRows.Scan(&reviewerID); err != nil {
				graderRows.Close()
				return nil, err
			}
			dualReviewers = append(dualReviewers, reviewerID)
		}
		if err := graderRows.Err(); err != nil {
			graderRows.Close()
			return nil, err
		}
		graderRows.Close()
		if len(dualReviewers) < 2 {
			return nil, ErrScoringNotReady
		}
	}
	return dualReviewers, nil
}

// insertScoringDualMarkTx 为一个片段同时写入两份指派和双评会话；外层事务负责一起提交。
func insertScoringDualMarkTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringSegmentTask) error {
	segment := task.segment
	first, second := task.reviewers[0], task.reviewers[1]
	ids := [2]string{}
	for i, reviewerID := range []string{first, second} {
		round := "first_mark"
		if i == 1 {
			round = "second_mark"
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO review_task
(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,assigned_to,grade_round,reason_code,scoring_run_id,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,'double_mark_required','assigned',50,$8::uuid,$9,'dual_human_policy',$10::uuid,$11::uuid)
RETURNING id::text`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, reviewerID, round, runID, actorID).Scan(&ids[i]); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO double_mark_session
(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_review_task_id,second_review_task_id,first_reviewer_id,second_reviewer_id,threshold,resolution_strategy,status,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8::uuid,$9::uuid,$10::uuid,$11::uuid,0,'average','pending',$12::uuid)`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, ids[0], ids[1], first, second, actorID)
	return err
}

// insertScoringStartReviewTx 返回是否新建任务；冲突跳过时不计入本次运行的人工片段数。
func insertScoringStartReviewTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringStartReviewTask) (bool, error) {
	segment := task.segment
	source, reason := task.cause.source, task.cause.reason
	result, insertErr := tx.ExecContext(ctx, `INSERT INTO review_task (tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,'pending',50,'single',$9,$10::uuid,$11::uuid)
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, source, reason, runID, actorID)
	if insertErr != nil {
		return false, insertErr
	}
	count, _ := result.RowsAffected()
	return count == 1, nil
}

// createScoringRunTasksTx 按查询顺序逐段完成持久化；任一段失败立即返回，不创建后续片段任务。
// 双人阅卷每段产生两份任务，但运行 review 计数仍只增加一个片段。
func (s *PostgresStore) createScoringRunTasksTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, segments []scoringRunSegment) (int, int, error) {
	dualReviewers, err := loadScoringDualReviewersTx(ctx, tx, tenantID, examID, segments)
	if err != nil {
		return 0, 0, err
	}
	dualIndex := 0
	queued, review := 0, 0
	for _, segment := range segments {
		task := scoringSegmentTask{segment: segment}
		// 名单已在处理任何片段前验证；仅双评片段消耗轮转位置，其他路径不改变指派顺序。
		if segment.scoringMode == "DUAL_HUMAN" {
			task.reviewers = [2]string{dualReviewers[dualIndex%len(dualReviewers)], dualReviewers[(dualIndex+1)%len(dualReviewers)]}
			dualIndex++
		}
		queuedDelta, reviewDelta, err := s.createScoringSegmentTasksTx(ctx, tx, tenantID, examID, runID, actorID, task)
		if err != nil {
			return 0, 0, err
		}
		queued += queuedDelta
		review += reviewDelta
	}
	return queued, review, nil
}

// createScoringSegmentTasksTx 在同一事务内完成一个片段的区域解析、路径选择和全部写入，再返回运行计数增量。
// 双评任务使用外层已选好的两名阅卷人；每段仍只计一次人工片段，冲突跳过的单评任务不计数。
func (s *PostgresStore) createScoringSegmentTasksTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, task scoringSegmentTask) (int, int, error) {
	segment := task.segment
	var area map[string]any
	if err := decodeJSONB(segment.area, &area, "question.answer_area"); err != nil {
		return 0, 0, err
	}
	options, _ := area["option_regions"].([]any)
	options = cropRelativeOptionRegions(area, options)
	answerScope, _ := area["answer_scope"].(string)
	route := routeScoringSegmentForScope(segment.kind, segment.scoringMode, answerScope, segment.ruleID != "", segment.answerID != "", len(options))
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item SET route=$3,stage='score',processing_status='waiting_dependency',updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, route.Kind); err != nil {
		return 0, 0, err
	}
	if route.Kind == scoringRouteObserveRule || route.Kind == scoringRouteImageAI {
		if err := queueScoringObservationTx(ctx, tx, tenantID, runID, actorID, segment, route.Kind); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	if route.Kind == scoringRouteRuleInput {
		created, err := insertRuleScoringInputTx(ctx, tx, tenantID, examID, runID, actorID, segment)
		if err != nil {
			return 0, 0, err
		}
		lowConfidence := segment.answerSource != "manual_entry" && segment.answerSource != "imported_answer" && (!segment.answerConfidence.Valid || segment.answerConfidence.Float64 < 0.9)
		_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET recognition_completed=true,stage=CASE WHEN $3 THEN 'done' ELSE 'score' END,processing_status=CASE WHEN $3 THEN 'succeeded' ELSE 'waiting_dependency' END,decision_status=CASE WHEN $3 THEN 'review_needed' ELSE 'undecided' END,reason_code=CASE WHEN $3 THEN 'answer_low_confidence' ELSE '' END,next_action=CASE WHEN $3 THEN 'teacher_review' ELSE 'wait' END WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, lowConfidence)
		return 0, created, err
	}
	if route.Kind == scoringRouteOMR {
		if err := s.queueScoringOMRTx(ctx, tx, tenantID, examID, runID, actorID, scoringOMRSegment{segment: segment, options: options}); err != nil {
			return 0, 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item item SET stage='observe',processing_status='queued',runtime_task_id=o.runtime_task_id FROM omr_run o WHERE item.tenant_id=$1::uuid AND item.id=$2::uuid AND o.tenant_id=item.tenant_id AND o.scoring_run_id=item.scoring_run_id AND o.answer_segment_id=item.answer_segment_id`, tenantID, segment.planItemID); err != nil {
			return 0, 0, err
		}
		return 1, 0, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_run_item SET stage='done',processing_status='succeeded',decision_status='manual_only',reason_code=$3,next_action='teacher_review' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, segment.planItemID, route.Reason); err != nil {
		return 0, 0, err
	}
	if segment.scoringMode == "DUAL_HUMAN" {
		if err := insertScoringDualMarkTx(ctx, tx, tenantID, examID, runID, actorID, task); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}
	created, err := insertScoringStartReviewTx(ctx, tx, tenantID, examID, runID, actorID, scoringStartReviewTask{segment: segment, cause: scoringReviewCause{source: route.Source, reason: route.Reason}})
	if err != nil {
		return 0, 0, err
	}
	if created {
		return 0, 1, nil
	}
	return 0, 0, nil
}

// insertRuleScoringInputTx 先替换当前候选，再为低置信答案创建复核任务；人工录入和导入答案沿用已确认来源规则。
func insertRuleScoringInputTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, segment scoringRunSegment) (int, error) {
	confidence := 0.0
	if segment.answerConfidence.Valid {
		confidence = segment.answerConfidence.Float64
	}
	confirmed := segment.answerSource == "manual_entry" || segment.answerSource == "imported_answer" || confidence >= 0.9
	decision := "ambiguous"
	if confirmed {
		decision = "confirmed"
	}
	source := "ocr"
	if segment.answerSource == "manual_entry" {
		source = "manual"
	}
	if segment.answerSource == "imported_answer" {
		source = "imported"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segment.id); err != nil {
		return 0, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,input_version_id,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$12::uuid,$13::uuid,$4,$5::jsonb,$6,NULLIF($7,0),$8,jsonb_build_object('answer_segment_answer_id',$9::text),'rule-input-v1','rule-input-v1',$10,true,$11::uuid)`, tenantID, segment.id, runID, source, segment.answerPayload, segment.answerText, confidence, decision, segment.answerID, "sha256:"+strings.ReplaceAll(segment.answerID, "-", ""), actorID, segment.snapshotID, segment.inputVersionID)
	if err != nil {
		return 0, err
	}
	if !confirmed {
		created, insertErr := insertScoringStartReviewTx(ctx, tx, tenantID, examID, runID, actorID, scoringStartReviewTask{segment: segment, cause: scoringReviewCause{source: "rule_review_required", reason: "answer_low_confidence"}})
		if insertErr != nil {
			return 0, insertErr
		}
		if created {
			return 1, nil
		}
	}
	return 0, nil
}

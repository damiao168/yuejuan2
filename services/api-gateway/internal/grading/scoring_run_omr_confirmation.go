package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// gradeConfirmedOMRAnswerTx 只处理本次已确认答案；自动评分失败后必须写入对应人工任务。
// 人工任务写入失败仍返回存储错误，由外层回滚整个回调，不单独提交 Worker 或 OMR 事实。
func (s *PostgresStore) gradeConfirmedOMRAnswerTx(ctx context.Context, tx *sql.Tx, tenantID, scoringRunID, actorID string, answer confirmedOMRAnswer, engine *Engine) (*QuestionGrade, error) {
	if engine == nil {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "grading_failure", reason: "auto_grade_engine_unavailable"})
	}
	gradingContext, contextErr := s.loadContextForAnswerTx(ctx, tx, tenantID, answer.segmentID, answer.answerID)
	if contextErr != nil {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "grading_failure", reason: "auto_grade_context_failed"})
	}
	gradingContext, _, contextErr = applyFrozenScoringRunContextTx(ctx, tx, tenantID, scoringRunID, gradingContext)
	if contextErr != nil {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "grading_failure", reason: "frozen_scoring_rule_unavailable"})
	}
	// 只有上游逐题质量门禁已接受的 OMR 进入这里；选择题沿用本次冻结阈值，不能被通用 OCR 的 0.9 二次拦截。
	evaluation, gradeErr := engine.gradeConfirmedOMRChoice(gradingContext, answer.minimumConfidence)
	// blank 是 OMR 明确观察到的事实，不用空字符串猜测。清晰空白按冻结题分记 0，仍受输入置信度约束。
	if gradingContext.Answer.AnswerPayload["presence"] == "blank" && gradingContext.Question.QuestionType != "single_choice" && gradingContext.Question.QuestionType != "multiple_choice" {
		evaluation = applyInputConfidence(gradingContext, finalize(engine.baseGrade(gradingContext, "omr_explicit_blank")))
		gradeErr = nil
	}
	if gradeErr != nil {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "grading_failure", reason: "auto_grade_execution_failed"})
	}
	if !evaluation.AutoPass || evaluation.NeedsHumanReview {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "rule_review_required", reason: "rule_not_auto_confirmed"})
	}
	confirmed, confirmErr := s.confirmRuleGradeForCandidateTx(ctx, tx, tenantID, answer.segmentID, answer.candidateID, actorID, evaluation)
	if confirmErr != nil {
		return nil, s.insertScoringReviewTaskTx(ctx, tx, tenantID, answer.segmentID, scoringRunID, actorID, scoringReviewCause{source: "grading_failure", reason: "auto_grade_confirmation_failed"})
	}
	return &confirmed, nil
}

// 自动确认同时需要服务端资格、单个选项、Worker 未要求复核及达标置信度；多选结果不走此分支。
func omrResultCanAutoConfirm(input OMRResultInput, autoConfirmEligible bool, minimumConfidence float64) bool {
	return autoConfirmEligible && input.Decision == "selected" && len(input.Selected) == 1 && !input.NeedsHumanReview && input.Confidence >= minimumConfidence
}

func omrReviewReason(input OMRResultInput, autoConfirmEligible bool, autoConfirmReason string, minimumConfidence float64) string {
	if input.Decision == "selected" && len(input.Selected) == 1 && !input.NeedsHumanReview && input.Confidence >= minimumConfidence && !autoConfirmEligible && strings.TrimSpace(autoConfirmReason) != "" {
		return "omr_" + autoConfirmReason
	}
	return "omr_" + input.Decision
}

// 多选要逐项确认整个集合，不能套用单选的 len=1 和第一、第二涂点差距。
// 新的 blank/多选分支必须带集合质量；旧 Worker 的单选仍沿用原有校准和置信度门槛。
func omrResultCanAutoConfirmForQuestion(input OMRResultInput, questionType string, eligible bool, minimumConfidence float64) bool {
	if !eligible || input.NeedsHumanReview || input.Confidence < minimumConfidence {
		return false
	}
	if input.Decision == "selected" && questionType != "multiple_choice" {
		if quality, ok := input.Thresholds["quality"].(map[string]any); ok && quality["classification"] != "clear" {
			return false
		}
		return omrResultCanAutoConfirm(input, eligible, minimumConfidence)
	}
	quality, ok := input.Thresholds["quality"].(map[string]any)
	if !ok || quality["version"] != "omr-set-quality-v1" {
		return false
	}
	expectedType := "single_choice"
	if questionType == "multiple_choice" {
		expectedType = "multiple_choice"
	}
	setConfidence, valid := floatValue(quality["set_confidence"])
	optionCount, countValid := floatValue(quality["option_count"])
	selectedCount, selectedValid := floatValue(quality["selected_count"])
	riskRaw, err := json.Marshal(quality["risk_flags"])
	var risks []string
	if !valid || !countValid || !selectedValid || math.IsNaN(setConfidence) || math.IsInf(setConfidence, 0) || setConfidence < minimumConfidence || setConfidence > 1 || quality["question_type"] != expectedType || optionCount < 1 || selectedCount != float64(len(input.Selected)) || selectedCount > optionCount || err != nil || json.Unmarshal(riskRaw, &risks) != nil || string(riskRaw) == "null" || len(risks) != 0 {
		return false
	}
	if input.Decision == "blank" {
		return len(input.Selected) == 0 && quality["classification"] == "blank"
	}
	return questionType == "multiple_choice" && input.Decision == "selected" && len(input.Selected) > 0 && quality["classification"] == "clear"
}

func omrReviewReasonForQuestion(input OMRResultInput, questionType string, eligible bool, eligibilityReason string, minimumConfidence float64) string {
	if quality, ok := input.Thresholds["quality"].(map[string]any); ok {
		switch quality["classification"] {
		case "possible_erasure", "unreadable", "uncertain_mark", "multiple_marks":
			return "omr_" + quality["classification"].(string)
		}
	}
	if !eligible && strings.TrimSpace(eligibilityReason) != "" && omrResultCanAutoConfirmForQuestion(input, questionType, true, minimumConfidence) {
		return "omr_" + eligibilityReason
	}
	if questionType == "multiple_choice" && input.Decision == "selected" && !omrResultCanAutoConfirmForQuestion(input, questionType, true, minimumConfidence) {
		return "omr_set_quality_insufficient"
	}
	return omrReviewReason(input, eligible, eligibilityReason, minimumConfidence)
}

func (s *PostgresStore) insertScoringReviewTaskTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID, scoringRunID, actorID string, cause scoringReviewCause) error {
	source, reason := cause.source, cause.reason
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by)
SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),sub.id::text),$3,'pending',80,'single',$4,$5::uuid,$6::uuid
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`,
		tenantID, segmentID, source, reason, scoringRunID, actorID)
	return err
}

// loadContextForAnswerTx 将自动评分绑定到本次 OMR 回调插入的答案记录，而非碰巧最新的答案。
func (s *PostgresStore) loadContextForAnswerTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID, answerID string) (Context, error) {
	row := tx.QueryRowContext(ctx, `
SELECT
  seg.id::text,
  q.id::text,q.tenant_id::text,q.exam_id::text,q.question_no,q.question_type,q.score::float8,
  COALESCE(ak.id::text,''),COALESCE(ak.answer_version,''),
  COALESCE(ak.standard_answer,'null'::jsonb),COALESCE(ak.equivalent_answers,'[]'::jsonb),
  COALESCE(sr.config,ak.tolerance,'{}'::jsonb),
  ans.id::text,ans.answer_text,ans.answer_payload,ans.source,ans.confidence::float8,ans.recorded_by::text,ans.created_at
FROM answer_segment seg
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id
LEFT JOIN LATERAL (
  SELECT id,answer_version,standard_answer,equivalent_answers,tolerance
  FROM question_answer_key
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT config FROM scoring_rule
  WHERE tenant_id=q.tenant_id AND question_id=q.id AND status='published' AND deleted_at IS NULL
  ORDER BY version DESC LIMIT 1
) sr ON true
JOIN answer_segment_answer ans ON ans.tenant_id=seg.tenant_id AND ans.answer_segment_id=seg.id AND ans.id=$3::uuid AND ans.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL
FOR UPDATE OF seg,ans`, tenantID, segmentID, answerID)

	var segmentIDOut, keyID, keyVersion string
	var question paper.Question
	var standardRaw, equivalentRaw, toleranceRaw, payloadRaw []byte
	var answer SegmentAnswer
	var confidence sql.NullFloat64
	if err := row.Scan(
		&segmentIDOut,
		&question.ID, &question.TenantID, &question.ExamID, &question.QuestionNo, &question.QuestionType, &question.Score,
		&keyID, &keyVersion, &standardRaw, &equivalentRaw, &toleranceRaw,
		&answer.ID, &answer.AnswerText, &payloadRaw, &answer.Source, &confidence, &answer.RecordedBy, &answer.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if keyID == "" {
		return Context{}, ErrAnswerKeyMissing
	}
	answerKey := paper.AnswerKey{ID: keyID, QuestionID: question.ID, AnswerVersion: keyVersion}
	if err := decodeJSONB(standardRaw, &answerKey.StandardAnswer, "question_answer_key.standard_answer"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(equivalentRaw, &answerKey.EquivalentAnswers, "question_answer_key.equivalent_answers"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(toleranceRaw, &answerKey.Tolerance, "question_answer_key.tolerance"); err != nil {
		return Context{}, err
	}
	question.AnswerKey = &answerKey
	answer.TenantID = tenantID
	answer.AnswerSegmentID = segmentIDOut
	answer.AnswerPayload = map[string]any{}
	if err := decodeJSONB(payloadRaw, &answer.AnswerPayload, "answer_segment_answer.answer_payload"); err != nil {
		return Context{}, err
	}
	if confidence.Valid {
		value := confidence.Float64
		answer.Confidence = &value
	}
	answer.CreatedAt = answer.CreatedAt.UTC()
	return Context{SegmentID: segmentIDOut, Question: question, AnswerKey: answerKey, Answer: answer}, nil
}

// confirmRuleGradeForCandidateTx 是事务内的 ConfirmRuleGrade；显式传入候选 ID，
// 防止并发或旧答案被误判为当前候选。
func (s *PostgresStore) confirmRuleGradeForCandidateTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID, candidateID, actorID string, grade Grade) (QuestionGrade, error) {
	if grade.AnswerSegmentID != segmentID || grade.NeedsHumanReview || !grade.AutoPass || math.IsNaN(grade.SuggestedScore) || math.IsInf(grade.SuggestedScore, 0) || math.IsNaN(grade.MaxScore) || math.IsInf(grade.MaxScore, 0) || grade.SuggestedScore < 0 || grade.SuggestedScore > grade.MaxScore {
		return QuestionGrade{}, ErrInvalidInput
	}
	var examID, submissionID, questionID, ruleID, scoringRunID, snapshotID string
	err := tx.QueryRowContext(ctx, `
SELECT sub.exam_id::text,seg.submission_id::text,seg.question_id::text,COALESCE(ac.scoring_run_id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN answer_candidate ac ON ac.tenant_id=seg.tenant_id AND ac.answer_segment_id=seg.id AND ac.id=$3::uuid AND ac.is_current AND ac.decision='confirmed' AND ac.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL
FOR UPDATE OF seg,ac`, tenantID, segmentID, candidateID).Scan(&examID, &submissionID, &questionID, &scoringRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, ErrNotFound
	}
	if err != nil {
		return QuestionGrade{}, err
	}
	if scoringRunID != "" {
		ruleID, snapshotID, err = frozenScoringRuleIDTx(ctx, tx, tenantID, scoringRunID, questionID)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT rule.id::text,snapshot.id::text FROM scoring_rule rule JOIN exam_question_snapshot snapshot ON snapshot.tenant_id=rule.tenant_id AND snapshot.exam_id=rule.exam_id AND snapshot.question_id=rule.question_id WHERE rule.tenant_id=$1::uuid AND rule.question_id=$2::uuid AND rule.status='published' AND rule.deleted_at IS NULL ORDER BY rule.version DESC,snapshot.snapshot_version DESC LIMIT 1`, tenantID, questionID).Scan(&ruleID, &snapshotID)
	}
	if err != nil {
		return QuestionGrade{}, err
	}
	var existing QuestionGrade
	var existingEvidence []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND answer_candidate_id=$3::uuid AND scoring_rule_id=$4::uuid AND is_current AND deleted_at IS NULL`, tenantID, segmentID, candidateID, ruleID).Scan(&existing.ID, &existing.AnswerSegmentID, &existing.QuestionID, &existing.Score, &existing.MaxScore, &existing.Source, &existing.Version, &existingEvidence, &existing.CreatedAt)
	if err == nil {
		if decodeErr := decodeJSONB(existingEvidence, &existing.Evidence, questionGradeEvidenceColumn); decodeErr != nil {
			return QuestionGrade{}, decodeErr
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, err
	}
	if err := guardAutomaticRuleGradeTx(ctx, tx, tenantID, segmentID); err != nil {
		return QuestionGrade{}, err
	}
	evidence, err := json.Marshal(map[string]any{"grader_type": grade.GraderType, "rule_version": grade.RuleVersion, "confidence": grade.Confidence, "matched_points": grade.MatchedPoints, "missing_points": grade.MissingPoints, "evidence": grade.Evidence, "risk_flags": grade.RiskFlags})
	if err != nil {
		return QuestionGrade{}, err
	}
	var priorID string
	_ = tx.QueryRowContext(ctx, `SELECT id::text FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL FOR UPDATE`, tenantID, segmentID).Scan(&priorID)
	if priorID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='superseded' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, priorID); err != nil {
			return QuestionGrade{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,answer_candidate_id,scoring_rule_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,NULLIF($6,'')::uuid,$14::uuid,$7::uuid,$8::uuid,'rule_confirmed','confirmed',$9,$10,$11::jsonb,(SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$5::uuid),NULLIF($12,'')::uuid,true,$13::uuid)
RETURNING id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at`, tenantID, examID, submissionID, questionID, segmentID, scoringRunID, candidateID, ruleID, grade.SuggestedScore, grade.MaxScore, evidence, priorID, actorID, snapshotID)
	var out QuestionGrade
	var evidenceRaw []byte
	if err = row.Scan(&out.ID, &out.AnswerSegmentID, &out.QuestionID, &out.Score, &out.MaxScore, &out.Source, &out.Version, &evidenceRaw, &out.CreatedAt); err != nil {
		return QuestionGrade{}, err
	}
	if err = decodeJSONB(evidenceRaw, &out.Evidence, questionGradeEvidenceColumn); err != nil {
		return QuestionGrade{}, err
	}
	return out, nil
}

// 自动规则结果不能替换教师已开始处理的工作。锁住现有任务后检查归属与草稿，
// 使并发认领/保存必须等待本事务结束；已有人工成绩始终优先。
func guardAutomaticRuleGradeTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,assigned_to IS NOT NULL OR status IN ('assigned','in_progress') FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL ORDER BY id FOR UPDATE`, tenantID, segmentID)
	if err != nil {
		return err
	}
	claimed := false
	for rows.Next() {
		var id string
		var owned bool
		if err := rows.Scan(&id, &owned); err != nil {
			rows.Close()
			return err
		}
		claimed = claimed || owned
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var protected bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='human' AND is_current AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM review_draft draft JOIN review_task task ON task.tenant_id=draft.tenant_id AND task.id=draft.review_task_id WHERE task.tenant_id=$1::uuid AND task.answer_segment_id=$2::uuid AND draft.deleted_at IS NULL AND task.deleted_at IS NULL)`, tenantID, segmentID).Scan(&protected)
	if err != nil {
		return err
	}
	if claimed || protected {
		return ErrInvalidTransition
	}
	return nil
}

func (s *PostgresStore) ConfirmRuleGrade(ctx context.Context, tenantID, segmentID, actorID string, grade Grade) (QuestionGrade, error) {
	if grade.AnswerSegmentID != segmentID || grade.NeedsHumanReview || !grade.AutoPass || math.IsNaN(grade.SuggestedScore) || math.IsInf(grade.SuggestedScore, 0) || math.IsNaN(grade.MaxScore) || math.IsInf(grade.MaxScore, 0) || grade.SuggestedScore < 0 || grade.SuggestedScore > grade.MaxScore {
		return QuestionGrade{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QuestionGrade{}, err
	}
	defer tx.Rollback()
	// 父运行先于候选加锁；取消命令锁同一行，迟到的规则结果不能越过取消边界。
	var expectedRunID, candidateID string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(scoring_run_id::text,''),id::text FROM answer_candidate WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND decision='confirmed' AND deleted_at IS NULL`, tenantID, segmentID).Scan(&expectedRunID, &candidateID)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, ErrNotFound
	}
	if err != nil {
		return QuestionGrade{}, err
	}
	if expectedRunID != "" {
		var runStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, expectedRunID).Scan(&runStatus); err != nil {
			return QuestionGrade{}, err
		}
		if runStatus != "queued" && runStatus != "processing" && runStatus != "needs_review" && runStatus != "failed" {
			return QuestionGrade{}, ErrInvalidTransition
		}
	}
	out, err := s.confirmRuleGradeForCandidateTx(ctx, tx, tenantID, segmentID, candidateID, actorID, grade)
	if err != nil {
		return QuestionGrade{}, err
	}
	if expectedRunID != "" {
		if _, err := s.refreshScoringRunTx(ctx, tx, tenantID, expectedRunID); err != nil {
			return QuestionGrade{}, err
		}
	}
	return out, tx.Commit()
}

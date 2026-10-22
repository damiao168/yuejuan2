package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const (
	questionGradeEvidenceColumn = "question_grade.evidence"
)

// scoringReviewCause 同时记录复核队列来源和具体原因，二者共同决定这项人工工作的含义。
type scoringReviewCause struct {
	source string
	reason string
}

// confirmedOMRAnswer 关联本次回调刚写入的候选和答案，不能改成按时间查找最新答案。
type confirmedOMRAnswer struct {
	segmentID         string
	candidateID       string
	answerID          string
	minimumConfidence float64
}

func (s *PostgresStore) GetOMRRun(ctx context.Context, tenantID, id string) (OMRRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT o.id::text,o.tenant_id::text,o.scoring_run_id::text,o.answer_segment_id::text,s.exam_id::text,COALESCE(o.runtime_task_id::text,''),o.status,COALESCE(o.decision,''),o.confidence,o.measurements,o.selected_options,COALESCE(o.overlay_file_asset_id::text,''),o.crop_sha256,COALESCE(o.reference_file_asset_id::text,''),COALESCE(o.reference_sha256,''),COALESCE(o.calibration_session_id::text,''),COALESCE(o.calibration_evidence_hash,''),o.profile_version,o.profile_hash,o.auto_confirm_min_confidence,o.auto_confirm_eligible,o.auto_confirm_reason FROM omr_run o JOIN scoring_run s ON s.tenant_id=o.tenant_id AND s.id=o.scoring_run_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.deleted_at IS NULL`, tenantID, id)
	var out OMRRun
	var confidence sql.NullFloat64
	var measurements, selected []byte
	err := row.Scan(&out.ID, &out.TenantID, &out.ScoringRunID, &out.AnswerSegmentID, &out.ExamID, &out.RuntimeTaskID, &out.Status, &out.Decision, &confidence, &measurements, &selected, &out.OverlayFileAssetID, &out.CropSHA256, &out.ReferenceFileAssetID, &out.ReferenceSHA256, &out.CalibrationSessionID, &out.CalibrationEvidenceHash, &out.ProfileVersion, &out.ProfileHash, &out.AutoConfirmMinConfidence, &out.AutoConfirmEligible, &out.AutoConfirmReason)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if confidence.Valid {
		v := confidence.Float64
		out.Confidence = &v
	}
	if err := decodeJSONB(measurements, &out.Measurements, "omr_run.measurements"); err != nil {
		return out, err
	}
	if err := decodeJSONB(selected, &out.SelectedOptions, "omr_run.selected_options"); err != nil {
		return out, err
	}
	return out, nil
}

// ApplyOMRResult 在一个事务中提交 Worker 结果、OMR 事实，以及自动成绩或人工复核任务。
// Worker 回调不能在没有持久化下一步评分动作时单独落库。
func (s *PostgresStore) ApplyOMRResult(ctx context.Context, tenantID, id, actorID string, input OMRResultInput, engine *Engine) (OMRRun, *QuestionGrade, error) {
	measurements, selected, err := encodeOMRResultInput(input)
	if err != nil {
		return OMRRun{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, nil, err
	}
	defer tx.Rollback()
	// 取消也先锁父运行；回调持有相同顺序，避免运行与 OMR 子记录互相等待。
	var parentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT run.status FROM scoring_run run JOIN omr_run omr ON omr.tenant_id=run.tenant_id AND omr.scoring_run_id=run.id WHERE omr.tenant_id=$1::uuid AND omr.id=$2::uuid AND omr.deleted_at IS NULL AND run.deleted_at IS NULL FOR UPDATE OF run`, tenantID, id).Scan(&parentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OMRRun{}, nil, ErrNotFound
		}
		return OMRRun{}, nil, err
	}
	var currentStatus, currentVersion, expectedProfileVersion, expectedProfileHash, expectedReferenceAssetID, expectedReferenceSHA256, expectedCalibrationID, expectedCalibrationEvidenceHash, calibrationStatus, calibrationEvidenceHash, autoConfirmReason, expectedCropHash, currentCropHash string
	var autoConfirmEligible bool
	var autoConfirmMinimumConfidence float64
	if err = tx.QueryRowContext(ctx, `SELECT o.status,COALESCE(o.result_version,''),o.profile_version,o.profile_hash,COALESCE(o.reference_file_asset_id::text,''),COALESCE(o.reference_sha256,''),COALESCE(o.calibration_session_id::text,''),COALESCE(o.calibration_evidence_hash,''),COALESCE(c.status,''),COALESCE(c.evidence_hash,''),o.auto_confirm_min_confidence,o.auto_confirm_eligible,o.auto_confirm_reason,o.crop_sha256,seg.crop_sha256 FROM omr_run o JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id AND seg.deleted_at IS NULL LEFT JOIN omr_calibration_session c ON c.tenant_id=o.tenant_id AND c.id=o.calibration_session_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.deleted_at IS NULL FOR UPDATE OF o,seg`, tenantID, id).Scan(&currentStatus, &currentVersion, &expectedProfileVersion, &expectedProfileHash, &expectedReferenceAssetID, &expectedReferenceSHA256, &expectedCalibrationID, &expectedCalibrationEvidenceHash, &calibrationStatus, &calibrationEvidenceHash, &autoConfirmMinimumConfidence, &autoConfirmEligible, &autoConfirmReason, &expectedCropHash, &currentCropHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OMRRun{}, nil, ErrNotFound
		}
		return OMRRun{}, nil, err
	}
	if currentStatus != "completed" && parentStatus != "processing" {
		return OMRRun{}, nil, ErrInvalidTransition
	}
	// 裁图已被配准修正替换时，旧任务只保留原运行历史，不能覆盖新输入的当前候选。
	if currentStatus != "completed" && expectedCropHash != currentCropHash {
		return OMRRun{}, nil, ErrRevisionConflict
	}
	if currentStatus != "completed" && (input.ProfileVersion != expectedProfileVersion || input.ProfileHash != expectedProfileHash || input.ReferenceSHA256 != expectedReferenceSHA256) {
		return OMRRun{}, nil, ErrInvalidInput
	}
	// 任务排队后校准可能被撤销；回调仍保存识别事实，但失去自动确认资格后必须转人工。
	if currentStatus != "completed" && expectedCalibrationID != "" && (calibrationStatus != "approved" || calibrationEvidenceHash != expectedCalibrationEvidenceHash) {
		autoConfirmEligible = false
		autoConfirmReason = paper.OMRAutoConfirmReasonCalibrationRevoked
		if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET auto_confirm_eligible=false,auto_confirm_reason=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, autoConfirmReason); err != nil {
			return OMRRun{}, nil, err
		}
	}
	task, err := completeOMRRuntimeTaskTx(ctx, tx, tenantID, id, input)
	if err != nil {
		return OMRRun{}, nil, err
	}
	if currentStatus == "completed" {
		if currentVersion != input.ResultVersion {
			return OMRRun{}, nil, ErrRevisionConflict
		}
		if err = tx.Commit(); err != nil {
			return OMRRun{}, nil, err
		}
		out, getErr := s.GetOMRRun(ctx, tenantID, id)
		return out, nil, getErr
	}
	var segmentID, scoringRunID string
	// 可重试失败是临时 Worker 故障留下的持久状态；新租约可以继续完成，旧租约仍由运行时完成检查拒绝。
	err = tx.QueryRowContext(ctx, `
UPDATE omr_run o
SET status='completed',decision=$3,confidence=$4,measurements=$5::jsonb,selected_options=$6::jsonb,
  overlay_file_asset_id=$7::uuid,result_version=$8,duration_ms=$9,completed_at=now(),updated_at=now()
FROM scoring_run sr
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.status IN ('queued','processing','retryable_error')
	  AND o.runtime_task_id=$10::uuid
  AND sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id AND sr.status='processing'
RETURNING o.answer_segment_id::text,o.scoring_run_id::text`, tenantID, id, input.Decision, input.Confidence, measurements, selected, input.OverlayFileAssetID, input.ResultVersion, input.DurationMS, task.ID).Scan(&segmentID, &scoringRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return OMRRun{}, nil, ErrRevisionConflict
	}
	if err != nil {
		return OMRRun{}, nil, err
	}
	evidence, err := json.Marshal(map[string]any{
		"measurements":                input.Measurements,
		"thresholds":                  input.Thresholds,
		"needs_human_review":          input.NeedsHumanReview,
		"profile_version":             expectedProfileVersion,
		"profile_hash":                expectedProfileHash,
		"reference_file_asset_id":     expectedReferenceAssetID,
		"reference_sha256":            expectedReferenceSHA256,
		"calibration_session_id":      expectedCalibrationID,
		"calibration_evidence_hash":   expectedCalibrationEvidenceHash,
		"auto_confirm_min_confidence": autoConfirmMinimumConfidence,
		"auto_confirm_eligible":       autoConfirmEligible,
		"auto_confirm_reason":         autoConfirmReason,
		"overlay_file_asset_id":       input.OverlayFileAssetID,
		"overlay_sha256":              input.OverlaySHA256,
	})
	if err != nil {
		return OMRRun{}, nil, ErrInvalidInput
	}
	_, err = tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segmentID)
	if err != nil {
		return OMRRun{}, nil, err
	}
	answerText := strings.Join(input.Selected, ",")
	var questionType string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(run.rule_snapshot->'questions'->(seg.question_id::text)->>'question_type',q.question_type) FROM answer_segment seg JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id JOIN scoring_run run ON run.tenant_id=seg.tenant_id AND run.id=$3::uuid WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid`, tenantID, segmentID, scoringRunID).Scan(&questionType); err != nil {
		return OMRRun{}, nil, err
	}
	decision := input.Decision
	if omrResultCanAutoConfirmForQuestion(input, questionType, autoConfirmEligible, autoConfirmMinimumConfidence) {
		decision = "confirmed"
	}
	presence := "present"
	if input.Decision == "blank" {
		presence = "blank"
	}
	var candidateID string
	err = tx.QueryRowContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) SELECT $1::uuid,$2::uuid,$3::uuid,eqs.id,'omr',jsonb_build_object('answers',$4::jsonb,'presence',$11::text),$5,$6,$7,$8::jsonb,'opencv-omr-v1',$9,seg.crop_sha256,true,$10::uuid FROM answer_segment seg JOIN scoring_run run ON run.tenant_id=seg.tenant_id AND run.id=$3::uuid JOIN LATERAL(SELECT id FROM exam_question_snapshot WHERE tenant_id=seg.tenant_id AND question_id=seg.question_id AND (run.rule_snapshot='{}'::jsonb OR id::text=run.rule_snapshot->'questions'->(seg.question_id::text)->'assessment_snapshot'->>'id') ORDER BY snapshot_version DESC LIMIT 1)eqs ON true WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid RETURNING id::text`, tenantID, segmentID, scoringRunID, selected, answerText, input.Confidence, decision, evidence, expectedProfileVersion, actorID, presence).Scan(&candidateID)
	if err != nil {
		return OMRRun{}, nil, err
	}
	var answerID string
	err = tx.QueryRowContext(ctx, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,answer_payload,source,confidence,recorded_by) VALUES($1::uuid,$2::uuid,$3,jsonb_build_object('answers',$4::jsonb,'presence',$7::text), 'omr',$5,$6::uuid) RETURNING id::text`, tenantID, segmentID, answerText, selected, input.Confidence, actorID, presence).Scan(&answerID)
	if err != nil {
		return OMRRun{}, nil, err
	}
	var questionGrade *QuestionGrade
	if decision != "confirmed" {
		// 未取得服务端资格时，清晰观察仍按本轮冻结规则给教师提供建议。
		// 建议附在对应候选证据中，不伪装成已确认的 question_grade。
		if engine != nil && omrResultCanAutoConfirmForQuestion(input, questionType, true, 0.9) {
			contextValue, contextErr := s.loadContextForAnswerTx(ctx, tx, tenantID, segmentID, answerID)
			if contextErr != nil {
				return OMRRun{}, nil, contextErr
			}
			contextValue, ruleID, contextErr := applyFrozenScoringRunContextTx(ctx, tx, tenantID, scoringRunID, contextValue)
			if contextErr == nil {
				suggestion, gradeErr := engine.Grade(contextValue)
				if presence == "blank" {
					suggestion = applyInputConfidence(contextValue, finalize(engine.baseGrade(contextValue, "omr_explicit_blank")))
					gradeErr = nil
				}
				if gradeErr == nil {
					raw, marshalErr := json.Marshal(map[string]any{"suggested_score": suggestion.SuggestedScore, "max_score": suggestion.MaxScore, "scoring_rule_id": ruleID, "confidence": suggestion.Confidence})
					if marshalErr != nil {
						return OMRRun{}, nil, marshalErr
					}
					if _, updateErr := tx.ExecContext(ctx, `UPDATE answer_candidate SET evidence=evidence || jsonb_build_object('rule_suggestion',$3::jsonb) WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, candidateID, raw); updateErr != nil {
						return OMRRun{}, nil, updateErr
					}
				}
			}
		}
		err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, scoringReviewCause{source: "omr_ambiguous", reason: omrReviewReasonForQuestion(input, questionType, autoConfirmEligible, autoConfirmReason, autoConfirmMinimumConfidence)})
	} else {
		answer := confirmedOMRAnswer{segmentID: segmentID, candidateID: candidateID, answerID: answerID, minimumConfidence: autoConfirmMinimumConfidence}
		questionGrade, err = s.gradeConfirmedOMRAnswerTx(ctx, tx, tenantID, scoringRunID, actorID, answer, engine)
	}
	if err != nil {
		return OMRRun{}, nil, err
	}
	if _, err = s.refreshScoringRunTx(ctx, tx, tenantID, scoringRunID); err != nil {
		return OMRRun{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, nil, err
	}
	out, getErr := s.GetOMRRun(ctx, tenantID, id)
	return out, questionGrade, getErr
}

// encodeOMRResultInput 在开启事务前完成输入检查和编码，避免无效结果取得运行锁。
func encodeOMRResultInput(input OMRResultInput) ([]byte, []byte, error) {
	if input.Decision != "selected" && input.Decision != "blank" && input.Decision != "multiple" && input.Decision != "ambiguous" {
		return nil, nil, ErrInvalidInput
	}
	if math.IsNaN(input.Confidence) || math.IsInf(input.Confidence, 0) || input.Confidence < 0 || input.Confidence > 1 || input.ResultVersion == "" || strings.TrimSpace(input.ProfileVersion) == "" || strings.TrimSpace(input.ProfileHash) == "" {
		return nil, nil, ErrInvalidInput
	}
	labels := map[string]bool{}
	for _, label := range input.Selected {
		if strings.TrimSpace(label) == "" || labels[label] {
			return nil, nil, ErrInvalidInput
		}
		labels[label] = true
	}
	if input.Decision == "blank" && len(input.Selected) != 0 || input.Decision == "selected" && len(input.Selected) == 0 || input.Decision == "multiple" && len(input.Selected) < 2 {
		return nil, nil, ErrInvalidInput
	}
	measurements, err := json.Marshal(input.Measurements)
	if err != nil {
		return nil, nil, ErrInvalidInput
	}
	selected, err := json.Marshal(input.Selected)
	if err != nil {
		return nil, nil, ErrInvalidInput
	}
	return measurements, selected, nil
}

// completeOMRRuntimeTaskTx 仍在 OMR 行与资格校验之后执行；已完成结果重放也必须通过租约和精确结果哈希校验。
func completeOMRRuntimeTaskTx(ctx context.Context, tx *sql.Tx, tenantID, id string, input OMRResultInput) (workerruntime.Task, error) {
	task, err := workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "omr-result-v1", Result: omrRuntimeResult(id, input), DurationMS: input.DurationMS})
	if err != nil {
		return workerruntime.Task{}, err
	}
	if task.SourceType != "omr_run" || task.SourceID != id {
		return workerruntime.Task{}, ErrInvalidTransition
	}
	return task, nil
}

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

// omrRuntimeResult 是 Worker Runtime 幂等哈希使用的精确语义载荷。
// 全部持久化 OMR 决策输入都放在此处，防止同一结果版本的重放静默改变持久化事实。
func omrRuntimeResult(id string, input OMRResultInput) map[string]any {
	return map[string]any{
		"omr_run_id":            id,
		"result_version":        input.ResultVersion,
		"decision":              input.Decision,
		"selected":              input.Selected,
		"confidence":            input.Confidence,
		"needs_human_review":    input.NeedsHumanReview,
		"measurements":          input.Measurements,
		"profile_version":       input.ProfileVersion,
		"profile_hash":          input.ProfileHash,
		"reference_sha256":      input.ReferenceSHA256,
		"thresholds":            input.Thresholds,
		"overlay_file_asset_id": input.OverlayFileAssetID,
		"overlay_sha256":        input.OverlaySHA256,
	}
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

// ApplyOMRWorkerFailure 与完成回调一样先锁来源，再在同一事务校验并结束当前尝试。
// 任何来源或汇总写入失败都回滚 runtime；否则终止失败会漏出评分工作区的重试列表。
func (s *PostgresStore) ApplyOMRWorkerFailure(ctx context.Context, tenantID, id string, input OMRFailureInput) (OMRRun, string, error) {
	raw, err := json.Marshal(input.ErrorDetail)
	if err != nil {
		return OMRRun{}, "", ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, "", err
	}
	defer tx.Rollback()
	var runtimeTaskID string
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(runtime_task_id::text,'') FROM omr_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, id).Scan(&runtimeTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return OMRRun{}, "", ErrNotFound
	}
	if err != nil {
		return OMRRun{}, "", err
	}
	if runtimeTaskID != input.TaskID {
		return OMRRun{}, "", ErrInvalidTransition
	}
	task, err := workerruntime.FailTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.FailInput{
		LeaseToken: input.LeaseToken, Retryable: input.Retryable, ErrorCode: input.ErrorCode, ErrorDetail: input.ErrorDetail, DurationMS: input.DurationMS,
	})
	if err != nil {
		return OMRRun{}, "", err
	}
	if task.SourceType != "omr_run" || task.SourceID != id {
		return OMRRun{}, "", ErrInvalidTransition
	}
	if err = s.applyOMRFailureTx(ctx, tx, tenantID, id, input.ErrorCode, raw, task.Status == workerruntime.StatusQueued); err != nil {
		return OMRRun{}, "", err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, "", err
	}
	out, err := s.GetOMRRun(ctx, tenantID, id)
	return out, task.Status, err
}

// ApplyOMRFailure 只保存领域失败，兼容已协调 runtime 的内部调用；Worker HTTP 回调使用上面的原子命令。
func (s *PostgresStore) ApplyOMRFailure(ctx context.Context, tenantID, id, errorCode string, detail map[string]any, retryable bool) (OMRRun, error) {
	raw, _ := json.Marshal(detail)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, err
	}
	defer tx.Rollback()
	if err = s.applyOMRFailureTx(ctx, tx, tenantID, id, errorCode, raw, retryable); err != nil {
		return OMRRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, err
	}
	return s.GetOMRRun(ctx, tenantID, id)
}

func (s *PostgresStore) applyOMRFailureTx(ctx context.Context, tx *sql.Tx, tenantID, id, errorCode string, raw []byte, retryable bool) error {
	// 可重试失败保留 retryable_error，终止失败写入完成时间；两种结果都刷新同一事务内的汇总。
	status := "terminal_error"
	if retryable {
		status = "retryable_error"
	}
	var runID string
	err := tx.QueryRowContext(ctx, `
UPDATE omr_run o
SET status=$3,error_code=$4,error_detail=$5::jsonb,updated_at=now(),
  completed_at=CASE WHEN $3='terminal_error' THEN now() ELSE NULL END
FROM scoring_run sr
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.status IN ('queued','processing','retryable_error')
  AND sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id AND sr.status='processing'
RETURNING o.scoring_run_id::text`, tenantID, id, status, errorCode, raw).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRevisionConflict
	}
	if err != nil {
		return err
	}
	_, err = s.refreshScoringRunTx(ctx, tx, tenantID, runID)
	return err
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

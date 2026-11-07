package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"math"
	"strings"
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
	// 任务排队后校准可能被撤销，或历史批准漏计了现在会自动接受的空白/多选错例；识别事实保留，但必须转人工。
	if currentStatus != "completed" && expectedCalibrationID != "" {
		calibrationValid := calibrationStatus == "approved" && calibrationEvidenceHash == expectedCalibrationEvidenceHash
		if calibrationValid {
			calibrationValid, err = omrCalibrationEvidenceAllowsAutoConfirmTx(ctx, tx, tenantID, expectedCalibrationID)
			if err != nil {
				return OMRRun{}, nil, err
			}
		}
		if !calibrationValid {
			autoConfirmEligible = false
			autoConfirmReason = paper.OMRAutoConfirmReasonCalibrationRevoked
			if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET auto_confirm_eligible=false,auto_confirm_reason=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, autoConfirmReason); err != nil {
				return OMRRun{}, nil, err
			}
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

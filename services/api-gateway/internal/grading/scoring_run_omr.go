package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

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

// ApplyOMRResult commits the worker result, OMR facts, and either an automatic
// grade or an actionable review task in one transaction. A worker result must
// never become durable without a durable next grading action.
func (s *PostgresStore) ApplyOMRResult(ctx context.Context, tenantID, id, actorID string, input OMRResultInput, engine *Engine) (OMRRun, *QuestionGrade, error) {
	if input.Decision != "selected" && input.Decision != "blank" && input.Decision != "multiple" && input.Decision != "ambiguous" {
		return OMRRun{}, nil, ErrInvalidInput
	}
	if input.Confidence < 0 || input.Confidence > 1 || input.ResultVersion == "" || strings.TrimSpace(input.ProfileVersion) == "" || strings.TrimSpace(input.ProfileHash) == "" {
		return OMRRun{}, nil, ErrInvalidInput
	}
	measurements, err := json.Marshal(input.Measurements)
	if err != nil {
		return OMRRun{}, nil, ErrInvalidInput
	}
	selected, err := json.Marshal(input.Selected)
	if err != nil {
		return OMRRun{}, nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, nil, err
	}
	defer tx.Rollback()
	var currentStatus, currentVersion, expectedProfileVersion, expectedProfileHash, expectedReferenceAssetID, expectedReferenceSHA256, expectedCalibrationID, expectedCalibrationEvidenceHash, calibrationStatus, calibrationEvidenceHash, autoConfirmReason string
	var autoConfirmEligible bool
	var autoConfirmMinimumConfidence float64
	if err = tx.QueryRowContext(ctx, `SELECT o.status,COALESCE(o.result_version,''),o.profile_version,o.profile_hash,COALESCE(o.reference_file_asset_id::text,''),COALESCE(o.reference_sha256,''),COALESCE(o.calibration_session_id::text,''),COALESCE(o.calibration_evidence_hash,''),COALESCE(c.status,''),COALESCE(c.evidence_hash,''),o.auto_confirm_min_confidence,o.auto_confirm_eligible,o.auto_confirm_reason FROM omr_run o LEFT JOIN omr_calibration_session c ON c.tenant_id=o.tenant_id AND c.id=o.calibration_session_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.deleted_at IS NULL FOR UPDATE OF o`, tenantID, id).Scan(&currentStatus, &currentVersion, &expectedProfileVersion, &expectedProfileHash, &expectedReferenceAssetID, &expectedReferenceSHA256, &expectedCalibrationID, &expectedCalibrationEvidenceHash, &calibrationStatus, &calibrationEvidenceHash, &autoConfirmMinimumConfidence, &autoConfirmEligible, &autoConfirmReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OMRRun{}, nil, ErrNotFound
		}
		return OMRRun{}, nil, err
	}
	if currentStatus != "completed" && (input.ProfileVersion != expectedProfileVersion || input.ProfileHash != expectedProfileHash || input.ReferenceSHA256 != expectedReferenceSHA256) {
		return OMRRun{}, nil, ErrInvalidInput
	}
	if currentStatus != "completed" && expectedCalibrationID != "" && (calibrationStatus != "approved" || calibrationEvidenceHash != expectedCalibrationEvidenceHash) {
		autoConfirmEligible = false
		autoConfirmReason = paper.OMRAutoConfirmReasonCalibrationRevoked
		if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET auto_confirm_eligible=false,auto_confirm_reason=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, id, autoConfirmReason); err != nil {
			return OMRRun{}, nil, err
		}
	}
	task, err := workerruntime.CompleteTaskInTx(ctx, tx, tenantID, input.TaskID, workerruntime.CompleteInput{LeaseToken: input.LeaseToken, ResultSchemaVersion: "omr-result-v1", Result: omrRuntimeResult(id, input), DurationMS: input.DurationMS})
	if err != nil {
		return OMRRun{}, nil, err
	}
	if task.SourceType != "omr_run" || task.SourceID != id {
		return OMRRun{}, nil, ErrInvalidTransition
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
	err = tx.QueryRowContext(ctx, `
UPDATE omr_run o
SET status='completed',decision=$3,confidence=$4,measurements=$5::jsonb,selected_options=$6::jsonb,
  overlay_file_asset_id=$7::uuid,result_version=$8,duration_ms=$9,completed_at=now(),updated_at=now()
FROM scoring_run sr
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.status IN ('queued','processing')
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
	decision := input.Decision
	if omrResultCanAutoConfirm(input, autoConfirmEligible, autoConfirmMinimumConfidence) {
		decision = "confirmed"
	}
	var candidateID string
	err = tx.QueryRowContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) SELECT $1::uuid,$2::uuid,$3::uuid,eqs.id,'omr',jsonb_build_object('answers',$4::jsonb),$5,$6,$7,$8::jsonb,'opencv-omr-v1',$9,seg.crop_sha256,true,$10::uuid FROM answer_segment seg JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid RETURNING id::text`, tenantID, segmentID, scoringRunID, selected, answerText, input.Confidence, decision, evidence, expectedProfileVersion, actorID).Scan(&candidateID)
	if err != nil {
		return OMRRun{}, nil, err
	}
	var answerID string
	err = tx.QueryRowContext(ctx, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,answer_payload,source,confidence,recorded_by) VALUES($1::uuid,$2::uuid,$3,jsonb_build_object('answers',$4::jsonb), 'omr',$5,$6::uuid) RETURNING id::text`, tenantID, segmentID, answerText, selected, input.Confidence, actorID).Scan(&answerID)
	if err != nil {
		return OMRRun{}, nil, err
	}
	var questionGrade *QuestionGrade
	if decision != "confirmed" {
		err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "omr_ambiguous", omrReviewReason(input, autoConfirmEligible, autoConfirmReason, autoConfirmMinimumConfidence))
	} else if engine == nil {
		err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "grading_failure", "auto_grade_engine_unavailable")
	} else {
		gradingContext, contextErr := s.loadContextForAnswerTx(ctx, tx, tenantID, segmentID, answerID)
		if contextErr != nil {
			err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "grading_failure", "auto_grade_context_failed")
		} else {
			evaluation, gradeErr := engine.Grade(gradingContext)
			if gradeErr != nil {
				err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "grading_failure", "auto_grade_execution_failed")
			} else if !evaluation.AutoPass || evaluation.NeedsHumanReview {
				err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "rule_review_required", "rule_not_auto_confirmed")
			} else {
				confirmed, confirmErr := s.confirmRuleGradeForCandidateTx(ctx, tx, tenantID, segmentID, candidateID, actorID, evaluation)
				if confirmErr != nil {
					err = s.insertScoringReviewTaskTx(ctx, tx, tenantID, segmentID, scoringRunID, actorID, "grading_failure", "auto_grade_confirmation_failed")
				} else {
					questionGrade = &confirmed
				}
			}
		}
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

// omrRuntimeResult is the exact semantic payload used by Worker Runtime's
// idempotency hash. Keeping every persisted OMR decision input here prevents a
// replay with the same result version from silently changing the durable facts.
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

func omrResultCanAutoConfirm(input OMRResultInput, autoConfirmEligible bool, minimumConfidence float64) bool {
	return autoConfirmEligible && input.Decision == "selected" && len(input.Selected) == 1 && !input.NeedsHumanReview && input.Confidence >= minimumConfidence
}

func omrReviewReason(input OMRResultInput, autoConfirmEligible bool, autoConfirmReason string, minimumConfidence float64) string {
	if input.Decision == "selected" && len(input.Selected) == 1 && !input.NeedsHumanReview && input.Confidence >= minimumConfidence && !autoConfirmEligible && strings.TrimSpace(autoConfirmReason) != "" {
		return "omr_" + autoConfirmReason
	}
	return "omr_" + input.Decision
}

func (s *PostgresStore) insertScoringReviewTaskTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID, scoringRunID, actorID, source, reason string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by)
SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),seg.id::text),$3,'pending',80,'single',$4,$5::uuid,$6::uuid
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`,
		tenantID, segmentID, source, reason, scoringRunID, actorID)
	return err
}

// loadContextForAnswerTx binds automatic grading to the answer row inserted by
// this OMR callback, rather than whichever answer happens to be latest.
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

// confirmRuleGradeForCandidateTx is the transaction-scoped counterpart of
// ConfirmRuleGrade. Its candidate ID is explicit so a concurrent or older
// answer can never be graded by accident.
func (s *PostgresStore) confirmRuleGradeForCandidateTx(ctx context.Context, tx *sql.Tx, tenantID, segmentID, candidateID, actorID string, grade Grade) (QuestionGrade, error) {
	if grade.AnswerSegmentID != segmentID || grade.NeedsHumanReview || !grade.AutoPass || grade.SuggestedScore < 0 || grade.SuggestedScore > grade.MaxScore {
		return QuestionGrade{}, ErrInvalidInput
	}
	var examID, submissionID, questionID, ruleID, scoringRunID string
	err := tx.QueryRowContext(ctx, `
SELECT sub.exam_id::text,seg.submission_id::text,seg.question_id::text,sr.id::text,COALESCE(ac.scoring_run_id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN answer_candidate ac ON ac.tenant_id=seg.tenant_id AND ac.answer_segment_id=seg.id AND ac.id=$3::uuid AND ac.is_current AND ac.decision='confirmed' AND ac.deleted_at IS NULL
JOIN scoring_rule sr ON sr.tenant_id=seg.tenant_id AND sr.question_id=seg.question_id AND sr.status='published' AND sr.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL
FOR UPDATE OF seg,ac`, tenantID, segmentID, candidateID).Scan(&examID, &submissionID, &questionID, &ruleID, &scoringRunID)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, ErrNotFound
	}
	if err != nil {
		return QuestionGrade{}, err
	}
	var existing QuestionGrade
	var existingEvidence []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND answer_candidate_id=$3::uuid AND scoring_rule_id=$4::uuid AND is_current AND deleted_at IS NULL`, tenantID, segmentID, candidateID, ruleID).Scan(&existing.ID, &existing.AnswerSegmentID, &existing.QuestionID, &existing.Score, &existing.MaxScore, &existing.Source, &existing.Version, &existingEvidence, &existing.CreatedAt)
	if err == nil {
		if decodeErr := decodeJSONB(existingEvidence, &existing.Evidence, "question_grade.evidence"); decodeErr != nil {
			return QuestionGrade{}, decodeErr
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
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
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,NULLIF($6,'')::uuid,(SELECT id FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$4::uuid),$7::uuid,$8::uuid,'rule_confirmed','confirmed',$9,$10,$11::jsonb,(SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$5::uuid),NULLIF($12,'')::uuid,true,$13::uuid)
RETURNING id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at`, tenantID, examID, submissionID, questionID, segmentID, scoringRunID, candidateID, ruleID, grade.SuggestedScore, grade.MaxScore, evidence, priorID, actorID)
	var out QuestionGrade
	var evidenceRaw []byte
	if err = row.Scan(&out.ID, &out.AnswerSegmentID, &out.QuestionID, &out.Score, &out.MaxScore, &out.Source, &out.Version, &evidenceRaw, &out.CreatedAt); err != nil {
		return QuestionGrade{}, err
	}
	if err = decodeJSONB(evidenceRaw, &out.Evidence, "question_grade.evidence"); err != nil {
		return QuestionGrade{}, err
	}
	return out, nil
}

func (s *PostgresStore) ApplyOMRFailure(ctx context.Context, tenantID, id, errorCode string, detail map[string]any, retryable bool) (OMRRun, error) {
	status := "terminal_error"
	if retryable {
		status = "retryable_error"
	}
	raw, _ := json.Marshal(detail)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRRun{}, err
	}
	defer tx.Rollback()
	var runID string
	err = tx.QueryRowContext(ctx, `
UPDATE omr_run o
SET status=$3,error_code=$4,error_detail=$5::jsonb,updated_at=now(),
  completed_at=CASE WHEN $3='terminal_error' THEN now() ELSE NULL END
FROM scoring_run sr
WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid AND o.status IN ('queued','processing','retryable_error')
  AND sr.tenant_id=o.tenant_id AND sr.id=o.scoring_run_id AND sr.status='processing'
RETURNING o.scoring_run_id::text`, tenantID, id, status, errorCode, raw).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return OMRRun{}, ErrRevisionConflict
	}
	if err != nil {
		return OMRRun{}, err
	}
	if _, err = s.refreshScoringRunTx(ctx, tx, tenantID, runID); err != nil {
		return OMRRun{}, err
	}
	if err = tx.Commit(); err != nil {
		return OMRRun{}, err
	}
	return s.GetOMRRun(ctx, tenantID, id)
}

func (s *PostgresStore) ConfirmRuleGrade(ctx context.Context, tenantID, segmentID, actorID string, grade Grade) (QuestionGrade, error) {
	if grade.AnswerSegmentID != segmentID || grade.NeedsHumanReview || !grade.AutoPass || grade.SuggestedScore < 0 || grade.SuggestedScore > grade.MaxScore {
		return QuestionGrade{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return QuestionGrade{}, err
	}
	defer tx.Rollback()
	var examID, submissionID, questionID, candidateID, ruleID, runID string
	err = tx.QueryRowContext(ctx, `
SELECT sub.exam_id::text, seg.submission_id::text, seg.question_id::text,
  ac.id::text, sr.id::text, COALESCE(ac.scoring_run_id::text,'')
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
JOIN answer_candidate ac ON ac.tenant_id=seg.tenant_id AND ac.answer_segment_id=seg.id AND ac.is_current AND ac.decision='confirmed' AND ac.deleted_at IS NULL
JOIN scoring_rule sr ON sr.tenant_id=seg.tenant_id AND sr.question_id=seg.question_id AND sr.status='published' AND sr.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL
FOR UPDATE OF seg`, tenantID, segmentID).Scan(&examID, &submissionID, &questionID, &candidateID, &ruleID, &runID)
	if errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, ErrNotFound
	}
	if err != nil {
		return QuestionGrade{}, err
	}
	var existing QuestionGrade
	var existingEvidence []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND answer_candidate_id=$3::uuid AND scoring_rule_id=$4::uuid AND is_current AND deleted_at IS NULL`, tenantID, segmentID, candidateID, ruleID).Scan(&existing.ID, &existing.AnswerSegmentID, &existing.QuestionID, &existing.Score, &existing.MaxScore, &existing.Source, &existing.Version, &existingEvidence, &existing.CreatedAt)
	if err == nil {
		if decodeErr := decodeJSONB(existingEvidence, &existing.Evidence, "question_grade.evidence"); decodeErr != nil {
			return QuestionGrade{}, decodeErr
		}
		return existing, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return QuestionGrade{}, err
	}
	evidenceMap := map[string]any{"grader_type": grade.GraderType, "rule_version": grade.RuleVersion, "confidence": grade.Confidence, "matched_points": grade.MatchedPoints, "missing_points": grade.MissingPoints, "evidence": grade.Evidence, "risk_flags": grade.RiskFlags}
	evidence, _ := json.Marshal(evidenceMap)
	var priorID string
	_ = tx.QueryRowContext(ctx, `SELECT id::text FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL FOR UPDATE`, tenantID, segmentID).Scan(&priorID)
	if priorID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='superseded' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, priorID); err != nil {
			return QuestionGrade{}, err
		}
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,answer_candidate_id,scoring_rule_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,NULLIF($6,'')::uuid,(SELECT id FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$4::uuid),$7::uuid,$8::uuid,'rule_confirmed','confirmed',$9,$10,$11::jsonb,(SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$5::uuid),NULLIF($12,'')::uuid,true,$13::uuid)
RETURNING id::text,answer_segment_id::text,question_id::text,score::float8,max_score::float8,source,version,evidence,created_at`, tenantID, examID, submissionID, questionID, segmentID, runID, candidateID, ruleID, grade.SuggestedScore, grade.MaxScore, evidence, priorID, actorID)
	var out QuestionGrade
	var evidenceRaw []byte
	if err = row.Scan(&out.ID, &out.AnswerSegmentID, &out.QuestionID, &out.Score, &out.MaxScore, &out.Source, &out.Version, &evidenceRaw, &out.CreatedAt); err != nil {
		return out, err
	}
	if err = decodeJSONB(evidenceRaw, &out.Evidence, "question_grade.evidence"); err != nil {
		return out, err
	}
	if runID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE scoring_run SET auto_confirmed_count=auto_confirmed_count+1,status=CASE WHEN queued_count=0 AND failed_count=0 AND review_count=0 THEN 'completed' WHEN queued_count=0 AND review_count>0 THEN 'needs_review' ELSE status END,completed_at=CASE WHEN queued_count=0 AND review_count=0 AND failed_count=0 THEN now() ELSE completed_at END,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

// 这些适配只写入本地仿真证据和评分绑定；候选、客观确认与复核任务仍由各自既有事务控制。
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
)

type answerCandidateCreateInput struct {
	tenantID   string
	adminID    string
	segmentID  string
	answerID   string
	question   questionDef
	answerData candidateAnswer
	extractor  string
}

type objectiveGradeConfirmation struct {
	tenantID     string
	adminID      string
	examID       string
	submissionID string
	questionID   string
	segmentID    string
	candidateID  string
	ruleID       string
	grade        map[string]any
}

func attachSegmentCrop(ctx context.Context, db *sql.DB, tenantID, segmentID string, asset map[string]any) error {
	result, err := db.ExecContext(ctx, `
UPDATE answer_segment
SET crop_file_asset_id=$3::uuid,crop_sha256=$4,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, segmentID, stringField(asset, "id"), stringField(asset, "hash_sha256"))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("attach crop affected %d answer segments", rows)
	}
	return nil
}

func createAnswerCandidate(
	ctx context.Context,
	db *sql.DB,
	input answerCandidateCreateInput,
) (string, error) {
	tenantID, adminID, segmentID, answerID := input.tenantID, input.adminID, input.segmentID, input.answerID
	question, answerData, extractor := input.question, input.answerData, input.extractor
	payload, _ := json.Marshal(map[string]any{
		"answer":          answerData.Text,
		"synthetic":       true,
		"simulation_only": true,
		"extractor":       extractor,
	})
	evidence, _ := json.Marshal(map[string]any{
		"answer_segment_answer_id": answerID,
		"simulation_seeded":        true,
		"dataset_id":               datasetID,
	})
	decision := "ambiguous"
	if answerData.Route == "auto_confirm" {
		decision = "confirmed"
	} else if displayText(answerData.Text) == "" {
		decision = "blank"
	} else if values, ok := answerData.Text.([]any); ok && len(values) > 1 {
		decision = "multiple"
	}
	source := "ocr"
	if extractor == "omr" {
		source = "omr"
	}
	var id string
	err := db.QueryRowContext(ctx, `
INSERT INTO answer_candidate(
 tenant_id,answer_segment_id,source,payload,display_text,confidence,decision,evidence,
 engine_version,profile_version,input_hash,is_current,created_by
)
VALUES($1::uuid,$2::uuid,$3,$4::jsonb,$5,$6,$7,$8::jsonb,$9,$10,$11,true,$12::uuid)
RETURNING id::text
`, tenantID, segmentID, source, payload, displayText(answerData.Text), answerData.Confidence, decision, evidence,
		"simulation-"+extractor+"-transcript-v1", "synthetic-handwriting-scan-v1",
		"sha256:"+sha256Hex([]byte(segmentID+":"+question.QuestionNo+":"+displayText(answerData.Text))), adminID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create answer candidate for %s: %w", question.QuestionNo, err)
	}
	return id, nil
}

func confirmObjectiveGrade(
	ctx context.Context,
	db *sql.DB,
	input objectiveGradeConfirmation,
) error {
	tenantID, adminID, examID, submissionID := input.tenantID, input.adminID, input.examID, input.submissionID
	questionID, segmentID, candidateID, ruleID, grade := input.questionID, input.segmentID, input.candidateID, input.ruleID, input.grade
	evidence, _ := json.Marshal(map[string]any{
		"ai_grade_id":             stringField(grade, "id"),
		"grader_type":             stringField(grade, "grader_type"),
		"rule_version":            stringField(grade, "rule_version"),
		"confidence":              numberField(grade, "confidence"),
		"risk_flags":              grade["risk_flags"],
		"simulation_seeded_input": true,
	})
	_, err := db.ExecContext(ctx, `
INSERT INTO question_grade(
 tenant_id,exam_id,submission_id,question_id,answer_segment_id,answer_candidate_id,scoring_rule_id,
 source,status,score,max_score,evidence,version,is_current,confirmed_by
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,
 'rule_confirmed','confirmed',$8,$9,$10::jsonb,1,true,$11::uuid)
`, tenantID, examID, submissionID, questionID, segmentID, candidateID, ruleID,
		numberField(grade, "suggested_score"), numberField(grade, "max_score"), evidence, adminID)
	if err != nil {
		return fmt.Errorf("confirm automatic objective grade: %w", err)
	}
	return nil
}

func createReviewTask(ctx context.Context, client *apiClient, segmentID, graderID, source string, priority int) (string, error) {
	response, err := client.json(ctx, http.MethodPost, "/api/v1/review-tasks", map[string]any{
		"answer_segment_id": segmentID,
		"source":            source,
		"priority":          priority,
		"assigned_to":       graderID,
		"grade_round":       "single",
	}, http.StatusCreated)
	if err != nil {
		return "", err
	}
	return nestedStringField(response, "task", "id"), nil
}

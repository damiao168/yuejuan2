package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type scoringObservedAnswer struct {
	Type  string   `json:"type"`
	Value any      `json:"value"`
	Parts []string `json:"parts,omitempty"`
}
type scoringObservedMember struct {
	SegmentID   string                 `json:"answer_segment_id"`
	QuestionID  string                 `json:"question_id"`
	Presence    string                 `json:"presence"`
	Answer      *scoringObservedAnswer `json:"answer"`
	Association string                 `json:"association"`
	Confidence  float64                `json:"confidence"`
	EvidenceIDs []string               `json:"evidence_ids"`
}
type scoringObservation struct {
	InputVersionID string                  `json:"input_version_id"`
	Members        []scoringObservedMember `json:"members"`
	Observations   []struct {
		ID         string `json:"id"`
		QuestionID string `json:"question_id"`
		CropSHA256 string `json:"crop_sha256"`
	} `json:"observations"`
}

// 只接受当前不可变输入中的成员。缺失或无法归属的成员单独复核，不能将共区正文复制给每一题。
func validateScoringObservation(raw json.RawMessage, input ScoringObservationInput) (map[string]scoringObservedMember, error) {
	var observation scoringObservation
	if err := json.Unmarshal(raw, &observation); err != nil {
		return nil, ErrInvalidInput
	}
	if observation.InputVersionID != input.InputVersionID {
		return nil, ErrRevisionConflict
	}
	expected := map[string]string{}
	for _, member := range input.Members {
		expected[member.SegmentID] = member.QuestionID
	}
	result := map[string]scoringObservedMember{}
	evidenceQuestions := map[string]string{}
	for _, evidence := range observation.Observations {
		if evidence.ID == "" || evidenceQuestions[evidence.ID] != "" || input.CropSHA256 != "" && evidence.CropSHA256 != input.CropSHA256 {
			return nil, ErrInvalidInput
		}
		evidenceQuestions[evidence.ID] = evidence.QuestionID
	}
	for _, member := range observation.Members {
		if questionID, ok := expected[member.SegmentID]; !ok || questionID != member.QuestionID {
			return nil, ErrInvalidInput
		}
		if _, exists := result[member.SegmentID]; exists {
			return nil, ErrInvalidInput
		}
		if math.IsNaN(member.Confidence) || math.IsInf(member.Confidence, 0) || member.Confidence < 0 || member.Confidence > 1 {
			return nil, ErrInvalidInput
		}
		switch member.Presence {
		case "present", "blank", "uncertain", "unreadable":
		default:
			return nil, ErrInvalidInput
		}
		if member.Association != "matched" && member.Association != "unresolved" {
			return nil, ErrInvalidInput
		}
		if member.Presence == "present" {
			hasAnswer := member.Answer != nil && strings.TrimSpace(stringify(member.Answer.Value)) != ""
			if member.Answer != nil {
				// 多小问允许中间未作答；保留空项才能维持小问顺序，但全空不能冒充已有作答。
				for _, part := range member.Answer.Parts {
					hasAnswer = hasAnswer || strings.TrimSpace(part) != ""
				}
			}
			if !hasAnswer {
				return nil, ErrInvalidInput
			}
		}
		if member.Answer != nil {
			switch member.Answer.Type {
			case "text", "latex", "number", "interval", "parts":
			default:
				return nil, ErrInvalidInput
			}
		}
		if member.Presence != "present" && member.Answer != nil {
			return nil, ErrInvalidInput
		}
		if (member.Presence == "present" || member.Presence == "blank") && member.Association == "matched" && len(member.EvidenceIDs) == 0 {
			return nil, ErrInvalidInput
		}
		for _, id := range member.EvidenceIDs {
			if evidenceQuestions[id] != member.QuestionID {
				return nil, ErrInvalidInput
			}
		}
		result[member.SegmentID] = member
	}
	for _, member := range input.Members {
		if _, ok := result[member.SegmentID]; !ok {
			result[member.SegmentID] = scoringObservedMember{SegmentID: member.SegmentID, QuestionID: member.QuestionID, Presence: "uncertain", Association: "unresolved"}
		}
	}
	return result, nil
}

func (s *PostgresStore) loadScoringObservationInput(ctx context.Context, task workerruntime.Task) (ScoringObservationInput, string, string, error) {
	var input ScoringObservationInput
	runID, _ := task.Payload["scoring_run_id"].(string)
	inputID, _ := task.Payload["input_version_id"].(string)
	if runID == "" || inputID == "" || task.SourceType != "answer_input_version" || task.SourceID != inputID {
		return input, "", "", ErrInvalidInput
	}
	var members []byte
	var actorID, status string
	err := s.db.QueryRowContext(ctx, `SELECT iv.id::text,iv.answer_segment_id::text,iv.crop_sha256,iv.members_json,sr.started_by::text,sr.status FROM answer_input_version iv JOIN scoring_run sr ON sr.tenant_id=iv.tenant_id AND sr.id=$3::uuid AND sr.deleted_at IS NULL WHERE iv.tenant_id=$1::uuid AND iv.id=$2::uuid AND EXISTS(SELECT 1 FROM scoring_run_item item WHERE item.tenant_id=iv.tenant_id AND item.scoring_run_id=sr.id AND item.input_version_id=iv.id AND item.observation_task_id=$4::uuid)`, task.TenantID, inputID, runID, task.ID).Scan(&input.InputVersionID, &input.SegmentID, &input.CropSHA256, &members, &actorID, &status)
	if err != nil {
		return input, "", "", err
	}
	if status == "cancelling" || status == "cancelled" {
		return input, "", "", ErrInvalidTransition
	}
	if err = json.Unmarshal(members, &input.Members); err != nil {
		return input, "", "", err
	}
	input.RequestID = task.ID
	input.ScoringRunID = runID
	return input, runID, actorID, nil
}

func (s *PostgresStore) completeScoringObservation(ctx context.Context, task workerruntime.Task, input ScoringObservationInput, runID, actorID string, raw json.RawMessage) error {
	members, err := validateScoringObservation(raw, input)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err = tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, task.TenantID, runID).Scan(&status); err != nil {
		return err
	}
	if status == "cancelling" || status == "cancelled" {
		return ErrInvalidTransition
	}
	if _, err = workerruntime.LockTaskForCompletionInTx(ctx, tx, task.TenantID, task.ID, task.LeaseToken); err != nil {
		return err
	}
	// crop 或配准变化时仅保留旧运行历史，迟到观察不能污染新的学生输入。
	var current bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM scoring_run_item item JOIN answer_input_version iv ON iv.tenant_id=item.tenant_id AND iv.id=item.input_version_id JOIN answer_segment seg ON seg.tenant_id=item.tenant_id AND seg.id=item.answer_segment_id WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND item.observation_task_id=$3::uuid AND (seg.deleted_at IS NOT NULL OR seg.crop_sha256<>iv.crop_sha256 OR seg.crop_file_asset_id<>iv.crop_file_asset_id OR seg.registration_run_id IS DISTINCT FROM iv.registration_run_id))`, task.TenantID, runID, task.ID).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrRevisionConflict
	}
	for _, expected := range input.Members {
		member := members[expected.SegmentID]
		var route string
		err = tx.QueryRowContext(ctx, `SELECT route FROM scoring_run_item WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND input_version_id=$4::uuid AND observation_task_id=$5::uuid FOR UPDATE`, task.TenantID, runID, expected.SegmentID, input.InputVersionID, task.ID).Scan(&route)
		// 共区可能还包含人工路线成员；本次任务只投影实际订阅了观察的条目。
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		reason := ""
		decision := "undecided"
		processing := "waiting_dependency"
		stage := "score"
		next := "wait"
		reliable := member.Association == "matched" && (member.Presence == "present" || member.Presence == "blank") && member.Confidence >= 0.9
		if route == scoringRouteObserveRule {
			// 现有 fill_blank 引擎只比较单个归一化字符串；多空不能拼接后静默记成零分。
			if member.Answer != nil && len(member.Answer.Parts) > 0 {
				reliable = false
				reason = "multi_blank_rule_requires_review"
			}
			if err = insertObservedCandidateTx(ctx, tx, task.TenantID, runID, actorID, input, member, raw, reliable); err != nil {
				return err
			}
			var hasCandidate, hasHumanTask bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_candidate WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND input_version_id=$4::uuid AND deleted_at IS NULL),EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$3::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL)`, task.TenantID, runID, member.SegmentID, input.InputVersionID).Scan(&hasCandidate, &hasHumanTask); err != nil {
				return err
			}
			if !hasCandidate {
				reliable = false
				reason = "manual_work_in_progress"
			}
			if !reliable {
				if reason == "" {
					reason = "answer_observation_uncertain"
				}
				decision = "review_needed"
				processing = "succeeded"
				stage = "done"
				next = "teacher_review"
				if !hasHumanTask {
					if _, err = insertObservedReviewTx(ctx, tx, task.TenantID, runID, actorID, member.SegmentID, reason); err != nil {
						return err
					}
				}
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE scoring_run_item SET observation_json=$5::jsonb,recognition_completed=$6,model_invoked=true,stage=$7,processing_status=$8,decision_status=$9,reason_code=$10,next_action=$11,retryable=false,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND input_version_id=$4::uuid`, task.TenantID, runID, member.SegmentID, input.InputVersionID, []byte(raw), member.Association == "matched" && (member.Presence == "present" || member.Presence == "blank"), stage, processing, decision, reason, next)
		if err != nil {
			return err
		}
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if _, err = workerruntime.CompleteTaskInTx(ctx, tx, task.TenantID, task.ID, workerruntime.CompleteInput{LeaseToken: task.LeaseToken, ResultSchemaVersion: "scoring-observation-result-v1", Result: result}); err != nil {
		return err
	}
	// 不确定观察会直接结束条目，不再进入后续评分；父运行汇总必须与条目、任务一同提交。
	if _, err = s.refreshScoringRunTx(ctx, tx, task.TenantID, runID); err != nil {
		return err
	}
	return tx.Commit()
}

func insertObservedCandidateTx(ctx context.Context, tx *sql.Tx, tenantID, runID, actorID string, input ScoringObservationInput, member scoringObservedMember, raw json.RawMessage, reliable bool) error {
	// 人工当前成绩和已开始阅卷的任务优先。后台观察仍存入条目，但不能替换教师正在使用的答案。
	var protected bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND source='human' AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL AND status IN ('assigned','in_progress','returned')) OR EXISTS(SELECT 1 FROM answer_segment_answer WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND source='manual_entry' AND deleted_at IS NULL)`, tenantID, member.SegmentID).Scan(&protected); err != nil {
		return err
	}
	if !protected {
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM review_draft draft JOIN review_task task ON task.tenant_id=draft.tenant_id AND task.id=draft.review_task_id WHERE task.tenant_id=$1::uuid AND task.answer_segment_id=$2::uuid AND draft.deleted_at IS NULL AND task.deleted_at IS NULL)`, tenantID, member.SegmentID).Scan(&protected); err != nil {
			return err
		}
	}
	if protected {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM answer_candidate WHERE tenant_id=$1::uuid AND scoring_run_id=$2::uuid AND answer_segment_id=$3::uuid AND input_version_id=$4::uuid AND deleted_at IS NULL)`, tenantID, runID, member.SegmentID, input.InputVersionID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, member.SegmentID); err != nil {
		return err
	}
	textValue := ""
	payload := map[string]any{"presence": member.Presence}
	if member.Answer != nil {
		textValue = stringify(member.Answer.Value)
		payload["answer"] = member.Answer.Value
		payload["type"] = member.Answer.Type
		if values, ok := member.Answer.Value.([]any); ok {
			payload["answers"] = values
		}
		if len(member.Answer.Parts) > 0 {
			payload["answers"] = member.Answer.Parts
			textValue = strings.Join(member.Answer.Parts, "; ")
		}
	}
	payloadRaw, _ := json.Marshal(payload)
	evidence, _ := json.Marshal(map[string]any{"input_version_id": input.InputVersionID, "observation_task_id": input.RequestID, "evidence_ids": member.EvidenceIDs, "observation": json.RawMessage(raw)})
	decision := "ambiguous"
	if reliable {
		decision = "confirmed"
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,input_version_id,presence,association_status,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) SELECT $1::uuid,$2::uuid,$3::uuid,item.assessment_snapshot_id,$4::uuid,$5,$6,'ocr',$7::jsonb,$8,$9,$10,$11::jsonb,'scoring-observation-v1','scoring-observation-v1',$12,true,$13::uuid FROM scoring_run_item item WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$3::uuid AND item.answer_segment_id=$2::uuid`, tenantID, member.SegmentID, runID, input.InputVersionID, member.Presence, member.Association, payloadRaw, textValue, member.Confidence, decision, evidence, scoringDigest([]any{input.InputVersionID, member}), actorID)
	if err != nil {
		return err
	}
	// 旧接口仍可读取有出处的非空正文；清晰空白仅存在候选中，不以占位文本冒充作答。
	if reliable && member.Presence == "present" {
		payload["input_version_id"] = input.InputVersionID
		payload["observation_task_id"] = input.RequestID
		payload["evidence_ids"] = member.EvidenceIDs
		answerPayload, _ := json.Marshal(payload)
		_, err = tx.ExecContext(ctx, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,answer_payload,source,confidence,recorded_by) VALUES($1::uuid,$2::uuid,$3,$4::jsonb,'ocr_text',$5,$6::uuid)`, tenantID, member.SegmentID, textValue, answerPayload, member.Confidence, actorID)
	}
	return err
}

func insertObservedReviewTx(ctx context.Context, tx *sql.Tx, tenantID, runID, actorID, segmentID, reason string) (bool, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by) SELECT seg.tenant_id,sub.exam_id,q.id,q.question_no,seg.id,sub.id,COALESCE(NULLIF(sub.candidate_no,''),sub.id::text),'rule_review_required','pending',60,'single',$5,$3::uuid,$4::uuid FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, segmentID, runID, actorID, reason)
	if err != nil {
		return false, fmt.Errorf("create observation review: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

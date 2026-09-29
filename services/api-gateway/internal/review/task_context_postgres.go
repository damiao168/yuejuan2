package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

var _ TaskContextStore = (*PostgresStore)(nil)

func (s *PostgresStore) GetTaskContext(ctx context.Context, tenantID, taskID string) (TaskContext, error) {
	workspace, err := s.GetWorkspace(ctx, tenantID, taskID)
	if err != nil {
		return TaskContext{}, err
	}
	out := taskContextFromWorkspace(workspace)
	if err := s.loadTaskClaim(ctx, tenantID, taskID, workspace.Task.Status, &out.Claim); err != nil {
		return TaskContext{}, err
	}
	out.AICandidates, err = s.listTaskAnswerCandidates(ctx, tenantID, workspace.Task.AnswerSegmentID)
	if err != nil {
		return TaskContext{}, err
	}
	out.ScoringEvidence, err = s.listTaskScoringEvidence(ctx, tenantID, workspace.Task.SubmissionID, workspace.Task.QuestionID)
	if err != nil {
		return TaskContext{}, err
	}
	if !allowsAIScorePrefill(workspace.Context.AssessmentSnapshot) {
		for index := range out.AICandidates {
			out.AICandidates[index].Payload = removeScoreFields(out.AICandidates[index].Payload)
			out.AICandidates[index].Evidence = removeScoreFields(out.AICandidates[index].Evidence)
		}
	}
	return out, nil
}

func (s *PostgresStore) loadTaskClaim(ctx context.Context, tenantID, taskID, taskStatus string, claim *TaskClaim) error {
	var claimedAt, expiresAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
SELECT claimed_at, claim_expires_at
FROM review_task
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
`, tenantID, taskID).Scan(&claimedAt, &expiresAt); err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	claim.ClaimedAt = nullTimePointer(claimedAt)
	claim.ExpiresAt = nullTimePointer(expiresAt)
	claim.CanRenew = claim.OwnerID != "" && isClaimableAssignedStatus(taskStatus)
	switch {
	case claimedAt.Valid && expiresAt.Valid && expiresAt.Time.After(time.Now()):
		claim.State = "claimed"
	case claimedAt.Valid:
		claim.State = "expired"
	case claim.OwnerID != "":
		claim.State = "assigned"
	default:
		claim.State = "unclaimed"
	}
	return nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copy := value.Time
	return &copy
}

// 返回当前候选和历史候选，当前项排在前面；调用方应读取 IsCurrent，不能把每条都当作当前识别结果。
func (s *PostgresStore) listTaskAnswerCandidates(ctx context.Context, tenantID, segmentID string) ([]AnswerCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, answer_segment_id::text, COALESCE(scoring_run_id::text,''),
       COALESCE(exam_question_snapshot_id::text,''), source, payload,
       display_text, confidence, decision, evidence, engine_version,
       profile_version, is_current, created_at
FROM answer_candidate
WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL
ORDER BY is_current DESC, created_at DESC, id
`, tenantID, segmentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnswerCandidate{}
	for rows.Next() {
		var item AnswerCandidate
		var payloadJSON, evidenceJSON []byte
		var confidence sql.NullFloat64
		if err := rows.Scan(
			&item.ID, &item.AnswerSegmentID, &item.ScoringRunID,
			&item.ExamQuestionSnapshotID, &item.Source, &payloadJSON,
			&item.DisplayText, &confidence, &item.Decision, &evidenceJSON,
			&item.EngineVersion, &item.ProfileVersion, &item.IsCurrent, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payloadJSON, &item.Payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(evidenceJSON, &item.Evidence); err != nil {
			return nil, err
		}
		if confidence.Valid {
			value := confidence.Float64
			item.Confidence = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) listTaskScoringEvidence(ctx context.Context, tenantID, submissionID, questionID string) ([]assessment.ScoringEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, question_id::text,
       exam_question_snapshot_id::text, evidence_type, source_artifact_id::text,
       COALESCE(rubric_criterion_key,''), payload_json, bbox_json, quality::float8,
       created_at
FROM scoring_evidence
WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND question_id=$3::uuid
ORDER BY created_at, id
`, tenantID, submissionID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []assessment.ScoringEvidence{}
	for rows.Next() {
		var item assessment.ScoringEvidence
		var payloadJSON, bboxJSON []byte
		var quality sql.NullFloat64
		if err := rows.Scan(
			&item.ID, &item.TenantID, &item.SubmissionID, &item.QuestionID,
			&item.ExamQuestionSnapshotID, &item.EvidenceType, &item.SourceArtifactID,
			&item.RubricCriterionKey, &payloadJSON, &bboxJSON, &quality, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payloadJSON, &item.Payload); err != nil {
			return nil, err
		}
		if len(bboxJSON) > 0 && string(bboxJSON) != "null" {
			item.BoundingBox = &assessment.BoundingBox{}
			if err := json.Unmarshal(bboxJSON, item.BoundingBox); err != nil {
				return nil, err
			}
		}
		if quality.Valid {
			value := quality.Float64
			item.Quality = &value
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

package answergroup

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"time"
)

func (s *PostgresStore) Confirm(ctx context.Context, tenantID, groupID, actorID string, input ConfirmInput) (Group, []Candidate, error) {
	if s.db == nil || !validIDs(tenantID, groupID, actorID) || input.ExpectedRevision <= 0 {
		return Group{}, nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, nil, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, nil, err
	}
	if group.RepresentationVersion != s.provider.Version() || group.AlgorithmVersion != DefaultAlgorithmVersion {
		return Group{}, nil, ErrStateConflict
	}
	refreshReadiness(&group)
	if group.Decision == nil || group.Decision.Revision != input.ExpectedRevision {
		return Group{}, nil, ErrRevisionConflict
	}
	if !group.CanConfirm {
		return Group{}, nil, ErrSamplingIncomplete
	}
	if err := s.validateConfirmationSources(ctx, tx, tenantID, group); err != nil {
		return Group{}, nil, err
	}
	now, rollback := time.Now().UTC(), uuid.NewString()
	created := make([]Candidate, 0, group.MemberCount)
	for _, member := range group.Members {
		candidateID := uuid.NewString()
		scoreJSON, _ := json.Marshal(group.Decision.ScoreCandidate)
		rubricJSON, _ := json.Marshal(group.Decision.RubricSelection)
		if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_automation_candidate(
  id,tenant_id,group_id,submission_id,segment_id,decision_revision,candidate_kind,
  score_candidate_json,rubric_selection_json,status,algorithm_version,rollback_reference,created_by
) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,'group_score',$7::jsonb,$8::jsonb,'active',$9,$10::uuid,$11::uuid)`,
			candidateID, tenantID, groupID, member.SubmissionID, member.SegmentID, group.Decision.Revision,
			scoreJSON, rubricJSON, group.AlgorithmVersion, rollback, actorID); err != nil {
			return Group{}, nil, err
		}
		created = append(created, Candidate{ID: candidateID, GroupID: groupID, SubmissionID: member.SubmissionID,
			SegmentID: member.SegmentID, DecisionRevision: group.Decision.Revision, Kind: CandidateGroupScore,
			ScoreCandidate: cloneMap(group.Decision.ScoreCandidate), RubricSelection: cloneMap(group.Decision.RubricSelection),
			Status: CandidateActive, AlgorithmVersion: group.AlgorithmVersion, RollbackReference: rollback, CreatedAt: now})
	}
	result, err := tx.ExecContext(ctx, `
UPDATE answer_group_decision SET confirmed_by=$4::uuid,confirmed_at=$5,rollback_reference=$6::uuid,
  sample_size=$7,updated_by=$4::uuid,updated_at=$5
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND revision=$3 AND confirmed_at IS NULL`,
		tenantID, groupID, input.ExpectedRevision, actorID, now, rollback, group.ReviewedSampleCount)
	if err != nil {
		return Group{}, nil, err
	}
	if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
		return Group{}, nil, ErrRevisionConflict
	}
	// 确认只发布可追溯的分组候选；候选仍需下游人工评分流程处理，不代表最终成绩。
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status='confirmed',updated_at=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, now); err != nil {
		return Group{}, nil, err
	}
	if err = insertOutbox(ctx, tx, tenantID, groupID, "answer_group.confirmed", map[string]any{
		"actor_id": actorID, "group_id": groupID, "affected_count": len(created),
		"algorithm_version": group.AlgorithmVersion, "representation_version": group.RepresentationVersion,
		"decision_revision": group.Decision.Revision, "rollback_reference": rollback,
	}); err != nil {
		return Group{}, nil, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, nil, err
	}
	return group, created, nil
}

func (s *PostgresStore) Rollback(ctx context.Context, tenantID, groupID, actorID string, input RollbackInput) (Group, []Candidate, error) {
	input.RollbackReference, input.Reason = strings.TrimSpace(input.RollbackReference), strings.TrimSpace(input.Reason)
	if s.db == nil || !validIDs(tenantID, groupID, actorID, input.RollbackReference) || input.Reason == "" || len(input.Reason) > 4000 {
		return Group{}, nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, nil, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, nil, err
	}
	if group.Status != StatusConfirmed || group.Decision == nil || group.Decision.RollbackReference != input.RollbackReference {
		return Group{}, nil, ErrRollbackConflict
	}
	now := time.Now().UTC()
	rows, err := tx.QueryContext(ctx, `
UPDATE answer_group_automation_candidate SET status='rolled_back',rolled_back_at=$4
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND rollback_reference=$3::uuid AND status='active'
RETURNING id::text,submission_id::text,segment_id::text,decision_revision,candidate_kind,score_candidate_json,rubric_selection_json,algorithm_version,created_at`,
		tenantID, groupID, input.RollbackReference, now)
	if err != nil {
		return Group{}, nil, err
	}
	updated := []Candidate{}
	for rows.Next() {
		var candidate Candidate
		var scoreRaw, rubricRaw []byte
		if err = rows.Scan(&candidate.ID, &candidate.SubmissionID, &candidate.SegmentID, &candidate.DecisionRevision, &candidate.Kind, &scoreRaw, &rubricRaw, &candidate.AlgorithmVersion, &candidate.CreatedAt); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		candidate.GroupID, candidate.Status, candidate.RollbackReference = groupID, CandidateRolledBack, input.RollbackReference
		if err = json.Unmarshal(scoreRaw, &candidate.ScoreCandidate); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		if err = json.Unmarshal(rubricRaw, &candidate.RubricSelection); err != nil {
			rows.Close()
			return Group{}, nil, err
		}
		updated = append(updated, candidate)
	}
	if err = rows.Close(); err != nil {
		return Group{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE answer_group_decision SET rolled_back_by=$4::uuid,rolled_back_at=$5,rollback_reason=$6,updated_by=$4::uuid,updated_at=$5
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND rollback_reference=$3::uuid`, tenantID, groupID, input.RollbackReference, actorID, now, input.Reason); err != nil {
		return Group{}, nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status='rolled_back',updated_at=$3 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, now); err != nil {
		return Group{}, nil, err
	}
	if err = insertOutbox(ctx, tx, tenantID, groupID, "answer_group.rolled_back", map[string]any{
		"actor_id": actorID, "group_id": groupID, "affected_count": len(updated),
		"algorithm_version": group.AlgorithmVersion, "rollback_reference": input.RollbackReference, "reason": input.Reason,
	}); err != nil {
		return Group{}, nil, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, nil, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, nil, err
	}
	return group, updated, nil
}

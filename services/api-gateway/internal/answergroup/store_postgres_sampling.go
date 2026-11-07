package answergroup

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *PostgresStore) ReviewSample(ctx context.Context, tenantID, groupID, memberSegmentID, actorID string, input SampleReviewInput) (Group, error) {
	input.Outcome, input.Notes = strings.TrimSpace(input.Outcome), strings.TrimSpace(input.Notes)
	if s.db == nil || !validIDs(tenantID, groupID, memberSegmentID, actorID) ||
		(input.Outcome != SampleAccepted && input.Outcome != SampleRejected) || len(input.Notes) > 4000 {
		return Group{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, err
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	found := false
	for _, member := range group.Members {
		if member.SegmentID == memberSegmentID {
			found = true
			break
		}
	}
	if !found {
		return Group{}, ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_sample_review(tenant_id,group_id,segment_id,outcome,notes,reviewed_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid)
ON CONFLICT(tenant_id,group_id,segment_id) DO UPDATE SET outcome=EXCLUDED.outcome,notes=EXCLUDED.notes,reviewed_by=EXCLUDED.reviewed_by,reviewed_at=now()`,
		tenantID, groupID, memberSegmentID, input.Outcome, input.Notes, actorID); err != nil {
		return Group{}, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, err
	}
	refreshReadiness(&group)
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, group.Status); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

func (s *PostgresStore) PutDecision(ctx context.Context, tenantID, groupID, actorID string, input DecisionInput) (Group, error) {
	if s.db == nil || !validIDs(tenantID, groupID, actorID) || len(input.ScoreCandidate) == 0 || len(input.RubricSelection) == 0 || input.ExpectedRevision < 0 {
		return Group{}, ErrInvalidInput
	}
	scoreJSON, err := json.Marshal(input.ScoreCandidate)
	if err != nil {
		return Group{}, ErrInvalidInput
	}
	rubricJSON, err := json.Marshal(input.RubricSelection)
	if err != nil {
		return Group{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Group{}, err
	}
	defer tx.Rollback()
	group, err := s.loadGroup(ctx, tx, tenantID, groupID, true)
	if err != nil {
		return Group{}, err
	}
	if group.Status == StatusConfirmed || group.Status == StatusRolledBack {
		return Group{}, ErrStateConflict
	}
	if group.Decision == nil {
		if input.ExpectedRevision != 0 {
			return Group{}, ErrRevisionConflict
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO answer_group_decision(
  tenant_id,group_id,score_candidate_json,rubric_selection_json,sample_size,min_sample,created_by,updated_by
) VALUES($1::uuid,$2::uuid,$3::jsonb,$4::jsonb,$5,$6,$7::uuid,$7::uuid)`,
			tenantID, groupID, scoreJSON, rubricJSON, group.ReviewedSampleCount, group.MinimumSample, actorID)
	} else {
		if group.Decision.Revision != input.ExpectedRevision {
			return Group{}, ErrRevisionConflict
		}
		result, updateErr := tx.ExecContext(ctx, `
UPDATE answer_group_decision SET score_candidate_json=$4::jsonb,rubric_selection_json=$5::jsonb,
  sample_size=$6,revision=revision+1,updated_by=$7::uuid,updated_at=now()
WHERE tenant_id=$1::uuid AND group_id=$2::uuid AND revision=$3 AND confirmed_at IS NULL AND rolled_back_at IS NULL`,
			tenantID, groupID, input.ExpectedRevision, scoreJSON, rubricJSON, group.ReviewedSampleCount, actorID)
		if updateErr != nil {
			return Group{}, updateErr
		}
		if count, countErr := result.RowsAffected(); countErr != nil || count != 1 {
			return Group{}, ErrRevisionConflict
		}
	}
	if err != nil {
		return Group{}, err
	}
	group, err = s.loadGroup(ctx, tx, tenantID, groupID, false)
	if err != nil {
		return Group{}, err
	}
	refreshReadiness(&group)
	if _, err = tx.ExecContext(ctx, `UPDATE answer_group SET status=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, groupID, group.Status); err != nil {
		return Group{}, err
	}
	if err = tx.Commit(); err != nil {
		return Group{}, err
	}
	return group, nil
}

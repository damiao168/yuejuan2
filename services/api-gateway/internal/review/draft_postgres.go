package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) GetDraft(ctx context.Context, tenantID, taskID, reviewerID string) (ReviewDraft, error) {
	return scanDraft(s.db.QueryRowContext(ctx, `
SELECT d.id::text,d.review_task_id::text,d.reviewer_id::text,d.score::float8,
 d.rubric_selections,d.comments,d.private_note,d.student_feedback,d.viewer_state,d.revision,d.client_updated_at,d.updated_at
FROM review_draft d JOIN review_task t ON t.tenant_id=d.tenant_id AND t.id=d.review_task_id
WHERE d.tenant_id=$1::uuid AND d.review_task_id=$2::uuid AND d.reviewer_id=$3::uuid
 AND d.deleted_at IS NULL AND t.deleted_at IS NULL AND t.assigned_to=$3::uuid`, tenantID, taskID, reviewerID))
}

func (s *PostgresStore) SaveDraft(ctx context.Context, tenantID, taskID, reviewerID string, input SaveDraftInput) (ReviewDraft, error) {
	if input.ExpectedRevision < 0 {
		return ReviewDraft{}, ErrInvalidInput
	}
	if input.Score != nil && *input.Score < 0 {
		return ReviewDraft{}, ErrInvalidInput
	}
	if input.ViewerState == nil {
		input.ViewerState = map[string]any{}
	}
	rubric, _ := json.Marshal(input.RubricSelections)
	viewer, _ := json.Marshal(input.ViewerState)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewDraft{}, err
	}
	defer tx.Rollback()
	var ownedTask int
	// 先锁任务再锁草稿，防止保存过程中任务被转派或提交；首次保存也受任务行锁保护。
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM review_task t WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.assigned_to=$3::uuid AND t.deleted_at IS NULL AND t.status IN ('assigned','in_progress','returned','pending') FOR UPDATE`, tenantID, taskID, reviewerID).Scan(&ownedTask)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewDraft{}, ErrForbidden
	}
	if err != nil {
		return ReviewDraft{}, err
	}
	var current int
	err = tx.QueryRowContext(ctx, `SELECT d.revision FROM review_draft d JOIN review_task t ON t.tenant_id=d.tenant_id AND t.id=d.review_task_id WHERE d.tenant_id=$1::uuid AND d.review_task_id=$2::uuid AND d.reviewer_id=$3::uuid AND d.deleted_at IS NULL AND t.deleted_at IS NULL AND t.assigned_to=$3::uuid AND t.status IN ('assigned','in_progress','returned','pending') FOR UPDATE OF d`, tenantID, taskID, reviewerID).Scan(&current)
	var out ReviewDraft
	// 草稿版本控制覆盖冲突；客户端时间仅作记录，不能决定哪次保存生效。
	if errors.Is(err, sql.ErrNoRows) {
		if input.ExpectedRevision != 0 {
			return out, ErrRevisionConflict
		}
		out, err = scanDraft(tx.QueryRowContext(ctx, `INSERT INTO review_draft(tenant_id,review_task_id,reviewer_id,score,rubric_selections,comments,private_note,student_feedback,viewer_state,client_updated_at) SELECT $1::uuid,t.id,$3::uuid,$4,$5::jsonb,$6,$7,$8,$9::jsonb,$10 FROM review_task t WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.assigned_to=$3::uuid AND t.deleted_at IS NULL AND t.status IN ('pending','assigned','in_progress','returned') RETURNING id::text,review_task_id::text,reviewer_id::text,score::float8,rubric_selections,comments,private_note,student_feedback,viewer_state,revision,client_updated_at,updated_at`, tenantID, taskID, reviewerID, input.Score, rubric, input.Comments, input.PrivateNote, input.StudentFeedback, viewer, input.ClientUpdatedAt))
	} else if err != nil {
		return out, err
	} else {
		if current != input.ExpectedRevision {
			return out, ErrRevisionConflict
		}
		out, err = scanDraft(tx.QueryRowContext(ctx, `UPDATE review_draft d SET score=$4,rubric_selections=$5::jsonb,comments=$6,private_note=$7,student_feedback=$8,viewer_state=$9::jsonb,client_updated_at=$11,revision=d.revision+1,updated_at=now() FROM review_task t WHERE d.tenant_id=$1::uuid AND d.review_task_id=$2::uuid AND d.reviewer_id=$3::uuid AND d.revision=$10 AND d.deleted_at IS NULL AND t.tenant_id=d.tenant_id AND t.id=d.review_task_id AND t.assigned_to=$3::uuid AND t.deleted_at IS NULL AND t.status IN ('pending','assigned','in_progress','returned') RETURNING d.id::text,d.review_task_id::text,d.reviewer_id::text,d.score::float8,d.rubric_selections,d.comments,d.private_note,d.student_feedback,d.viewer_state,d.revision,d.client_updated_at,d.updated_at`, tenantID, taskID, reviewerID, input.Score, rubric, input.Comments, input.PrivateNote, input.StudentFeedback, viewer, input.ExpectedRevision, input.ClientUpdatedAt))
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

type draftScanner interface{ Scan(...any) error }

func scanDraft(row draftScanner) (ReviewDraft, error) {
	var out ReviewDraft
	var score sql.NullFloat64
	var rubric, viewer []byte
	err := row.Scan(&out.ID, &out.ReviewTaskID, &out.ReviewerID, &score, &rubric, &out.Comments, &out.PrivateNote, &out.StudentFeedback, &viewer, &out.Revision, &out.ClientUpdatedAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if score.Valid {
		v := score.Float64
		out.Score = &v
	}
	if err := decodeJSONB(rubric, &out.RubricSelections, "review_draft.rubric_selections"); err != nil {
		return out, err
	}
	if err := decodeJSONB(viewer, &out.ViewerState, "review_draft.viewer_state"); err != nil {
		return out, err
	}
	if out.RubricSelections == nil {
		out.RubricSelections = []RubricSelection{}
	}
	if out.ViewerState == nil {
		out.ViewerState = map[string]any{}
	}
	return out, nil
}

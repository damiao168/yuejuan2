package appeal

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) ReviewAppeal(ctx context.Context, tenantID string, id string, actorID string, input ReviewAppealInput) (Appeal, *ScoreAdjustment, error) {
	input = normalizeReviewInput(input)
	if err := validateReviewInput(input); err != nil {
		return Appeal{}, nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Appeal{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := scanAppeal(tx.QueryRowContext(ctx, `
SELECT `+appealColumns()+`
FROM appeal
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return Appeal{}, nil, err
	}
	if item.Status == "closed" {
		return Appeal{}, nil, ErrInvalidTransition
	}
	if input.ExpectedRevision <= 0 || item.Revision != input.ExpectedRevision {
		return Appeal{}, nil, ErrRevisionConflict
	}
	if !canReviewTransition(item.Status, input.Status) {
		return Appeal{}, nil, ErrInvalidTransition
	}
	var adjustment *ScoreAdjustment
	if input.Status == "score_adjusted" {
		finalID := input.FinalGradeID
		if finalID == "" {
			finalID = item.FinalGradeID
		}
		if finalID == "" || input.AdjustedScore == nil {
			return Appeal{}, nil, ErrInvalidInput
		}
		final, err := s.getFinalGradeRefTx(ctx, tx, tenantID, finalID, item.SubmissionID)
		if err != nil {
			return Appeal{}, nil, err
		}
		if *input.AdjustedScore < 0 || *input.AdjustedScore > final.MaxScore {
			return Appeal{}, nil, ErrInvalidInput
		}
		adj, err := s.applyAdjustmentTx(ctx, tx, tenantID, item, final, actorID, scoreAdjustmentDecision{
			Reason: input.Reason, Score: *input.AdjustedScore,
		})
		if err != nil {
			return Appeal{}, nil, err
		}
		adjustment = &adj
		item.FinalGradeID = final.ID
		item.QuestionID = final.QuestionID
		item.QuestionNo = final.QuestionNo
	}
	item, err = scanAppeal(tx.QueryRowContext(ctx, `
UPDATE appeal
SET status = $3,
    result_reason = $4,
    assigned_to = NULLIF($5, '')::uuid,
    reviewed_by = $6::uuid,
    reviewed_at = now(),
    final_grade_id = NULLIF($7, '')::uuid,
    question_id = NULLIF($8, '')::uuid,
    question_no = $9,
    revision = revision + 1,
    updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $10
RETURNING `+appealColumns()+`
`, tenantID, id, input.Status, input.Reason, input.AssignedTo, actorID, item.FinalGradeID, item.QuestionID, item.QuestionNo, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Appeal{}, nil, ErrRevisionConflict
		}
		return Appeal{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Appeal{}, nil, err
	}
	item, err = s.withDetails(ctx, item)
	return item, adjustment, err
}

type finalGradeRef struct {
	ID              string
	QuestionID      string
	QuestionNo      string
	AnswerSegmentID string
	SubmissionID    string
	Score           float64
	MaxScore        float64
}

func (s *PostgresStore) getFinalGradeRefTx(ctx context.Context, tx *sql.Tx, tenantID string, finalGradeID string, submissionID string) (finalGradeRef, error) {
	var out finalGradeRef
	if err := tx.QueryRowContext(ctx, `
SELECT id::text, question_id::text, question_no, answer_segment_id::text, submission_id::text, score::float8, max_score::float8
FROM final_grade
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, finalGradeID, submissionID).Scan(
		&out.ID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.Score,
		&out.MaxScore,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return finalGradeRef{}, ErrInvalidInput
		}
		return finalGradeRef{}, err
	}
	return out, nil
}

type scoreAdjustmentDecision struct {
	Reason string
	Score  float64
}

// 调分流水、单题分数和总分增量共用裁决事务；任何一步失败都不能留下部分调分。
func (s *PostgresStore) applyAdjustmentTx(ctx context.Context, tx *sql.Tx, tenantID string, item Appeal, final finalGradeRef, actorID string, decision scoreAdjustmentDecision) (ScoreAdjustment, error) {
	previous := final.Score
	delta := decision.Score - previous
	adjustment, err := scanAdjustment(tx.QueryRowContext(ctx, `
INSERT INTO score_adjustment (
  tenant_id, appeal_id, exam_id, submission_id, submission_grade_id, final_grade_id,
  question_id, question_no, previous_score, adjusted_score, delta, reason, adjusted_by
)
VALUES ($1, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7::uuid, $8, $9, $10, $11, $12, $13::uuid)
RETURNING id::text, tenant_id::text, appeal_id::text, exam_id::text, submission_id::text,
  submission_grade_id::text, final_grade_id::text, question_id::text, question_no,
  previous_score::float8, adjusted_score::float8, delta::float8, reason, adjusted_by::text, created_at
`, tenantID, item.ID, item.ExamID, item.SubmissionID, item.SubmissionGradeID, final.ID,
		final.QuestionID, final.QuestionNo, previous, decision.Score, delta, decision.Reason, actorID))
	if err != nil {
		return ScoreAdjustment{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE final_grade
SET score = $4, status = 'locked', locked = true, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3
`, tenantID, final.ID, item.SubmissionID, decision.Score); err != nil {
		return ScoreAdjustment{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE submission_grade
SET total_score = total_score + $4, status = 'published', locked = true, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND submission_id::text = $3
`, tenantID, item.SubmissionGradeID, item.SubmissionID, delta); err != nil {
		return ScoreAdjustment{}, err
	}
	return adjustment, nil
}

func (s *PostgresStore) listAdjustments(ctx context.Context, tenantID string, appealID string) ([]ScoreAdjustment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, appeal_id::text, exam_id::text, submission_id::text,
  submission_grade_id::text, final_grade_id::text, question_id::text, question_no,
  previous_score::float8, adjusted_score::float8, delta::float8, reason, adjusted_by::text, created_at
FROM score_adjustment
WHERE tenant_id = $1 AND appeal_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
`, tenantID, appealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoreAdjustment{}
	for rows.Next() {
		item, err := scanAdjustment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanAdjustment(row scanner) (ScoreAdjustment, error) {
	var out ScoreAdjustment
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.AppealID,
		&out.ExamID,
		&out.SubmissionID,
		&out.SubmissionGradeID,
		&out.FinalGradeID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.PreviousScore,
		&out.AdjustedScore,
		&out.Delta,
		&out.Reason,
		&out.AdjustedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScoreAdjustment{}, ErrNotFound
		}
		return ScoreAdjustment{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

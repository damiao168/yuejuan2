package review

import (
	"context"
	"database/sql"
	"errors"
)

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner) (ReviewTask, error) {
	var out ReviewTask
	var dueAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.Source,
		&out.Status,
		&out.Priority,
		&out.AssignedTo,
		&out.ReturnReason,
		&out.GradeRound,
		&dueAt,
		&out.Revision,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewTask{}, ErrNotFound
		}
		return ReviewTask{}, err
	}
	if dueAt.Valid {
		out.DueAt = &dueAt.Time
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type gradeScanner interface {
	Scan(dest ...any) error
}

func scanGrade(row gradeScanner) (HumanGrade, error) {
	var out HumanGrade
	var selectionsRaw []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ReviewTaskID,
		&out.AnswerSegmentID,
		&out.ReviewerID,
		&out.Score,
		&out.MaxScore,
		&selectionsRaw,
		&out.Comments,
		&out.PrivateNote,
		&out.StudentFeedback,
		&out.Reason,
		&out.GradeRound,
		&out.AIGradeID,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return HumanGrade{}, ErrNotFound
		}
		return HumanGrade{}, err
	}
	if err := decodeJSONB(selectionsRaw, &out.RubricSelections, "human_grade.rubric_selections"); err != nil {
		return HumanGrade{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}

func (s *PostgresStore) getTaskTx(ctx context.Context, tx *sql.Tx, tenantID string, id string) (ReviewTask, error) {
	return scanTask(tx.QueryRowContext(ctx, `
	SELECT `+reviewTaskColumns+`
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
}

func (s *PostgresStore) insertReviewTaskTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, taskContext Context, input CreateTaskInput) (ReviewTask, error) {
	input = normalizeCreateTask(input)
	if validateCreateTask(input) != nil {
		return ReviewTask{}, ErrInvalidInput
	}
	status := "pending"
	if input.AssignedTo != "" {
		status = "assigned"
	}
	return scanTask(tx.QueryRowContext(ctx, `
INSERT INTO review_task (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id,
  anonymous_code, source, status, priority, assigned_to, grade_round, due_at, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid, $12, $13, $14)
RETURNING `+reviewTaskColumns+`
`, tenantID, taskContext.ExamID, taskContext.Question.ID, taskContext.Question.QuestionNo,
		input.AnswerSegmentID, taskContext.SubmissionID, taskContext.AnonymousCode,
		input.Source, status, input.Priority, input.AssignedTo, input.GradeRound, input.DueAt, actorID))
}

// 题目策略优先于考试策略，明确禁用也算覆盖；都未配置时才根据考试阅卷模式给出默认双评策略。
func (s *PostgresStore) effectivePolicyTx(ctx context.Context, tx *sql.Tx, tenantID string, examID string, questionID string) (DoubleMarkPolicy, error) {
	if questionID != "" {
		policy, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
FROM double_mark_policy
WHERE tenant_id = $1 AND exam_id::text = $2 AND question_id::text = $3 AND deleted_at IS NULL
`, tenantID, examID, questionID))
		if err == nil {
			return policy, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return DoubleMarkPolicy{}, err
		}
	}
	policy, err := scanPolicy(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
FROM double_mark_policy
WHERE tenant_id = $1 AND exam_id::text = $2 AND question_id IS NULL AND deleted_at IS NULL
`, tenantID, examID))
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return DoubleMarkPolicy{}, err
	}
	var gradingMode string
	if getErr := tx.QueryRowContext(ctx, `
SELECT grading_mode
FROM exam
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, examID).Scan(&gradingMode); getErr != nil {
		if errors.Is(getErr, sql.ErrNoRows) {
			return DoubleMarkPolicy{}, ErrNotFound
		}
		return DoubleMarkPolicy{}, getErr
	}
	if gradingMode != "double_mark" && gradingMode != "blind_double_mark" {
		return DoubleMarkPolicy{}, ErrNotFound
	}
	return DoubleMarkPolicy{
		TenantID:           tenantID,
		ExamID:             examID,
		Enabled:            true,
		Threshold:          0,
		ResolutionStrategy: "average",
	}, nil
}

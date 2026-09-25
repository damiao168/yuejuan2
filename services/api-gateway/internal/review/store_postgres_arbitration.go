package review

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"errors"
	"math"
	"strings"
)

func (s *PostgresStore) CreateArbitrationTask(ctx context.Context, tenantID string, actorID string, input CreateArbitrationTaskInput) (ArbitrationTask, error) {
	input.DoubleMarkSessionID = stringsTrim(input.DoubleMarkSessionID)
	input.AssignedTo = stringsTrim(input.AssignedTo)
	input.DifferenceReason = stringsTrim(input.DifferenceReason)
	if input.DoubleMarkSessionID == "" {
		return ArbitrationTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArbitrationTask{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockScoringRunForSessionTx(ctx, tx, tenantID, input.DoubleMarkSessionID); err != nil {
		return ArbitrationTask{}, err
	}
	session, err := s.getSessionTx(ctx, tx, tenantID, input.DoubleMarkSessionID, true)
	if err != nil {
		return ArbitrationTask{}, err
	}
	if session.ArbitrationTaskID != "" || session.Status == "cancelled" {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	first, firstOK, err := s.latestGradeTx(ctx, tx, tenantID, session.FirstReviewTaskID)
	if err != nil {
		return ArbitrationTask{}, err
	}
	second, secondOK, err := s.latestGradeTx(ctx, tx, tenantID, session.SecondReviewTaskID)
	if err != nil {
		return ArbitrationTask{}, err
	}
	if !firstOK || !secondOK {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	diff := math.Abs(first.Score - second.Score)
	if diff <= session.Threshold {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	policy, _ := s.effectivePolicyTx(ctx, tx, tenantID, session.ExamID, session.QuestionID)
	task, err := s.createArbitrationTaskTx(ctx, tx, tenantID, actorID, session, first, second, input.DifferenceReason, input.AssignedTo, policy.AllowSameArbitrator)
	if err != nil {
		return ArbitrationTask{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE double_mark_session
SET status = 'needs_arbitration', score_difference = $3, arbitration_task_id = $4::uuid, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, session.ID, diff, task.ID); err != nil {
		return ArbitrationTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArbitrationTask{}, err
	}
	return task, nil
}

func (s *PostgresStore) ListArbitrationTasks(ctx context.Context, tenantID string, filter ArbitrationFilter) ([]ArbitrationTask, error) {
	scopeMode := filter.ScopeMode
	if scopeMode == "" {
		scopeMode = "tenant"
	}
	rows, err := s.db.QueryContext(ctx, arbitrationSelect()+`
WHERE arbitration_task.tenant_id = $1 AND arbitration_task.deleted_at IS NULL
  AND ($2 = '' OR ($2 = 'active' AND status IN ('pending', 'assigned')) OR status = $2)
  AND ($3 = '' OR COALESCE(assigned_to::text, '') = $3)
  AND ($4 = '' OR exam_id::text = $4)
  AND ($6 = '' OR created_at < $5 OR (created_at = $5 AND id::text < $6))
  AND (
    $8 IN ('platform', 'tenant')
    OR ($8 = 'school' AND EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id = arbitration_task.tenant_id AND e.id = arbitration_task.exam_id AND e.school_id::text = ANY(string_to_array(NULLIF($9, ''), ','))))
    OR ($8 = 'class' AND exam_id::text = ANY(string_to_array(NULLIF($10, ''), ',')))
    OR ($8 = 'assigned' AND (COALESCE(assigned_to::text, '') = $11 OR id::text = ANY(string_to_array(NULLIF($12, ''), ','))))
  )
ORDER BY created_at DESC, id::text DESC
LIMIT NULLIF($7, 0)
`, tenantID, filter.Status, filter.AssignedTo, filter.ExamID,
		filter.CursorCreatedAt, filter.CursorID, filter.Limit,
		scopeMode, strings.Join(filter.ScopeSchoolIDs, ","), strings.Join(filter.ScopeExamIDs, ","),
		filter.ScopeActorID, strings.Join(filter.ScopeTaskIDs, ","))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArbitrationTask{}
	for rows.Next() {
		task, err := scanArbitration(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetArbitrationTask(ctx context.Context, tenantID string, id string) (ArbitrationTask, error) {
	return scanArbitration(s.db.QueryRowContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
}

func (s *PostgresStore) AssignArbitrationTask(ctx context.Context, tenantID string, id string, _ string, input AssignArbitrationTaskInput) (ArbitrationTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return ArbitrationTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArbitrationTask{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := lockScoringRunForArbitrationTx(ctx, tx, tenantID, id); err != nil {
		return ArbitrationTask{}, err
	}
	task, err := scanArbitration(tx.QueryRowContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return ArbitrationTask{}, err
	}
	if task.Status != "pending" && task.Status != "assigned" {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ArbitrationTask{}, ErrRevisionConflict
	}
	if !arbitratorAllowed(task, input.AssignedTo) {
		return ArbitrationTask{}, ErrForbidden
	}
	task, err = scanArbitration(tx.QueryRowContext(ctx, `
UPDATE arbitration_task
SET assigned_to = $3::uuid, status = 'assigned', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4
RETURNING id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, revision, created_by::text, created_at, updated_at
`, tenantID, id, input.AssignedTo, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArbitrationTask{}, ErrRevisionConflict
		}
		return ArbitrationTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArbitrationTask{}, err
	}
	return task, nil
}

func (s *PostgresStore) SubmitArbitration(ctx context.Context, tenantID string, id string, arbitratorID string, input SubmitArbitrationInput) (ArbitrationTask, FinalGrade, error) {
	input.Reason = stringsTrim(input.Reason)
	input.StudentFeedback = stringsTrim(input.StudentFeedback)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var replay ArbitrationSubmitResult
	if found, err := commandreceipt.Load(ctx, tx, tenantID, arbitratorID, "review.arbitrate", id, input, &replay); err != nil || found {
		return replay.Task, replay.Grade, err
	}
	// Match scoring cancellation's lock order before locking the task.
	scoringRunID, err := lockScoringRunForArbitrationTx(ctx, tx, tenantID, id)
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	task, err := scanArbitration(tx.QueryRowContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if task.Status != "pending" && task.Status != "assigned" {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ArbitrationTask{}, FinalGrade{}, ErrRevisionConflict
	}
	if task.AssignedTo == "" || task.AssignedTo != arbitratorID {
		return ArbitrationTask{}, FinalGrade{}, ErrForbidden
	}
	if !arbitratorAllowed(task, arbitratorID) {
		return ArbitrationTask{}, FinalGrade{}, ErrForbidden
	}
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, task.AnswerSegmentID)
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if input.FinalScore < 0 || input.FinalScore > taskContext.Question.Score {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	session, err := s.getSessionTx(ctx, tx, tenantID, task.DoubleMarkSessionID, true)
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	finalGrade, err := s.insertFinalGradeTx(ctx, tx, tenantID, arbitratorID, session, input.FinalScore, "arbitration", task.ID, "arbitration")
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	task, err = scanArbitration(tx.QueryRowContext(ctx, `
UPDATE arbitration_task
SET assigned_to = $3::uuid, status = 'submitted', final_score = $4, reason = $5, student_feedback = $6,
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $7
RETURNING id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, revision, created_by::text, created_at, updated_at
`, tenantID, id, arbitratorID, input.FinalScore, input.Reason, input.StudentFeedback, input.ExpectedRevision))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArbitrationTask{}, FinalGrade{}, ErrRevisionConflict
		}
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE double_mark_session
SET status = 'arbitrated', final_grade_id = $3::uuid, arbitration_task_id = $4::uuid, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, session.ID, finalGrade.ID, task.ID); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if err := s.completeSessionReviewTasksTx(ctx, tx, tenantID, session); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if scoringRunID.Valid {
		if _, err := s.recordScoringDoubleMarkGradeTx(ctx, tx, tenantID, scoringRunID.String, session.SecondReviewTaskID, arbitratorID, &finalGrade); err != nil {
			return ArbitrationTask{}, FinalGrade{}, err
		}
		if err := s.syncScoringDoubleMarkRunTx(ctx, tx, tenantID, scoringRunID.String); err != nil {
			return ArbitrationTask{}, FinalGrade{}, err
		}
	}
	if err := commandreceipt.Save(ctx, tx, tenantID, arbitratorID, "review.arbitrate", id, input, ArbitrationSubmitResult{Task: task, Grade: finalGrade}); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if err := tx.Commit(); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	return task, finalGrade, nil
}

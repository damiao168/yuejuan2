package review

import (
	"context"
	"database/sql"
	"errors"
)

func (s *PostgresStore) SetExamDoubleMarkPolicy(ctx context.Context, tenantID string, examID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error) {
	examID = stringsTrim(examID)
	input = normalizePolicyInput(input)
	if examID == "" || validatePolicyInput(input) != nil {
		return DoubleMarkPolicy{}, ErrInvalidInput
	}
	policy, err := scanPolicy(s.db.QueryRowContext(ctx, `
UPDATE double_mark_policy
SET enabled = $3, threshold = $4, resolution_strategy = $5, allow_same_arbitrator = $6, updated_at = now()
WHERE tenant_id = $1 AND exam_id::text = $2 AND question_id IS NULL AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
`, tenantID, examID, input.Enabled, input.Threshold, input.ResolutionStrategy, input.AllowSameArbitrator))
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return DoubleMarkPolicy{}, err
	}
	return scanPolicy(s.db.QueryRowContext(ctx, `
INSERT INTO double_mark_policy (
  tenant_id, exam_id, question_id, enabled, threshold, resolution_strategy, allow_same_arbitrator, created_by
)
VALUES ($1, $2::uuid, NULL, $3, $4, $5, $6, $7)
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
`, tenantID, examID, input.Enabled, input.Threshold, input.ResolutionStrategy, input.AllowSameArbitrator, actorID))
}

func (s *PostgresStore) SetQuestionDoubleMarkPolicy(ctx context.Context, tenantID string, questionID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error) {
	questionID = stringsTrim(questionID)
	input = normalizePolicyInput(input)
	if questionID == "" || validatePolicyInput(input) != nil {
		return DoubleMarkPolicy{}, ErrInvalidInput
	}
	var examID string
	if err := s.db.QueryRowContext(ctx, `
SELECT exam_id::text
FROM question
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, questionID).Scan(&examID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DoubleMarkPolicy{}, ErrNotFound
		}
		return DoubleMarkPolicy{}, err
	}
	policy, err := scanPolicy(s.db.QueryRowContext(ctx, `
UPDATE double_mark_policy
SET enabled = $3, threshold = $4, resolution_strategy = $5, allow_same_arbitrator = $6, updated_at = now()
WHERE tenant_id = $1 AND question_id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
`, tenantID, questionID, input.Enabled, input.Threshold, input.ResolutionStrategy, input.AllowSameArbitrator))
	if err == nil {
		return policy, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return DoubleMarkPolicy{}, err
	}
	return scanPolicy(s.db.QueryRowContext(ctx, `
INSERT INTO double_mark_policy (
  tenant_id, exam_id, question_id, enabled, threshold, resolution_strategy, allow_same_arbitrator, created_by
)
VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8)
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
`, tenantID, examID, questionID, input.Enabled, input.Threshold, input.ResolutionStrategy, input.AllowSameArbitrator, actorID))
}

func (s *PostgresStore) ListDoubleMarkPolicies(ctx context.Context, tenantID string, filter PolicyFilter) ([]DoubleMarkPolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(question_id::text, ''),
  enabled, threshold::float8, resolution_strategy, allow_same_arbitrator, created_by::text, created_at, updated_at
FROM double_mark_policy
WHERE tenant_id = $1 AND deleted_at IS NULL
  AND ($2 = '' OR exam_id::text = $2)
  AND ($3 = '' OR COALESCE(question_id::text, '') = $3)
ORDER BY exam_id, question_id NULLS FIRST, created_at
`, tenantID, filter.ExamID, filter.QuestionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DoubleMarkPolicy{}
	for rows.Next() {
		policy, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, policy)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateDoubleMarkSession(ctx context.Context, tenantID string, actorID string, input CreateDoubleMarkSessionInput) (DoubleMarkSession, error) {
	input.AnswerSegmentID = stringsTrim(input.AnswerSegmentID)
	input.FirstReviewerID = stringsTrim(input.FirstReviewerID)
	input.SecondReviewerID = stringsTrim(input.SecondReviewerID)
	if input.AnswerSegmentID == "" || input.FirstReviewerID == "" || input.SecondReviewerID == "" || input.FirstReviewerID == input.SecondReviewerID || input.Priority < 0 {
		return DoubleMarkSession{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DoubleMarkSession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, input.AnswerSegmentID)
	if err != nil {
		return DoubleMarkSession{}, err
	}
	policy, err := s.effectivePolicyTx(ctx, tx, tenantID, taskContext.ExamID, taskContext.Question.ID)
	if err != nil {
		return DoubleMarkSession{}, err
	}
	if !policy.Enabled {
		return DoubleMarkSession{}, ErrInvalidInput
	}
	// 两个任务与双评会话一起提交，任一步失败都会回滚，避免只派出一份评分任务。
	firstTask, err := s.insertReviewTaskTx(ctx, tx, tenantID, actorID, taskContext, CreateTaskInput{
		AnswerSegmentID: input.AnswerSegmentID,
		Source:          "double_mark_required",
		Priority:        input.Priority,
		AssignedTo:      input.FirstReviewerID,
		GradeRound:      "first_mark",
		DueAt:           cloneTime(input.DueAt),
	})
	if err != nil {
		return DoubleMarkSession{}, err
	}
	secondTask, err := s.insertReviewTaskTx(ctx, tx, tenantID, actorID, taskContext, CreateTaskInput{
		AnswerSegmentID: input.AnswerSegmentID,
		Source:          "double_mark_required",
		Priority:        input.Priority,
		AssignedTo:      input.SecondReviewerID,
		GradeRound:      "second_mark",
		DueAt:           cloneTime(input.DueAt),
	})
	if err != nil {
		return DoubleMarkSession{}, err
	}
	session, err := scanSession(tx.QueryRowContext(ctx, `
INSERT INTO double_mark_session (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  first_review_task_id, second_review_task_id, first_reviewer_id, second_reviewer_id,
  threshold, resolution_strategy, status, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8::uuid, $9::uuid, $10::uuid, $11::uuid, $12, $13, 'pending', $14)
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_review_task_id::text, second_review_task_id::text, first_reviewer_id::text, second_reviewer_id::text,
  threshold::float8, resolution_strategy, status, score_difference::float8,
  COALESCE(final_grade_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  created_by::text, created_at, updated_at
`, tenantID, taskContext.ExamID, taskContext.Question.ID, taskContext.Question.QuestionNo,
		input.AnswerSegmentID, taskContext.SubmissionID, taskContext.AnonymousCode,
		firstTask.ID, secondTask.ID, input.FirstReviewerID, input.SecondReviewerID,
		policy.Threshold, policy.ResolutionStrategy, actorID))
	if err != nil {
		return DoubleMarkSession{}, err
	}
	if err := tx.Commit(); err != nil {
		return DoubleMarkSession{}, err
	}
	return session, nil
}

func (s *PostgresStore) ListDoubleMarkSessions(ctx context.Context, tenantID string, filter DoubleMarkSessionFilter) ([]DoubleMarkSession, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_review_task_id::text, second_review_task_id::text, first_reviewer_id::text, second_reviewer_id::text,
  threshold::float8, resolution_strategy, status, score_difference::float8,
  COALESCE(final_grade_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  created_by::text, created_at, updated_at
FROM double_mark_session
WHERE tenant_id = $1 AND deleted_at IS NULL
   AND ($2 = '' OR status = $2)
   AND ($3 = '' OR answer_segment_id::text = $3)
   AND ($4 = '' OR exam_id::text = $4)
   AND ($5 = '' OR question_id::text = $5)
ORDER BY created_at DESC
	`, tenantID, filter.Status, filter.AnswerSegmentID, filter.ExamID, filter.QuestionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DoubleMarkSession{}
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetDoubleMarkSession(ctx context.Context, tenantID string, id string) (DoubleMarkSession, error) {
	return scanSession(s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_review_task_id::text, second_review_task_id::text, first_reviewer_id::text, second_reviewer_id::text,
  threshold::float8, resolution_strategy, status, score_difference::float8,
  COALESCE(final_grade_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  created_by::text, created_at, updated_at
FROM double_mark_session
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id))
}

package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateTask(ctx context.Context, tenantID string, actorID string, input CreateTaskInput) (ReviewTask, error) {
	input = normalizeCreateTask(input)
	if validateCreateTask(input) != nil {
		return ReviewTask{}, ErrInvalidInput
	}
	taskContext, err := s.loadContext(ctx, tenantID, input.AnswerSegmentID)
	if err != nil {
		return ReviewTask{}, err
	}
	status := "pending"
	if input.AssignedTo != "" {
		status = "assigned"
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO review_task (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id,
  anonymous_code, source, status, priority, assigned_to, grade_round, due_at, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid, $12, $13, $14)
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, taskContext.ExamID, taskContext.Question.ID, taskContext.Question.QuestionNo, input.AnswerSegmentID,
		taskContext.SubmissionID, taskContext.AnonymousCode, input.Source, status, input.Priority,
		input.AssignedTo, input.GradeRound, input.DueAt, actorID)
	return scanTask(row)
}

func (s *PostgresStore) ListTasks(ctx context.Context, tenantID string, filter ListFilter) ([]ReviewTask, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
FROM review_task
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR assigned_to::text = $3)
  AND ($4 = '' OR exam_id::text = $4)
ORDER BY priority DESC, created_at ASC
`, tenantID, filter.Status, filter.AssignedTo, filter.ExamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReviewTask{}
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID string, id string) (ReviewTask, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	return scanTask(row)
}

func (s *PostgresStore) AssignTask(ctx context.Context, tenantID string, id string, _ string, input AssignTaskInput) (ReviewTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" {
		return ReviewTask{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE review_task
SET assigned_to = $3::uuid, status = 'assigned', return_reason = '', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed')
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, id, input.AssignedTo)
	task, err := scanTask(row)
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.GetTask(ctx, tenantID, id); getErr == nil {
			return ReviewTask{}, ErrInvalidTransition
		}
	}
	return task, err
}

func (s *PostgresStore) BatchAssignTasks(ctx context.Context, tenantID string, _ string, input BatchAssignInput) ([]ReviewTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" || len(input.TaskIDs) == 0 {
		return nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := make([]ReviewTask, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		if stringsTrim(id) == "" {
			return nil, ErrInvalidInput
		}
		task, err := scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET assigned_to = $3::uuid, status = 'assigned', return_reason = '', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed')
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, id, input.AssignedTo))
		if errors.Is(err, ErrNotFound) {
			if _, getErr := s.GetTask(ctx, tenantID, id); getErr == nil {
				return nil, ErrInvalidTransition
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) SubmitGrade(ctx context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput) (SubmitResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SubmitResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return SubmitResult{}, err
	}
	if task.AssignedTo != reviewerID {
		return SubmitResult{}, ErrForbidden
	}
	if !canSubmit(task.Status) {
		return SubmitResult{}, ErrInvalidTransition
	}
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, task.AnswerSegmentID)
	if err != nil {
		return SubmitResult{}, err
	}
	if err := validateSubmit(input, taskContext); err != nil {
		return SubmitResult{}, err
	}
	selections, _ := json.Marshal(input.RubricSelections)
	grade, err := scanGrade(tx.QueryRowContext(ctx, `
INSERT INTO human_grade (
  tenant_id, review_task_id, answer_segment_id, reviewer_id, score, max_score,
  rubric_selections, comments, private_note, student_feedback, reason, grade_round, ai_grade_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, '')::uuid)
RETURNING id::text, tenant_id::text, review_task_id::text, answer_segment_id::text, reviewer_id::text,
  score::float8, max_score::float8, rubric_selections, comments, private_note, student_feedback,
  reason, grade_round, COALESCE(ai_grade_id::text, ''), created_at
`, tenantID, task.ID, task.AnswerSegmentID, reviewerID, input.Score, taskContext.Question.Score,
		selections, stringsTrim(input.Comments), stringsTrim(input.PrivateNote),
		stringsTrim(input.StudentFeedback), stringsTrim(input.Reason), task.GradeRound,
		aiGradeIDFromContext(taskContext)))
	if err != nil {
		return SubmitResult{}, err
	}
	questionGradeID := ""
	if task.GradeRound == "single" {
		evidence, marshalErr := json.Marshal(map[string]any{
			"human_grade_id":    grade.ID,
			"rubric_selections": input.RubricSelections,
			"comments":          stringsTrim(input.Comments),
			"student_feedback":  stringsTrim(input.StudentFeedback),
			"reason":            stringsTrim(input.Reason),
		})
		if marshalErr != nil {
			return SubmitResult{}, marshalErr
		}
		var priorID string
		_ = tx.QueryRowContext(ctx, `SELECT id::text FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL FOR UPDATE`, tenantID, task.AnswerSegmentID).Scan(&priorID)
		if priorID != "" {
			if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='superseded' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, priorID); err != nil {
				return SubmitResult{}, err
			}
		}
		err = tx.QueryRowContext(ctx, `
INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,review_task_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
SELECT rt.tenant_id,rt.exam_id,rt.submission_id,rt.question_id,rt.answer_segment_id,rt.scoring_run_id,rt.id,'human','confirmed',$3,$4,$5::jsonb,
  (SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=rt.answer_segment_id),NULLIF($6,'')::uuid,true,$7::uuid
FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid
RETURNING id::text`, tenantID, task.ID, input.Score, taskContext.Question.Score, evidence, priorID, reviewerID).Scan(&questionGradeID)
		if err != nil {
			return SubmitResult{}, err
		}
	}
	task, err = scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET status = 'submitted', current_grade_id = NULLIF($3,'')::uuid,
  claim_expires_at = NULL, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, id, questionGradeID))
	if err != nil {
		return SubmitResult{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE review_draft SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND review_task_id=$2::uuid AND reviewer_id=$3::uuid AND deleted_at IS NULL`, tenantID, task.ID, reviewerID); err != nil {
		return SubmitResult{}, err
	}
	if task.GradeRound == "single" {
		if _, err = tx.ExecContext(ctx, `UPDATE scoring_run sr SET review_count=GREATEST(0,sr.review_count-1),human_confirmed_count=sr.human_confirmed_count+1,
status=CASE WHEN sr.queued_count=0 AND GREATEST(0,sr.review_count-1)=0 AND sr.failed_count=0 THEN 'completed' WHEN sr.queued_count=0 THEN 'needs_review' ELSE sr.status END,
completed_at=CASE WHEN sr.queued_count=0 AND GREATEST(0,sr.review_count-1)=0 AND sr.failed_count=0 THEN now() ELSE sr.completed_at END,updated_at=now()
FROM review_task rt WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid AND rt.scoring_run_id=sr.id AND sr.tenant_id=rt.tenant_id`, tenantID, task.ID); err != nil {
			return SubmitResult{}, err
		}
	}
	session, finalGrade, arbitrationTask, err := s.resolveDoubleMarkAfterGradeTx(ctx, tx, tenantID, reviewerID, task)
	if err != nil {
		return SubmitResult{}, err
	}
	if refreshed, refreshErr := s.getTaskTx(ctx, tx, tenantID, id); refreshErr == nil {
		task = refreshed
	}
	if err := tx.Commit(); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{
		Task:              task,
		Grade:             grade,
		QuestionGradeID:   questionGradeID,
		DoubleMarkSession: session,
		FinalGrade:        finalGrade,
		ArbitrationTask:   arbitrationTask,
	}, nil
}

func (s *PostgresStore) ReturnTask(ctx context.Context, tenantID string, id string, _ string, input ReturnTaskInput) (ReviewTask, error) {
	input.Reason = stringsTrim(input.Reason)
	if input.Reason == "" {
		return ReviewTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewTask{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanTask(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return ReviewTask{}, err
	}
	if !canReturnTask(task.Status) {
		return ReviewTask{}, ErrInvalidTransition
	}
	task, err = scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET status = 'returned', return_reason = $3, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, id, input.Reason))
	if err != nil {
		return ReviewTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReviewTask{}, err
	}
	return task, nil
}

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
ORDER BY created_at DESC
`, tenantID, filter.Status, filter.AnswerSegmentID)
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
	session, err := s.getSessionTx(ctx, tx, tenantID, input.DoubleMarkSessionID, true)
	if err != nil {
		return ArbitrationTask{}, err
	}
	if session.ArbitrationTaskID != "" {
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
	rows, err := s.db.QueryContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND deleted_at IS NULL
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR COALESCE(assigned_to::text, '') = $3)
ORDER BY created_at DESC
`, tenantID, filter.Status, filter.AssignedTo)
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
	if input.AssignedTo == "" {
		return ArbitrationTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArbitrationTask{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanArbitration(tx.QueryRowContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return ArbitrationTask{}, err
	}
	if task.Status == "submitted" {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	if !arbitratorAllowed(task, input.AssignedTo) {
		return ArbitrationTask{}, ErrForbidden
	}
	task, err = scanArbitration(tx.QueryRowContext(ctx, `
UPDATE arbitration_task
SET assigned_to = $3::uuid, status = 'assigned', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
RETURNING id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, created_by::text, created_at, updated_at
`, tenantID, id, input.AssignedTo))
	if err != nil {
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
	if input.Reason == "" {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanArbitration(tx.QueryRowContext(ctx, arbitrationSelect()+`
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id))
	if err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	if task.Status == "submitted" {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidTransition
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
SET assigned_to = $3::uuid, status = 'submitted', final_score = $4, reason = $5, student_feedback = $6, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
RETURNING id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, created_by::text, created_at, updated_at
`, tenantID, id, arbitratorID, input.FinalScore, input.Reason, input.StudentFeedback))
	if err != nil {
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
	if err := tx.Commit(); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	return task, finalGrade, nil
}

func (s *PostgresStore) loadContext(ctx context.Context, tenantID string, segmentID string) (Context, error) {
	return s.loadContextScanner(ctx, s.db, tenantID, segmentID)
}

func (s *PostgresStore) loadContextTx(ctx context.Context, tx *sql.Tx, tenantID string, segmentID string) (Context, error) {
	return s.loadContextScanner(ctx, tx, tenantID, segmentID)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *PostgresStore) loadContextScanner(ctx context.Context, q queryer, tenantID string, segmentID string) (Context, error) {
	row := q.QueryRowContext(ctx, `
SELECT
  sub.exam_id::text,
  sub.id::text,
  COALESCE(NULLIF(sub.candidate_no, ''), seg.id::text),
  seg.id::text,
  q.id::text, q.tenant_id::text, q.exam_id::text, COALESCE(q.exam_paper_id::text, ''),
  q.question_no, q.question_type, q.score::float8, COALESCE(q.stem, ''),
  q.knowledge_points, q.answer_area, q.sort_order, q.status,
  COALESCE(qr.id::text, ''), COALESCE(qr.question_id::text, ''), COALESCE(rv.version, ''),
  COALESCE(qr.status, ''), COALESCE(qr.max_score::float8, 0), COALESCE(qr.points, '[]'::jsonb),
  COALESCE(qr.deductions, '[]'::jsonb), COALESCE(qr.examples, '[]'::jsonb),
  COALESCE(ans.answer_text, ''), COALESCE(ans.source, ''),
  COALESCE(ag.ai_suggestion, 'null'::jsonb)
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id = seg.tenant_id AND sub.id = seg.submission_id
JOIN question q ON q.tenant_id = seg.tenant_id AND q.id = seg.question_id
LEFT JOIN LATERAL (
  SELECT id, question_id, status, max_score, points, deductions, examples, rubric_version_id
  FROM question_rubric
  WHERE tenant_id = q.tenant_id AND question_id = q.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) qr ON true
LEFT JOIN rubric_version rv ON rv.tenant_id = q.tenant_id AND rv.id = qr.rubric_version_id
LEFT JOIN LATERAL (
  SELECT answer_text, source
  FROM answer_segment_answer
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT jsonb_build_object(
    'id', id::text,
    'ai_grade_id', id::text,
    'tenant_id', tenant_id::text,
    'answer_segment_id', answer_segment_id::text,
    'question_id', question_id::text,
    'question_no', question_no,
    'question_type', question_type,
    'answer_version', answer_version,
    'grader_type', grader_type,
    'rule_version', rule_version,
    'model_version', model_version,
    'prompt_version', prompt_version,
    'rubric_version', rubric_version,
    'delivery_mode', delivery_mode,
    'suggested_score', suggested_score,
    'max_score', max_score,
    'confidence', confidence,
    'matched_points', matched_points,
    'missing_points', missing_points,
    'evidence', evidence,
    'risk_flags', risk_flags,
    'needs_human_review', needs_human_review,
    'auto_pass', auto_pass,
    'mock', mock,
    'status', status,
    'failure_reason', COALESCE(failure_reason, ''),
    'student_feedback', student_feedback,
    'teacher_note', teacher_note,
    'created_by', created_by::text,
    'created_at', created_at
  ) AS ai_suggestion
  FROM ai_grade
  WHERE tenant_id = seg.tenant_id AND answer_segment_id = seg.id AND deleted_at IS NULL
  ORDER BY created_at DESC
  LIMIT 1
) ag ON true
WHERE seg.tenant_id = $1 AND seg.id::text = $2 AND seg.deleted_at IS NULL AND sub.deleted_at IS NULL
`, tenantID, segmentID)
	var out Context
	var question paper.Question
	var kpRaw, areaRaw []byte
	var rubric paper.Rubric
	var pointsRaw, deductionsRaw, examplesRaw []byte
	var answerSource string
	var aiSuggestionRaw []byte
	if err := row.Scan(
		&out.ExamID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.AnswerSegmentID,
		&question.ID,
		&question.TenantID,
		&question.ExamID,
		&question.ExamPaperID,
		&question.QuestionNo,
		&question.QuestionType,
		&question.Score,
		&question.Stem,
		&kpRaw,
		&areaRaw,
		&question.SortOrder,
		&question.Status,
		&rubric.ID,
		&rubric.QuestionID,
		&rubric.Version,
		&rubric.Status,
		&rubric.MaxScore,
		&pointsRaw,
		&deductionsRaw,
		&examplesRaw,
		&out.RawAnswer,
		&answerSource,
		&aiSuggestionRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Context{}, ErrNotFound
		}
		return Context{}, err
	}
	if err := decodeJSONB(kpRaw, &question.KnowledgePoints, "question.knowledge_points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(areaRaw, &question.AnswerArea, "question.answer_area"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(pointsRaw, &rubric.Points, "rubric.points"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(deductionsRaw, &rubric.Deductions, "rubric.deductions"); err != nil {
		return Context{}, err
	}
	if err := decodeJSONB(examplesRaw, &rubric.Examples, "rubric.examples"); err != nil {
		return Context{}, err
	}
	if answerSource == "ocr_text" {
		out.OCRText = out.RawAnswer
	}
	if err := decodeJSONB(aiSuggestionRaw, &out.AISuggestion, "ai_grade.ai_suggestion"); err != nil {
		return Context{}, err
	}
	if automationResult, loadErr := loadAutomationResult(ctx, q, tenantID, segmentID); loadErr != nil {
		return Context{}, loadErr
	} else {
		out.AutomationResult = automationResult
	}
	out.Question = question
	out.Rubric = rubric
	return out, nil
}

func loadAutomationResult(ctx context.Context, q queryer, tenantID, segmentID string) (*AutomationResult, error) {
	row := q.QueryRowContext(ctx, `
SELECT
  COALESCE(NULLIF(ac.source,''),ans.source,''),
  COALESCE(NULLIF(ac.display_text,''),ans.answer_text,''),
  COALESCE(ac.confidence,ans.confidence)::float8,
  COALESCE(ac.decision,''),
  COALESCE(ak.id::text,''),COALESCE(ak.standard_answer,'null'::jsonb),
  COALESCE(sr.rule_type,''),g.score::float8,g.max_score::float8,COALESCE(g.source,'')
FROM (SELECT 1) seed
LEFT JOIN LATERAL (
  SELECT source,display_text,confidence,decision
  FROM answer_candidate
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ac ON true
LEFT JOIN LATERAL (
  SELECT source,answer_text,confidence
  FROM answer_segment_answer
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ans ON true
LEFT JOIN LATERAL (
  SELECT id,standard_answer
  FROM question_answer_key
  WHERE tenant_id=$1::uuid AND question_id=(
    SELECT question_id FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
  ) AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) ak ON true
LEFT JOIN LATERAL (
  SELECT rule_type
  FROM scoring_rule
  WHERE tenant_id=$1::uuid AND question_id=(
    SELECT question_id FROM answer_segment WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL
  ) AND status='published' AND deleted_at IS NULL
  ORDER BY version DESC LIMIT 1
) sr ON true
LEFT JOIN LATERAL (
  SELECT score,max_score,source
  FROM question_grade
  WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL
  ORDER BY created_at DESC LIMIT 1
) g ON true`, tenantID, segmentID)

	var out AutomationResult
	var confidence, score, maxScore sql.NullFloat64
	var answerKeyID string
	var standardAnswerRaw []byte
	if err := row.Scan(
		&out.Source, &out.RecognizedAnswer, &confidence, &out.Decision,
		&answerKeyID, &standardAnswerRaw, &out.RuleType, &score, &maxScore, &out.GradeSource,
	); err != nil {
		return nil, err
	}
	if confidence.Valid {
		value := confidence.Float64
		out.Confidence = &value
	}
	if score.Valid {
		value := score.Float64
		out.Score = &value
	}
	if maxScore.Valid {
		value := maxScore.Float64
		out.MaxScore = &value
	}
	if answerKeyID != "" {
		if err := decodeJSONB(standardAnswerRaw, &out.StandardAnswer, "question_answer_key.standard_answer"); err != nil {
			return nil, err
		}
	}
	if out.Source == "" && out.RecognizedAnswer == "" && out.RuleType == "" && answerKeyID == "" {
		return nil, nil
	}
	return &out, nil
}

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
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
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
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, created_by::text, created_at, updated_at
`, tenantID, taskContext.ExamID, taskContext.Question.ID, taskContext.Question.QuestionNo,
		input.AnswerSegmentID, taskContext.SubmissionID, taskContext.AnonymousCode,
		input.Source, status, input.Priority, input.AssignedTo, input.GradeRound, input.DueAt, actorID))
}

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

func (s *PostgresStore) getSessionTx(ctx context.Context, tx *sql.Tx, tenantID string, id string, forUpdate bool) (DoubleMarkSession, error) {
	query := `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_review_task_id::text, second_review_task_id::text, first_reviewer_id::text, second_reviewer_id::text,
  threshold::float8, resolution_strategy, status, score_difference::float8,
  COALESCE(final_grade_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  created_by::text, created_at, updated_at
FROM double_mark_session
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`
	if forUpdate {
		query += "FOR UPDATE"
	}
	return scanSession(tx.QueryRowContext(ctx, query, tenantID, id))
}

func (s *PostgresStore) latestGradeTx(ctx context.Context, tx *sql.Tx, tenantID string, taskID string) (HumanGrade, bool, error) {
	grade, err := scanGrade(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, review_task_id::text, answer_segment_id::text, reviewer_id::text,
  score::float8, max_score::float8, rubric_selections, comments, private_note, student_feedback,
  reason, grade_round, COALESCE(ai_grade_id::text, ''), created_at
FROM human_grade
WHERE tenant_id = $1 AND review_task_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT 1
`, tenantID, taskID))
	if errors.Is(err, ErrNotFound) {
		return HumanGrade{}, false, nil
	}
	return grade, err == nil, err
}

func (s *PostgresStore) resolveDoubleMarkAfterGradeTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, task ReviewTask) (*DoubleMarkSession, *FinalGrade, *ArbitrationTask, error) {
	session, err := scanSession(tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_review_task_id::text, second_review_task_id::text, first_reviewer_id::text, second_reviewer_id::text,
  threshold::float8, resolution_strategy, status, score_difference::float8,
  COALESCE(final_grade_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  created_by::text, created_at, updated_at
FROM double_mark_session
WHERE tenant_id = $1
  AND (first_review_task_id::text = $2 OR second_review_task_id::text = $2)
  AND deleted_at IS NULL
FOR UPDATE
`, tenantID, task.ID))
	if errors.Is(err, ErrNotFound) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	first, firstOK, err := s.latestGradeTx(ctx, tx, tenantID, session.FirstReviewTaskID)
	if err != nil {
		return nil, nil, nil, err
	}
	second, secondOK, err := s.latestGradeTx(ctx, tx, tenantID, session.SecondReviewTaskID)
	if err != nil {
		return nil, nil, nil, err
	}
	if !firstOK || !secondOK {
		status := "pending"
		if firstOK {
			status = "first_submitted"
		}
		if secondOK {
			status = "second_submitted"
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE double_mark_session
SET status = $3, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, session.ID, status); err != nil {
			return nil, nil, nil, err
		}
		session.Status = status
		return &session, nil, nil, nil
	}
	diff := math.Abs(first.Score - second.Score)
	session.ScoreDifference = floatPtr(diff)
	if diff <= session.Threshold {
		score, err := resolveScore(session.ResolutionStrategy, first.Score, second.Score)
		if err != nil {
			return nil, nil, nil, err
		}
		finalGrade, err := s.insertFinalGradeTx(ctx, tx, tenantID, actorID, session, score, "double_mark_auto", "", session.ResolutionStrategy)
		if err != nil {
			return nil, nil, nil, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE double_mark_session
SET status = 'auto_finalized', score_difference = $3, final_grade_id = $4::uuid, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, session.ID, diff, finalGrade.ID); err != nil {
			return nil, nil, nil, err
		}
		if err := s.completeSessionReviewTasksTx(ctx, tx, tenantID, session); err != nil {
			return nil, nil, nil, err
		}
		session.Status = "auto_finalized"
		session.FinalGradeID = finalGrade.ID
		return &session, &finalGrade, nil, nil
	}
	policy, _ := s.effectivePolicyTx(ctx, tx, tenantID, session.ExamID, session.QuestionID)
	reason := fmt.Sprintf("score difference %.2f exceeds threshold %.2f", diff, session.Threshold)
	arbitrationTask, err := s.createArbitrationTaskTx(ctx, tx, tenantID, actorID, session, first, second, reason, "", policy.AllowSameArbitrator)
	if err != nil {
		return nil, nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE double_mark_session
SET status = 'needs_arbitration', score_difference = $3, arbitration_task_id = $4::uuid, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, session.ID, diff, arbitrationTask.ID); err != nil {
		return nil, nil, nil, err
	}
	session.Status = "needs_arbitration"
	session.ArbitrationTaskID = arbitrationTask.ID
	return &session, nil, &arbitrationTask, nil
}

func (s *PostgresStore) createArbitrationTaskTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, session DoubleMarkSession, first HumanGrade, second HumanGrade, reason string, assignedTo string, allowSame bool) (ArbitrationTask, error) {
	if assignedTo != "" {
		candidate := ArbitrationTask{FirstReviewerID: session.FirstReviewerID, SecondReviewerID: session.SecondReviewerID, AllowSameArbitrator: allowSame}
		if !arbitratorAllowed(candidate, assignedTo) {
			return ArbitrationTask{}, ErrForbidden
		}
	}
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, session.AnswerSegmentID)
	if err != nil {
		return ArbitrationTask{}, err
	}
	payload, _ := json.Marshal(contextForArbitration(taskContext))
	status := "pending"
	if assignedTo != "" {
		status = "assigned"
	}
	diff := math.Abs(first.Score - second.Score)
	if stringsTrim(reason) == "" {
		reason = fmt.Sprintf("score difference %.2f exceeds threshold %.2f", diff, session.Threshold)
	}
	return scanArbitration(tx.QueryRowContext(ctx, `
INSERT INTO arbitration_task (
  tenant_id, double_mark_session_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  first_reviewer_id, second_reviewer_id, first_score, second_score, score_difference,
  difference_reason, status, assigned_to, allow_same_arbitrator, context, created_by
)
VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9::uuid, $10::uuid, $11, $12, $13, $14, $15, NULLIF($16, '')::uuid, $17, $18, $19)
RETURNING id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, created_by::text, created_at, updated_at
`, tenantID, session.ID, session.ExamID, session.QuestionID, session.QuestionNo,
		session.AnswerSegmentID, session.SubmissionID, session.AnonymousCode,
		session.FirstReviewerID, session.SecondReviewerID, first.Score, second.Score, diff,
		reason, status, assignedTo, allowSame, payload, actorID))
}

func (s *PostgresStore) insertFinalGradeTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, session DoubleMarkSession, score float64, source string, arbitrationTaskID string, strategy string) (FinalGrade, error) {
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, session.AnswerSegmentID)
	if err != nil {
		return FinalGrade{}, err
	}
	return scanFinalGrade(tx.QueryRowContext(ctx, `
INSERT INTO final_grade (
  tenant_id, exam_id, question_id, question_no, answer_segment_id, submission_id, anonymous_code,
  score, max_score, source, double_mark_session_id, arbitration_task_id, resolution_strategy, locked, created_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::uuid, NULLIF($12, '')::uuid, $13, false, $14)
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, score::float8, max_score::float8,
  source, COALESCE(double_mark_session_id::text, ''), COALESCE(arbitration_task_id::text, ''),
  COALESCE(resolution_strategy, ''), locked, created_by::text, created_at, updated_at
`, tenantID, session.ExamID, session.QuestionID, session.QuestionNo,
		session.AnswerSegmentID, session.SubmissionID, session.AnonymousCode,
		score, taskContext.Question.Score, source, session.ID, arbitrationTaskID, strategy, actorID))
}

func (s *PostgresStore) completeSessionReviewTasksTx(ctx context.Context, tx *sql.Tx, tenantID string, session DoubleMarkSession) error {
	_, err := tx.ExecContext(ctx, `
UPDATE review_task
SET status = 'completed', updated_at = now()
WHERE tenant_id = $1 AND id::text IN ($2, $3)
`, tenantID, session.FirstReviewTaskID, session.SecondReviewTaskID)
	return err
}

type policyScanner interface {
	Scan(dest ...any) error
}

func scanPolicy(row policyScanner) (DoubleMarkPolicy, error) {
	var out DoubleMarkPolicy
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.Enabled,
		&out.Threshold,
		&out.ResolutionStrategy,
		&out.AllowSameArbitrator,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DoubleMarkPolicy{}, ErrNotFound
		}
		return DoubleMarkPolicy{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(row sessionScanner) (DoubleMarkSession, error) {
	var out DoubleMarkSession
	var diff sql.NullFloat64
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.FirstReviewTaskID,
		&out.SecondReviewTaskID,
		&out.FirstReviewerID,
		&out.SecondReviewerID,
		&out.Threshold,
		&out.ResolutionStrategy,
		&out.Status,
		&diff,
		&out.FinalGradeID,
		&out.ArbitrationTaskID,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DoubleMarkSession{}, ErrNotFound
		}
		return DoubleMarkSession{}, err
	}
	if diff.Valid {
		out.ScoreDifference = floatPtr(diff.Float64)
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func arbitrationSelect() string {
	return `
SELECT id::text, tenant_id::text, double_mark_session_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code,
  first_reviewer_id::text, second_reviewer_id::text, first_score::float8, second_score::float8,
  score_difference::float8, difference_reason, status, COALESCE(assigned_to::text, ''),
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, created_by::text, created_at, updated_at
FROM arbitration_task
`
}

type arbitrationScanner interface {
	Scan(dest ...any) error
}

func scanArbitration(row arbitrationScanner) (ArbitrationTask, error) {
	var out ArbitrationTask
	var finalScore sql.NullFloat64
	var contextRaw []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.DoubleMarkSessionID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.FirstReviewerID,
		&out.SecondReviewerID,
		&out.FirstScore,
		&out.SecondScore,
		&out.ScoreDifference,
		&out.DifferenceReason,
		&out.Status,
		&out.AssignedTo,
		&finalScore,
		&out.Reason,
		&out.StudentFeedback,
		&out.AllowSameArbitrator,
		&contextRaw,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ArbitrationTask{}, ErrNotFound
		}
		return ArbitrationTask{}, err
	}
	if finalScore.Valid {
		out.FinalScore = floatPtr(finalScore.Float64)
	}
	if err := decodeJSONB(contextRaw, &out.Context, "arbitration_task.context"); err != nil {
		return ArbitrationTask{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

type finalGradeScanner interface {
	Scan(dest ...any) error
}

func scanFinalGrade(row finalGradeScanner) (FinalGrade, error) {
	var out FinalGrade
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.ExamID,
		&out.QuestionID,
		&out.QuestionNo,
		&out.AnswerSegmentID,
		&out.SubmissionID,
		&out.AnonymousCode,
		&out.Score,
		&out.MaxScore,
		&out.Source,
		&out.DoubleMarkSessionID,
		&out.ArbitrationTaskID,
		&out.ResolutionStrategy,
		&out.Locked,
		&out.CreatedBy,
		&out.CreatedAt,
		&out.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return FinalGrade{}, ErrNotFound
		}
		return FinalGrade{}, err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

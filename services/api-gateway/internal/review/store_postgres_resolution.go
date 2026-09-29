package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

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
		// 先到的一份只更新进度；另一份未提交前，不能生成终分或仲裁任务。
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
	// 阈值单位是分，等于阈值也自动合分；使用会话创建时保存的规则。
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
	// 是否允许原阅卷人参与仲裁取创建仲裁时的策略，并保存到仲裁任务中。
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
  final_score::float8, reason, student_feedback, allow_same_arbitrator, context, revision, created_by::text, created_at, updated_at
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
SET status = 'completed', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text IN ($2, $3)
`, tenantID, session.FirstReviewTaskID, session.SecondReviewTaskID)
	return err
}

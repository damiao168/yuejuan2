package review

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

const reviewTaskColumns = `id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, revision, created_by::text, created_at, updated_at`

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
RETURNING `+reviewTaskColumns+`
`, tenantID, taskContext.ExamID, taskContext.Question.ID, taskContext.Question.QuestionNo, input.AnswerSegmentID,
		taskContext.SubmissionID, taskContext.AnonymousCode, input.Source, status, input.Priority,
		input.AssignedTo, input.GradeRound, input.DueAt, actorID)
	return scanTask(row)
}

func (s *PostgresStore) ListTasks(ctx context.Context, tenantID string, filter ListFilter) ([]ReviewTask, error) {
	scopeMode := filter.ScopeMode
	if scopeMode == "" {
		scopeMode = "tenant" // backwards-compatible store callers are tenant-scoped
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+reviewTaskColumns+`
FROM review_task
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR assigned_to::text = $3)
  AND ($4 = '' OR exam_id::text = $4)
  AND ($7 = '' OR priority < $5 OR (priority = $5 AND (created_at > $6 OR (created_at = $6 AND id::text > $7))))
  AND (
    $9 IN ('platform', 'tenant')
    OR ($9 = 'school' AND EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id = review_task.tenant_id AND e.id = review_task.exam_id AND e.school_id::text = ANY(string_to_array(NULLIF($10, ''), ','))))
    OR ($9 = 'class' AND exam_id::text = ANY(string_to_array(NULLIF($11, ''), ',')))
    OR ($9 = 'assigned' AND (assigned_to::text = $12 OR id::text = ANY(string_to_array(NULLIF($13, ''), ','))))
  )
ORDER BY priority DESC, created_at ASC, id::text ASC
LIMIT NULLIF($8, 0)
`, tenantID, filter.Status, filter.AssignedTo, filter.ExamID,
		filter.CursorPriority, filter.CursorCreatedAt, filter.CursorID, filter.Limit,
		scopeMode, strings.Join(filter.ScopeSchoolIDs, ","), strings.Join(filter.ScopeExamIDs, ","),
		filter.ScopeActorID, strings.Join(filter.ScopeTaskIDs, ","))
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

func (s *PostgresStore) AggregateTasks(ctx context.Context, tenantID string, filter ListFilter) (TaskAggregate, error) {
	scopeMode := filter.ScopeMode
	if scopeMode == "" {
		scopeMode = "tenant"
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT status, COALESCE(assigned_to::text, ''), count(*)::int
FROM review_task
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND ($2 = '' OR status = $2)
  AND ($3 = '' OR assigned_to::text = $3)
  AND ($4 = '' OR exam_id::text = $4)
  AND (
    $5 IN ('platform', 'tenant')
    OR ($5 = 'school' AND EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id = review_task.tenant_id AND e.id = review_task.exam_id AND e.school_id::text = ANY(string_to_array(NULLIF($6, ''), ','))))
    OR ($5 = 'class' AND exam_id::text = ANY(string_to_array(NULLIF($7, ''), ',')))
    OR ($5 = 'assigned' AND (assigned_to::text = $8 OR id::text = ANY(string_to_array(NULLIF($9, ''), ','))))
  )
GROUP BY status, assigned_to
`, tenantID, filter.Status, filter.AssignedTo, filter.ExamID,
		scopeMode, strings.Join(filter.ScopeSchoolIDs, ","), strings.Join(filter.ScopeExamIDs, ","),
		filter.ScopeActorID, strings.Join(filter.ScopeTaskIDs, ","))
	if err != nil {
		return TaskAggregate{}, err
	}
	defer rows.Close()

	aggregate := TaskAggregate{StatusCounts: map[string]int{}, Reviewers: []ReviewerTaskAggregate{}}
	reviewers := map[string]*ReviewerTaskAggregate{}
	for rows.Next() {
		var status, reviewerID string
		var count int
		if err := rows.Scan(&status, &reviewerID, &count); err != nil {
			return TaskAggregate{}, err
		}
		aggregate.TotalCount += count
		aggregate.StatusCounts[status] += count
		completed := status == "submitted" || status == "completed"
		if completed {
			aggregate.CompletedCount += count
		} else {
			aggregate.RemainingCount += count
		}
		if reviewerID == "" {
			continue
		}
		reviewer := reviewers[reviewerID]
		if reviewer == nil {
			reviewer = &ReviewerTaskAggregate{ReviewerID: reviewerID}
			reviewers[reviewerID] = reviewer
		}
		reviewer.TotalCount += count
		if completed {
			reviewer.CompletedCount += count
		} else {
			reviewer.RemainingCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return TaskAggregate{}, err
	}
	ids := make([]string, 0, len(reviewers))
	for id := range reviewers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		aggregate.Reviewers = append(aggregate.Reviewers, *reviewers[id])
	}
	return aggregate, nil
}

func (s *PostgresStore) HasActiveAssignment(ctx context.Context, tenantID string, reviewerID string, answerSegmentID string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM review_task
  WHERE tenant_id = $1::uuid
    AND assigned_to = $2::uuid
    AND answer_segment_id = $3::uuid
    AND deleted_at IS NULL
    AND status NOT IN ('completed', 'cancelled')
)
`, tenantID, reviewerID, answerSegmentID).Scan(&exists)
	return exists, err
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID string, id string) (ReviewTask, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+reviewTaskColumns+`
FROM review_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	return scanTask(row)
}

func (s *PostgresStore) AssignTask(ctx context.Context, tenantID string, id string, _ string, input AssignTaskInput) (ReviewTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE review_task
SET assigned_to = $3::uuid, status = 'assigned', return_reason = '', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed', 'cancelled')
  AND (source <> 'double_mark_required' OR assigned_to=$3::uuid)
  AND (scoring_run_id IS NULL OR EXISTS (
    SELECT 1 FROM scoring_run sr WHERE sr.tenant_id=review_task.tenant_id AND sr.id=review_task.scoring_run_id
      AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
  ))
RETURNING `+reviewTaskColumns+`
`, tenantID, id, input.AssignedTo, input.ExpectedRevision)
	task, err := scanTask(row)
	if errors.Is(err, ErrNotFound) {
		if current, getErr := s.GetTask(ctx, tenantID, id); getErr == nil {
			if current.Revision != input.ExpectedRevision {
				return ReviewTask{}, ErrRevisionConflict
			}
			return ReviewTask{}, ErrInvalidTransition
		}
	}
	return task, err
}

func (s *PostgresStore) BatchAssignTasks(ctx context.Context, tenantID string, _ string, input BatchAssignInput) ([]ReviewTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" || len(input.TaskIDs) == 0 || len(input.ExpectedRevisions) != len(input.TaskIDs) {
		return nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// 批量分配是一次事务；任一任务不存在、版本过期或状态不允许，前面的分配也不生效。
	out := make([]ReviewTask, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		if stringsTrim(id) == "" {
			return nil, ErrInvalidInput
		}
		expected := input.ExpectedRevisions[id]
		if expected <= 0 {
			return nil, ErrRevisionConflict
		}
		task, err := scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET assigned_to = $3::uuid, status = 'assigned', return_reason = '', revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL AND status NOT IN ('submitted', 'completed', 'cancelled')
  AND (source <> 'double_mark_required' OR assigned_to=$3::uuid)
  AND (scoring_run_id IS NULL OR EXISTS (
    SELECT 1 FROM scoring_run sr WHERE sr.tenant_id=review_task.tenant_id AND sr.id=review_task.scoring_run_id
      AND sr.status IN ('queued','processing','needs_review','failed') AND sr.deleted_at IS NULL
  ))
RETURNING `+reviewTaskColumns+`
`, tenantID, id, input.AssignedTo, expected))
		if errors.Is(err, ErrNotFound) {
			if current, getErr := s.GetTask(ctx, tenantID, id); getErr == nil {
				if current.Revision != expected {
					return nil, ErrRevisionConflict
				}
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
	var replay SubmitResult
	// 已成功的命令先返回原回执；任务后来已提交或删除，也不应让客户端把成功误判为失败。
	if found, err := commandreceipt.Load(ctx, tx, tenantID, reviewerID, "review.submit", id, input, &replay); err != nil || found {
		return replay, err
	}
	// 先锁父评分运行再锁任务，与取消流程保持同一锁序；已取消的运行不能再写入人工成绩或当前题目成绩。
	var runID sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT scoring_run_id::text FROM review_task WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL`, tenantID, id).Scan(&runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SubmitResult{}, ErrNotFound
		}
		return SubmitResult{}, err
	}
	if runID.Valid {
		var runStatus string
		if err := tx.QueryRowContext(ctx, `SELECT status FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, runID.String).Scan(&runStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return SubmitResult{}, ErrInvalidTransition
			}
			return SubmitResult{}, err
		}
		if runStatus != "queued" && runStatus != "processing" && runStatus != "needs_review" && runStatus != "failed" {
			return SubmitResult{}, ErrInvalidTransition
		}
	}
	task, err := scanTask(tx.QueryRowContext(ctx, `
	SELECT `+reviewTaskColumns+`
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
	if input.ExpectedRevision <= 0 || task.Revision != input.ExpectedRevision {
		return SubmitResult{}, ErrRevisionConflict
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
  tenant_id, review_task_id, answer_segment_id, exam_question_snapshot_id, reviewer_id, score, max_score,
  rubric_selections, comments, private_note, student_feedback, reason, grade_round, ai_grade_id
)
VALUES ($1, $2, $3, (SELECT eqs.id FROM answer_segment seg JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id JOIN exam_question_snapshot eqs ON eqs.tenant_id=q.tenant_id AND eqs.exam_id=q.exam_id AND eqs.question_id=q.id WHERE seg.tenant_id=$1::uuid AND seg.id=$3::uuid), $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, '')::uuid)
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
	// 单评直接替换当前题目成绩；双评先保存各自的人工评分，等合分或仲裁后再产生当前成绩。
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
INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,review_task_id,source,status,score,max_score,evidence,version,supersedes_id,is_current,confirmed_by)
SELECT rt.tenant_id,rt.exam_id,rt.submission_id,rt.question_id,rt.answer_segment_id,rt.scoring_run_id,eqs.id,rt.id,'human','confirmed',$3,$4,$5::jsonb,
  (SELECT COALESCE(MAX(version),0)+1 FROM question_grade WHERE tenant_id=$1::uuid AND answer_segment_id=rt.answer_segment_id),NULLIF($6,'')::uuid,true,$7::uuid
FROM review_task rt JOIN exam_question_snapshot eqs ON eqs.tenant_id=rt.tenant_id AND eqs.exam_id=rt.exam_id AND eqs.question_id=rt.question_id WHERE rt.tenant_id=$1::uuid AND rt.id=$2::uuid
RETURNING id::text`, tenantID, task.ID, input.Score, taskContext.Question.Score, evidence, priorID, reviewerID).Scan(&questionGradeID)
		if err != nil {
			return SubmitResult{}, err
		}
	}
	task, err = scanTask(tx.QueryRowContext(ctx, `
UPDATE review_task
SET status = 'submitted', current_grade_id = NULLIF($3,'')::uuid,
  claim_expires_at = NULL, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4
RETURNING `+reviewTaskColumns+`
`, tenantID, id, questionGradeID, input.ExpectedRevision))
	if err != nil {
		return SubmitResult{}, err
	}
	// 提交成绩与清除本人草稿处于同一事务，失败回滚后草稿仍可继续使用。
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
	if runID.Valid && (task.GradeRound == "first_mark" || task.GradeRound == "second_mark") {
		if finalGrade != nil {
			questionGradeID, err = s.recordScoringDoubleMarkGradeTx(ctx, tx, tenantID, runID.String, task.ID, reviewerID, finalGrade)
			if err != nil {
				return SubmitResult{}, err
			}
		}
		if err := s.syncScoringDoubleMarkRunTx(ctx, tx, tenantID, runID.String); err != nil {
			return SubmitResult{}, err
		}
	}
	if refreshed, refreshErr := s.getTaskTx(ctx, tx, tenantID, id); refreshErr == nil {
		task = refreshed
	}
	result := SubmitResult{
		Task:              task,
		Grade:             grade,
		QuestionGradeID:   questionGradeID,
		DoubleMarkSession: session,
		FinalGrade:        finalGrade,
		ArbitrationTask:   arbitrationTask,
	}

	if err := commandreceipt.Save(ctx, tx, tenantID, reviewerID, "review.submit", id, input, result); err != nil {
		return SubmitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) ReturnTask(ctx context.Context, tenantID string, id string, actorID string, input ReturnTaskInput) (ReviewTask, error) {
	input.Reason = stringsTrim(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewTask{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := scanTask(tx.QueryRowContext(ctx, `
	SELECT `+reviewTaskColumns+`
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
	if task.Revision != input.ExpectedRevision {
		return ReviewTask{}, ErrRevisionConflict
	}
	updateQuery := `
UPDATE review_task
SET status = 'returned', return_reason = $3, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND revision = $4 AND deleted_at IS NULL
	`
	args := []any{tenantID, id, input.Reason, input.ExpectedRevision}
	// 普通阅卷人的有效租约须在写入时再检查，不能只依赖 HTTP 层稍早读到的结果。
	if input.MustOwnActiveClaim {
		updateQuery += ` AND assigned_to = $5::uuid AND claim_expires_at > now()`
		args = append(args, actorID)
	}
	updateQuery += ` RETURNING ` + reviewTaskColumns
	task, err = scanTask(tx.QueryRowContext(ctx, updateQuery, args...))
	if input.MustOwnActiveClaim && errors.Is(err, ErrNotFound) {
		return ReviewTask{}, ErrForbidden
	}
	if err != nil {
		return ReviewTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReviewTask{}, err
	}
	return task, nil
}

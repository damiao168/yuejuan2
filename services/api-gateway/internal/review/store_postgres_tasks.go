package review

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
	"errors"
	"sort"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

const reviewTaskColumns = `id::text, tenant_id::text, exam_id::text, question_id::text, question_no,
  answer_segment_id::text, submission_id::text, anonymous_code, source, status, priority,
  COALESCE(assigned_to::text, ''), return_reason, grade_round, due_at, revision, created_by::text, created_at, updated_at, reason_code`

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateTask(ctx context.Context, tenantID string, actorID string, input CreateTaskInput) (ReviewTask, error) {
	input = normalizeCreateTask(input)
	if validateCreateTask(input) != nil {
		return ReviewTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewTask{}, err
	}
	defer tx.Rollback()
	taskContext, err := s.loadContextTx(ctx, tx, tenantID, input.AnswerSegmentID)
	if err != nil {
		return ReviewTask{}, err
	}
	task, err := s.insertReviewTaskTx(ctx, tx, tenantID, actorID, taskContext, input)
	if err != nil {
		return ReviewTask{}, err
	}
	return task, tx.Commit()
}

func (s *PostgresStore) ListTasks(ctx context.Context, tenantID string, filter ListFilter) ([]ReviewTask, error) {
	scopeMode := filter.ScopeMode
	if scopeMode == "" {
		scopeMode = "tenant" // 兼容旧版的存储调用方受租户作用域限制。
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT `+reviewTaskColumns+`
FROM review_task
WHERE tenant_id = $1
  AND deleted_at IS NULL
  AND ($2 = '' OR status = $2 OR ($2 = 'active' AND status IN ('pending','assigned','in_progress','returned'))
    OR ($2 = 'reviewed' AND status IN ('submitted','completed')))
  AND ($3 = '' OR assigned_to::text = $3)
  AND ($4 = '' OR exam_id::text = $4)
  AND ($14 = '' OR question_id::text = $14)
  AND (NOT $15 OR assigned_to IS NULL)
  AND ($17 = '' OR EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id=review_task.tenant_id AND e.id=review_task.exam_id AND e.subject=$17))
  AND ($16 = '' OR strpos(lower(concat_ws(' ',id::text,submission_id::text,anonymous_code,question_no,source,
    (SELECT concat_ws(' ',e.name,e.subject) FROM exam e WHERE e.tenant_id=review_task.tenant_id AND e.id=review_task.exam_id))),lower($16)) > 0)
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
		filter.ScopeActorID, strings.Join(filter.ScopeTaskIDs, ","), filter.QuestionID, filter.Unassigned, strings.TrimSpace(filter.Q), filter.Subject)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := s.hydrateTaskDisplayLabels(ctx, tenantID, out); err != nil {
		return nil, err
	}
	return out, nil
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
  AND ($2 = '' OR status = $2 OR ($2 = 'active' AND status IN ('pending','assigned','in_progress','returned'))
    OR ($2 = 'reviewed' AND status IN ('submitted','completed')))
  AND ($3 = '' OR assigned_to::text = $3)
  AND ($4 = '' OR exam_id::text = $4)
  AND ($10 = '' OR question_id::text = $10)
  AND (NOT $11 OR assigned_to IS NULL)
  AND ($13 = '' OR EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id=review_task.tenant_id AND e.id=review_task.exam_id AND e.subject=$13))
  AND ($12 = '' OR strpos(lower(concat_ws(' ',id::text,anonymous_code,question_no,source,
    (SELECT concat_ws(' ',e.name,e.subject) FROM exam e WHERE e.tenant_id=review_task.tenant_id AND e.id=review_task.exam_id))),lower($12)) > 0)
  AND (
    $5 IN ('platform', 'tenant')
    OR ($5 = 'school' AND EXISTS (SELECT 1 FROM exam e WHERE e.tenant_id = review_task.tenant_id AND e.id = review_task.exam_id AND e.school_id::text = ANY(string_to_array(NULLIF($6, ''), ','))))
    OR ($5 = 'class' AND exam_id::text = ANY(string_to_array(NULLIF($7, ''), ',')))
    OR ($5 = 'assigned' AND (assigned_to::text = $8 OR id::text = ANY(string_to_array(NULLIF($9, ''), ','))))
  )
GROUP BY status, assigned_to
`, tenantID, filter.Status, filter.AssignedTo, filter.ExamID,
		scopeMode, strings.Join(filter.ScopeSchoolIDs, ","), strings.Join(filter.ScopeExamIDs, ","),
		filter.ScopeActorID, strings.Join(filter.ScopeTaskIDs, ","), filter.QuestionID, filter.Unassigned, strings.TrimSpace(filter.Q), filter.Subject)
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
		} else if isRemainingReviewTask(status) {
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
		} else if isRemainingReviewTask(status) {
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
	if filter.IncludeStatistics && filter.ExamID != "" {
		if err := rows.Close(); err != nil {
			return TaskAggregate{}, err
		}
		statistics, err := s.reviewStatistics(ctx, tenantID, filter)
		if err != nil {
			return TaskAggregate{}, err
		}
		aggregate.Statistics = statistics
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
	task, err := scanTask(row)
	if err != nil {
		return ReviewTask{}, err
	}
	tasks := []ReviewTask{task}
	if err := s.hydrateTaskDisplayLabels(ctx, tenantID, tasks); err != nil {
		return ReviewTask{}, err
	}
	return tasks[0], nil
}

// hydrateTaskDisplayLabels 仅补充已经通过任务作用域筛选的考试名称和学科。
// 不返回学校、班级或学生身份，也不借此扩大教师可浏览的考试范围。
func (s *PostgresStore) hydrateTaskDisplayLabels(ctx context.Context, tenantID string, tasks []ReviewTask) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tasks))
	for _, task := range tasks {
		ids = append(ids, task.ExamID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,name,subject FROM exam WHERE tenant_id=$1 AND id::text=ANY(string_to_array($2,',')) AND deleted_at IS NULL`, tenantID, strings.Join(ids, ","))
	if err != nil {
		return err
	}
	defer rows.Close()
	labels := map[string][2]string{}
	for rows.Next() {
		var id, name, subject string
		if err := rows.Scan(&id, &name, &subject); err != nil {
			return err
		}
		labels[id] = [2]string{name, subject}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range tasks {
		label := labels[tasks[i].ExamID]
		tasks[i].ExamName = label[0]
		tasks[i].Subject = label[1]
	}
	return nil
}

func (s *PostgresStore) AssignTask(ctx context.Context, tenantID string, id string, _ string, input AssignTaskInput) (ReviewTask, error) {
	input.AssignedTo = stringsTrim(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ReviewTask{}, err
	}
	defer tx.Rollback()
	if err := validateTaskAssigneeTx(ctx, tx, tenantID, id, input.AssignedTo); err != nil {
		return ReviewTask{}, err
	}
	row := tx.QueryRowContext(ctx, `
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
	if err != nil {
		return ReviewTask{}, err
	}
	return task, tx.Commit()
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
		if err := validateTaskAssigneeTx(ctx, tx, tenantID, id, input.AssignedTo); err != nil {
			return nil, err
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

// 先在事务内锁住任务并取其真实考试，不能信任客户端声称的考试或之前打开过的候选列表。
// 批量分派逐项复核；任一考试学科不匹配时，整个批次回滚。
func validateTaskAssigneeTx(ctx context.Context, tx *sql.Tx, tenantID, taskID, assigneeID string) error {
	var examID string
	err := tx.QueryRowContext(ctx, `SELECT exam_id::text FROM review_task WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL FOR UPDATE`, tenantID, taskID).Scan(&examID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return validateReviewAssigneeTx(ctx, tx, tenantID, examID, assigneeID, "review:work")
}

func validateReviewAssigneeTx(ctx context.Context, tx *sql.Tx, tenantID, examID, assigneeID, permission string) error {
	err := workflowactors.ValidateReviewAssignee(ctx, tx, tenantID, examID, assigneeID, permission)
	if errors.Is(err, workflowactors.ErrAssigneeIneligible) {
		return errors.Join(ErrForbidden, err)
	}
	return err
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

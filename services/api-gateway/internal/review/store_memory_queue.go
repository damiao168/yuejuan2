package review

import (
	"context"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) CreateTask(_ context.Context, tenantID string, actorID string, input CreateTaskInput) (ReviewTask, error) {
	input = normalizeCreateTask(input)
	if err := validateCreateTask(input); err != nil {
		return ReviewTask{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, ok := s.contexts[key(tenantID, input.AnswerSegmentID)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	return cloneTask(s.createTaskLocked(tenantID, actorID, input, ctx)), nil
}

func (s *MemoryStore) ListTasks(_ context.Context, tenantID string, filter ListFilter) ([]ReviewTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ReviewTask{}
	for _, task := range s.tasks {
		if !reviewTaskMatchesFilter(task, tenantID, filter) || !memoryTaskScopeAllows(task.ID, task.ExamID, task.AssignedTo, filter) {
			continue
		}
		out = append(out, cloneTask(task))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			if out[i].CreatedAt.Equal(out[j].CreatedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Priority > out[j].Priority
	})
	return paginateReviewTasks(out, filter), nil
}

func reviewTaskMatchesFilter(task ReviewTask, tenantID string, filter ListFilter) bool {
	if task.TenantID != tenantID {
		return false
	}
	if filter.Status == "active" && !containsString([]string{"pending", "assigned", "in_progress", "returned"}, task.Status) {
		return false
	}
	// 历史包含单评提交和双评最终化；列表与聚合共用此边界，草稿不能进入已阅记录。
	if filter.Status == "reviewed" && !submittedReviewStatus(task.Status) {
		return false
	}
	if filter.Status != "" && filter.Status != "active" && filter.Status != "reviewed" && task.Status != filter.Status {
		return false
	}
	if filter.AssignedTo != "" && task.AssignedTo != filter.AssignedTo {
		return false
	}
	if filter.QuestionID != "" && task.QuestionID != filter.QuestionID {
		return false
	}
	if filter.Subject != "" && task.Subject != filter.Subject {
		return false
	}
	if filter.Unassigned && task.AssignedTo != "" {
		return false
	}
	if !reviewTaskMatchesSearch(task, filter.Q) {
		return false
	}
	return filter.ExamID == "" || task.ExamID == filter.ExamID
}

// 关键词按字面文本匹配，百分号等字符不是通配符；空关键词不缩小队列。
func reviewTaskMatchesSearch(task ReviewTask, query string) bool {
	return query == "" || strings.Contains(strings.ToLower(strings.Join([]string{task.ID, task.SubmissionID, task.AnonymousCode, task.QuestionNo, task.Source, task.ExamName, task.Subject}, " ")), strings.ToLower(strings.TrimSpace(query)))
}

// Scope 由调用方填充；显式 AssignedTo 与空班级/学校考试列表沿用原筛选边界。
func memoryTaskScopeAllows(taskID string, examID string, assignedTo string, filter ListFilter) bool {
	mode := filter.ScopeMode
	if mode == "" {
		mode = "tenant"
	}
	if mode == "none" {
		return false
	}
	if mode == "assigned" && filter.AssignedTo == "" && assignedTo != filter.ScopeActorID && !containsString(filter.ScopeTaskIDs, taskID) {
		return false
	}
	if mode == "school" {
		// 学校管理员的考试集合由本校关系派生；空集合不能回退为整个租户。
		return containsString(filter.ScopeExamIDs, examID)
	}
	if (mode == "class" || mode == "school") && len(filter.ScopeExamIDs) > 0 && !containsString(filter.ScopeExamIDs, examID) {
		return false
	}
	return true
}

// 队列先按优先级降序、时间和 ID 升序排序；游标之后再截取 Limit，不能先分页再筛选。
func paginateReviewTasks(out []ReviewTask, filter ListFilter) []ReviewTask {
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			if reviewTaskAfterCursor(out[start], filter) {
				break
			}
			start++
		}
		out = out[start:]
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out
}

func reviewTaskAfterCursor(task ReviewTask, filter ListFilter) bool {
	if task.Priority != filter.CursorPriority {
		return task.Priority < filter.CursorPriority
	}
	if !task.CreatedAt.Equal(filter.CursorCreatedAt) {
		return task.CreatedAt.After(filter.CursorCreatedAt)
	}
	return task.ID > filter.CursorID
}

func (s *MemoryStore) AggregateTasks(ctx context.Context, tenantID string, filter ListFilter) (TaskAggregate, error) {
	filter.Limit = 0
	filter.CursorID = ""
	filter.CursorPriority = 0
	filter.CursorCreatedAt = time.Time{}
	tasks, err := s.ListTasks(ctx, tenantID, filter)
	if err != nil {
		return TaskAggregate{}, err
	}
	aggregate := aggregateReviewTasks(tasks)
	if filter.IncludeStatistics && filter.ExamID != "" {
		s.mu.RLock()
		aggregate.Statistics = s.reviewStatisticsLocked(tenantID, filter)
		s.mu.RUnlock()
	}
	return aggregate, nil
}

func aggregateReviewTasks(tasks []ReviewTask) TaskAggregate {
	aggregate := TaskAggregate{StatusCounts: map[string]int{}, Reviewers: []ReviewerTaskAggregate{}}
	reviewers := map[string]*ReviewerTaskAggregate{}
	for _, task := range tasks {
		aggregate.TotalCount++
		aggregate.StatusCounts[task.Status]++
		completed := task.Status == "submitted" || task.Status == "completed"
		if completed {
			aggregate.CompletedCount++
		} else if isRemainingReviewTask(task.Status) {
			aggregate.RemainingCount++
		}
		if task.AssignedTo == "" {
			continue
		}
		reviewer := reviewers[task.AssignedTo]
		if reviewer == nil {
			reviewer = &ReviewerTaskAggregate{ReviewerID: task.AssignedTo}
			reviewers[task.AssignedTo] = reviewer
		}
		reviewer.TotalCount++
		if completed {
			reviewer.CompletedCount++
		} else if isRemainingReviewTask(task.Status) {
			reviewer.RemainingCount++
		}
	}
	ids := make([]string, 0, len(reviewers))
	for id := range reviewers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		aggregate.Reviewers = append(aggregate.Reviewers, *reviewers[id])
	}
	return aggregate
}

// 已取消任务保留在全状态历史和状态计数中，但不再占用待阅工作量。
func isRemainingReviewTask(status string) bool {
	return status == "pending" || status == "assigned" || status == "in_progress" || status == "returned"
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *MemoryStore) HasActiveAssignment(_ context.Context, tenantID string, reviewerID string, answerSegmentID string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.AssignedTo == reviewerID && task.AnswerSegmentID == answerSegmentID && task.Status != "completed" && task.Status != "cancelled" {
			return true, nil
		}
	}
	return false, nil
}

func (s *MemoryStore) GetTask(_ context.Context, tenantID string, id string) (ReviewTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	return cloneTask(task), nil
}

func (s *MemoryStore) AssignTask(_ context.Context, tenantID string, id string, _ string, input AssignTaskInput) (ReviewTask, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	if task.Status == "submitted" || task.Status == "completed" {
		return ReviewTask{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ReviewTask{}, ErrRevisionConflict
	}
	task.AssignedTo = input.AssignedTo
	task.Status = "assigned"
	task.ReturnReason = ""
	delete(s.claims, key(tenantID, id))
	task.Revision++
	task.UpdatedAt = time.Now().UTC()
	s.tasks[key(tenantID, id)] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) BatchAssignTasks(_ context.Context, tenantID string, _ string, input BatchAssignInput) ([]ReviewTask, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || len(input.TaskIDs) == 0 || len(input.ExpectedRevisions) != len(input.TaskIDs) {
		return nil, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range input.TaskIDs {
		task, ok := s.tasks[key(tenantID, id)]
		if !ok {
			return nil, ErrNotFound
		}
		if task.Status == "submitted" || task.Status == "completed" {
			return nil, ErrInvalidTransition
		}
		if input.ExpectedRevisions[id] <= 0 || task.Revision != input.ExpectedRevisions[id] {
			return nil, ErrRevisionConflict
		}
	}
	now := time.Now().UTC()
	out := make([]ReviewTask, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		task := s.tasks[key(tenantID, id)]
		task.AssignedTo = input.AssignedTo
		task.Status = "assigned"
		task.ReturnReason = ""
		delete(s.claims, key(tenantID, id))
		task.Revision++
		task.UpdatedAt = now
		s.tasks[key(tenantID, id)] = task
		out = append(out, cloneTask(task))
	}
	return out, nil
}

// Locked 辅助函数不再加锁；调用方必须持有 s.mu 写锁，才能一起更新任务、评分和会话。
func (s *MemoryStore) createTaskLocked(tenantID string, actorID string, input CreateTaskInput, ctx Context) ReviewTask {
	now := time.Now().UTC()
	status := "pending"
	if strings.TrimSpace(input.AssignedTo) != "" {
		status = "assigned"
	}
	task := ReviewTask{
		ID:              s.id("review-task"),
		TenantID:        tenantID,
		ExamID:          ctx.ExamID,
		QuestionID:      ctx.Question.ID,
		QuestionNo:      ctx.Question.QuestionNo,
		AnswerSegmentID: input.AnswerSegmentID,
		SubmissionID:    ctx.SubmissionID,
		AnonymousCode:   ctx.AnonymousCode,
		Source:          input.Source,
		Status:          status,
		Priority:        input.Priority,
		AssignedTo:      strings.TrimSpace(input.AssignedTo),
		GradeRound:      normalizeGradeRound(input.GradeRound),
		Revision:        1,
		DueAt:           cloneTime(input.DueAt),
		CreatedBy:       actorID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.tasks[key(tenantID, task.ID)] = task
	return task
}

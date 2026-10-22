package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	receipts      commandreceipt.Memory
	mu            sync.RWMutex
	next          int
	contexts      map[string]Context
	tasks         map[string]ReviewTask
	claims        map[string]memoryTaskClaim
	drafts        map[string]ReviewDraft
	grades        map[string][]HumanGrade
	policies      map[string]DoubleMarkPolicy
	sessions      map[string]DoubleMarkSession
	sessionByTask map[string]string
	arbitrations  map[string]ArbitrationTask
	finalGrades   map[string]FinalGrade
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:          1,
		contexts:      map[string]Context{},
		tasks:         map[string]ReviewTask{},
		claims:        map[string]memoryTaskClaim{},
		drafts:        map[string]ReviewDraft{},
		grades:        map[string][]HumanGrade{},
		policies:      map[string]DoubleMarkPolicy{},
		sessions:      map[string]DoubleMarkSession{},
		sessionByTask: map[string]string{},
		arbitrations:  map[string]ArbitrationTask{},
		finalGrades:   map[string]FinalGrade{},
	}
}

func (s *MemoryStore) AddContext(tenantID string, segmentID string, ctx Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx.AnswerSegmentID = segmentID
	ctx.AISuggestion = cloneMap(ctx.AISuggestion)
	s.contexts[key(tenantID, segmentID)] = ctx
}

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
	if filter.Status != "" && filter.Status != "active" && task.Status != filter.Status {
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
	return aggregateReviewTasks(tasks), nil
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

type memoryTaskClaim struct {
	OwnerID   string
	ClaimedAt time.Time
	ExpiresAt time.Time
}

func (s *MemoryStore) SubmitGrade(requestCtx context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay SubmitResult
	if found, err := s.receipts.Load(requestCtx, tenantID, reviewerID, "review.submit", id, input, &replay); err != nil || found {
		return replay, err
	}
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return SubmitResult{}, ErrNotFound
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
	ctx, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	if !ok {
		return SubmitResult{}, ErrNotFound
	}
	if err := validateSubmit(input, ctx); err != nil {
		return SubmitResult{}, err
	}
	if err := requireSubmissionQualification(requestCtx, tenantID, reviewerID, task, ctx); err != nil {
		return SubmitResult{}, err
	}
	now := time.Now().UTC()
	grade := HumanGrade{
		ID:               s.id("human-grade"),
		TenantID:         tenantID,
		ReviewTaskID:     task.ID,
		AnswerSegmentID:  task.AnswerSegmentID,
		ReviewerID:       reviewerID,
		Score:            input.Score,
		MaxScore:         ctx.Question.Score,
		RubricSelections: cloneSelections(input.RubricSelections),
		Comments:         strings.TrimSpace(input.Comments),
		PrivateNote:      strings.TrimSpace(input.PrivateNote),
		StudentFeedback:  strings.TrimSpace(input.StudentFeedback),
		Reason:           strings.TrimSpace(input.Reason),
		GradeRound:       task.GradeRound,
		AIGradeID:        aiGradeIDFromContext(ctx),
		CreatedAt:        now,
	}
	task.Status = "submitted"
	task.Revision++
	task.UpdatedAt = now
	s.tasks[key(tenantID, id)] = task
	s.grades[key(tenantID, task.ID)] = append(s.grades[key(tenantID, task.ID)], grade)
	session, finalGrade, arbitrationTask, err := s.resolveDoubleMarkAfterGradeLocked(tenantID, reviewerID, task)
	if err != nil {
		return SubmitResult{}, err
	}
	if refreshed, ok := s.tasks[key(tenantID, id)]; ok {
		task = refreshed
	}
	result := SubmitResult{
		Task:              cloneTask(task),
		Grade:             cloneGrade(grade),
		DoubleMarkSession: cloneSessionPtr(session),
		FinalGrade:        cloneFinalGradePtr(finalGrade),
		ArbitrationTask:   cloneArbitrationPtr(arbitrationTask),
	}
	if err := s.receipts.Save(requestCtx, tenantID, reviewerID, "review.submit", id, input, result); err != nil {
		return SubmitResult{}, err
	}
	return result, nil
}

func (s *MemoryStore) ReturnTask(_ context.Context, tenantID string, id string, actorID string, input ReturnTaskInput) (ReviewTask, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return ReviewTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	if input.MustOwnActiveClaim {
		claim, claimed := s.claims[key(tenantID, id)]
		if task.AssignedTo != actorID || !claimed || claim.OwnerID != actorID || !claim.ExpiresAt.After(time.Now().UTC()) {
			return ReviewTask{}, ErrForbidden
		}
	}
	if !canReturnTask(task.Status) {
		return ReviewTask{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ReviewTask{}, ErrRevisionConflict
	}
	task.Status = "returned"
	task.ReturnReason = input.Reason
	task.Revision++
	task.UpdatedAt = time.Now().UTC()
	s.tasks[key(tenantID, id)] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) SetExamDoubleMarkPolicy(_ context.Context, tenantID string, examID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error) {
	examID = strings.TrimSpace(examID)
	input = normalizePolicyInput(input)
	if examID == "" || validatePolicyInput(input) != nil {
		return DoubleMarkPolicy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return clonePolicy(s.upsertPolicyLocked(tenantID, examID, "", actorID, input)), nil
}

func (s *MemoryStore) SetQuestionDoubleMarkPolicy(_ context.Context, tenantID string, questionID string, actorID string, input SetDoubleMarkPolicyInput) (DoubleMarkPolicy, error) {
	questionID = strings.TrimSpace(questionID)
	input = normalizePolicyInput(input)
	if questionID == "" || validatePolicyInput(input) != nil {
		return DoubleMarkPolicy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	examID := ""
	for _, ctx := range s.contexts {
		if ctx.Question.ID == questionID && ctx.Question.ExamID != "" {
			examID = ctx.Question.ExamID
			break
		}
	}
	if examID == "" {
		return DoubleMarkPolicy{}, ErrNotFound
	}
	return clonePolicy(s.upsertPolicyLocked(tenantID, examID, questionID, actorID, input)), nil
}

func (s *MemoryStore) ListDoubleMarkPolicies(ctx context.Context, tenantID string, filter PolicyFilter) ([]DoubleMarkPolicy, error) {
	if err := filter.Access.Validate(tenantID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []DoubleMarkPolicy{}
	for _, policy := range s.policies {
		if policy.TenantID != tenantID {
			continue
		}
		if filter.ExamID != "" && policy.ExamID != filter.ExamID {
			continue
		}
		if filter.QuestionID != "" && policy.QuestionID != filter.QuestionID {
			continue
		}
		allowed, err := filter.Access.Allows(ctx, tenantID, "exam", policy.ExamID, policy.ExamID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		out = append(out, clonePolicy(policy))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ExamID == out[j].ExamID {
			return out[i].QuestionID < out[j].QuestionID
		}
		return out[i].ExamID < out[j].ExamID
	})
	return out, nil
}

func (s *MemoryStore) CreateDoubleMarkSession(_ context.Context, tenantID string, actorID string, input CreateDoubleMarkSessionInput) (DoubleMarkSession, error) {
	input.AnswerSegmentID = strings.TrimSpace(input.AnswerSegmentID)
	input.FirstReviewerID = strings.TrimSpace(input.FirstReviewerID)
	input.SecondReviewerID = strings.TrimSpace(input.SecondReviewerID)
	if input.AnswerSegmentID == "" || input.FirstReviewerID == "" || input.SecondReviewerID == "" || input.FirstReviewerID == input.SecondReviewerID || input.Priority < 0 {
		return DoubleMarkSession{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx, ok := s.contexts[key(tenantID, input.AnswerSegmentID)]
	if !ok {
		return DoubleMarkSession{}, ErrNotFound
	}
	policy, ok := s.effectivePolicyLocked(tenantID, ctx.ExamID, ctx.Question.ID)
	if !ok {
		return DoubleMarkSession{}, ErrNotFound
	}
	if !policy.Enabled {
		return DoubleMarkSession{}, ErrInvalidInput
	}
	now := time.Now().UTC()
	firstTask := s.createTaskLocked(tenantID, actorID, CreateTaskInput{
		AnswerSegmentID: input.AnswerSegmentID,
		Source:          "double_mark_required",
		Priority:        input.Priority,
		AssignedTo:      input.FirstReviewerID,
		GradeRound:      "first_mark",
		DueAt:           cloneTime(input.DueAt),
	}, ctx)
	secondTask := s.createTaskLocked(tenantID, actorID, CreateTaskInput{
		AnswerSegmentID: input.AnswerSegmentID,
		Source:          "double_mark_required",
		Priority:        input.Priority,
		AssignedTo:      input.SecondReviewerID,
		GradeRound:      "second_mark",
		DueAt:           cloneTime(input.DueAt),
	}, ctx)
	session := DoubleMarkSession{
		ID:                 s.id("double-mark-session"),
		TenantID:           tenantID,
		ExamID:             ctx.ExamID,
		QuestionID:         ctx.Question.ID,
		QuestionNo:         ctx.Question.QuestionNo,
		AnswerSegmentID:    input.AnswerSegmentID,
		SubmissionID:       ctx.SubmissionID,
		AnonymousCode:      ctx.AnonymousCode,
		FirstReviewTaskID:  firstTask.ID,
		SecondReviewTaskID: secondTask.ID,
		FirstReviewerID:    input.FirstReviewerID,
		SecondReviewerID:   input.SecondReviewerID,
		Threshold:          policy.Threshold,
		ResolutionStrategy: policy.ResolutionStrategy,
		Status:             "pending",
		CreatedBy:          actorID,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	s.sessions[key(tenantID, session.ID)] = session
	s.sessionByTask[key(tenantID, firstTask.ID)] = session.ID
	s.sessionByTask[key(tenantID, secondTask.ID)] = session.ID
	return cloneSession(session), nil
}

func (s *MemoryStore) ListDoubleMarkSessions(ctx context.Context, tenantID string, filter DoubleMarkSessionFilter) ([]DoubleMarkSession, error) {
	if err := filter.Access.Validate(tenantID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []DoubleMarkSession{}
	for _, session := range s.sessions {
		if session.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && session.Status != filter.Status {
			continue
		}
		if filter.AnswerSegmentID != "" && session.AnswerSegmentID != filter.AnswerSegmentID {
			continue
		}
		if filter.ExamID != "" && session.ExamID != filter.ExamID {
			continue
		}
		if filter.QuestionID != "" && session.QuestionID != filter.QuestionID {
			continue
		}
		allowed, err := filter.Access.Allows(ctx, tenantID, "exam", session.ExamID, session.ExamID)
		if err != nil {
			return nil, err
		}
		if !allowed {
			continue
		}
		out = append(out, cloneSession(session))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) GetDoubleMarkSession(_ context.Context, tenantID string, id string) (DoubleMarkSession, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[key(tenantID, id)]
	if !ok {
		return DoubleMarkSession{}, ErrNotFound
	}
	return cloneSession(session), nil
}

func (s *MemoryStore) CreateArbitrationTask(_ context.Context, tenantID string, actorID string, input CreateArbitrationTaskInput) (ArbitrationTask, error) {
	input.DoubleMarkSessionID = strings.TrimSpace(input.DoubleMarkSessionID)
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	input.DifferenceReason = strings.TrimSpace(input.DifferenceReason)
	if input.DoubleMarkSessionID == "" {
		return ArbitrationTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[key(tenantID, input.DoubleMarkSessionID)]
	if !ok {
		return ArbitrationTask{}, ErrNotFound
	}
	if session.ArbitrationTaskID != "" {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	first, firstOK := s.latestGradeLocked(tenantID, session.FirstReviewTaskID)
	second, secondOK := s.latestGradeLocked(tenantID, session.SecondReviewTaskID)
	if !firstOK || !secondOK {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	diff := math.Abs(first.Score - second.Score)
	if diff <= session.Threshold {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	policy, _ := s.effectivePolicyLocked(tenantID, session.ExamID, session.QuestionID)
	task, err := s.createArbitrationTaskLocked(tenantID, actorID, session, first, second, input, policy.AllowSameArbitrator)
	if err != nil {
		return ArbitrationTask{}, err
	}
	session.ScoreDifference = floatPtr(diff)
	session.Status = "needs_arbitration"
	session.ArbitrationTaskID = task.ID
	session.UpdatedAt = time.Now().UTC()
	s.sessions[key(tenantID, session.ID)] = session
	return cloneArbitration(task), nil
}

func (s *MemoryStore) ListArbitrationTasks(_ context.Context, tenantID string, filter ArbitrationFilter) ([]ArbitrationTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ArbitrationTask{}
	scopeFilter := ListFilter{AssignedTo: filter.AssignedTo, ScopeMode: filter.ScopeMode, ScopeActorID: filter.ScopeActorID, ScopeTaskIDs: filter.ScopeTaskIDs, ScopeExamIDs: filter.ScopeExamIDs}
	for _, task := range s.arbitrations {
		if !arbitrationTaskMatchesFilter(task, tenantID, filter) || !memoryTaskScopeAllows(task.ID, task.ExamID, task.AssignedTo, scopeFilter) {
			continue
		}
		out = append(out, cloneArbitration(task))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return paginateArbitrationTasks(out, filter), nil
}

func arbitrationTaskMatchesFilter(task ArbitrationTask, tenantID string, filter ArbitrationFilter) bool {
	if task.TenantID != tenantID {
		return false
	}
	if filter.Status == "active" && task.Status != "pending" && task.Status != "assigned" {
		return false
	}
	if filter.Status != "" && filter.Status != "active" && task.Status != filter.Status {
		return false
	}
	if filter.AssignedTo != "" && task.AssignedTo != filter.AssignedTo {
		return false
	}
	return filter.ExamID == "" || task.ExamID == filter.ExamID
}

// 仲裁队列按时间和 ID 降序排列；游标取更早记录，方向与普通阅卷队列不同。
func paginateArbitrationTasks(out []ArbitrationTask, filter ArbitrationFilter) []ArbitrationTask {
	if filter.CursorID != "" {
		start := 0
		for start < len(out) {
			item := out[start]
			if item.CreatedAt.Before(filter.CursorCreatedAt) ||
				(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
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

func (s *MemoryStore) GetArbitrationTask(_ context.Context, tenantID string, id string) (ArbitrationTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.arbitrations[key(tenantID, id)]
	if !ok {
		return ArbitrationTask{}, ErrNotFound
	}
	if task.Context.Question == nil || task.Context.Rubric == nil {
		if frozen, exists := s.contexts[key(tenantID, task.AnswerSegmentID)]; exists {
			// 旧仲裁记录没有存标准；补齐同一答题片段的冻结材料，保留当时存下的作答和建议。
			materials := contextForArbitration(frozen)
			if task.Context.Question == nil {
				task.Context.Question = materials.Question
			}
			if task.Context.Rubric == nil {
				task.Context.Rubric = materials.Rubric
			}
		}
	}
	return cloneArbitration(task), nil
}

func (s *MemoryStore) AssignArbitrationTask(_ context.Context, tenantID string, id string, _ string, input AssignArbitrationTaskInput) (ArbitrationTask, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || input.ExpectedRevision <= 0 {
		return ArbitrationTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.arbitrations[key(tenantID, id)]
	if !ok {
		return ArbitrationTask{}, ErrNotFound
	}
	if task.Status == "submitted" {
		return ArbitrationTask{}, ErrInvalidTransition
	}
	if task.Revision != input.ExpectedRevision {
		return ArbitrationTask{}, ErrRevisionConflict
	}
	if !arbitratorAllowed(task, input.AssignedTo) {
		return ArbitrationTask{}, ErrForbidden
	}
	task.AssignedTo = input.AssignedTo
	task.Status = "assigned"
	task.Revision++
	task.UpdatedAt = time.Now().UTC()
	s.arbitrations[key(tenantID, id)] = task
	return cloneArbitration(task), nil
}

func (s *MemoryStore) SubmitArbitration(requestCtx context.Context, tenantID string, id string, arbitratorID string, input SubmitArbitrationInput) (ArbitrationTask, FinalGrade, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	input.StudentFeedback = strings.TrimSpace(input.StudentFeedback)
	if input.Reason == "" || input.ExpectedRevision <= 0 {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay ArbitrationSubmitResult
	if found, err := s.receipts.Load(requestCtx, tenantID, arbitratorID, "review.arbitrate", id, input, &replay); err != nil || found {
		return replay.Task, replay.Grade, err
	}
	task, ok := s.arbitrations[key(tenantID, id)]
	if !ok {
		return ArbitrationTask{}, FinalGrade{}, ErrNotFound
	}
	if task.Status == "submitted" {
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
	ctx, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	if !ok {
		return ArbitrationTask{}, FinalGrade{}, ErrNotFound
	}
	if input.FinalScore < 0 || input.FinalScore > ctx.Question.Score {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	session, ok := s.sessions[key(tenantID, task.DoubleMarkSessionID)]
	if !ok {
		return ArbitrationTask{}, FinalGrade{}, ErrNotFound
	}
	finalGrade := s.createFinalGradeLocked(tenantID, arbitratorID, session, input.FinalScore, "arbitration", task.ID, "arbitration")
	now := time.Now().UTC()
	task.AssignedTo = arbitratorID
	task.Status = "submitted"
	task.FinalScore = floatPtr(input.FinalScore)
	task.Reason = input.Reason
	task.StudentFeedback = input.StudentFeedback
	task.Revision++
	task.UpdatedAt = now
	s.arbitrations[key(tenantID, task.ID)] = task
	session.Status = "arbitrated"
	session.FinalGradeID = finalGrade.ID
	session.ArbitrationTaskID = task.ID
	session.UpdatedAt = now
	s.sessions[key(tenantID, session.ID)] = session
	s.completeSessionReviewTasksLocked(tenantID, session, now)
	if err := s.receipts.Save(requestCtx, tenantID, arbitratorID, "review.arbitrate", id, input, ArbitrationSubmitResult{Task: task, Grade: finalGrade}); err != nil {
		return ArbitrationTask{}, FinalGrade{}, err
	}
	return cloneArbitration(task), cloneFinalGrade(finalGrade), nil
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

func (s *MemoryStore) upsertPolicyLocked(tenantID string, examID string, questionID string, actorID string, input SetDoubleMarkPolicyInput) DoubleMarkPolicy {
	now := time.Now().UTC()
	id := s.id("double-mark-policy")
	policyKey := policyKey(tenantID, examID, questionID)
	if existing, ok := s.policies[policyKey]; ok {
		id = existing.ID
		now = existing.CreatedAt
	}
	policy := DoubleMarkPolicy{
		ID:                  id,
		TenantID:            tenantID,
		ExamID:              examID,
		QuestionID:          questionID,
		Enabled:             input.Enabled,
		Threshold:           input.Threshold,
		ResolutionStrategy:  input.ResolutionStrategy,
		AllowSameArbitrator: input.AllowSameArbitrator,
		CreatedBy:           actorID,
		CreatedAt:           now,
		UpdatedAt:           time.Now().UTC(),
	}
	s.policies[policyKey] = policy
	return policy
}

func (s *MemoryStore) effectivePolicyLocked(tenantID string, examID string, questionID string) (DoubleMarkPolicy, bool) {
	if questionID != "" {
		policy, ok := s.policies[policyKey(tenantID, examID, questionID)]
		if ok {
			return policy, true
		}
	}
	policy, ok := s.policies[policyKey(tenantID, examID, "")]
	return policy, ok
}

func (s *MemoryStore) resolveDoubleMarkAfterGradeLocked(tenantID string, actorID string, task ReviewTask) (*DoubleMarkSession, *FinalGrade, *ArbitrationTask, error) {
	sessionID := s.sessionByTask[key(tenantID, task.ID)]
	if sessionID == "" {
		return nil, nil, nil, nil
	}
	session, ok := s.sessions[key(tenantID, sessionID)]
	if !ok {
		return nil, nil, nil, ErrNotFound
	}
	first, firstOK := s.latestGradeLocked(tenantID, session.FirstReviewTaskID)
	second, secondOK := s.latestGradeLocked(tenantID, session.SecondReviewTaskID)
	now := time.Now().UTC()
	if !firstOK || !secondOK {
		if firstOK {
			session.Status = "first_submitted"
		}
		if secondOK {
			session.Status = "second_submitted"
		}
		session.UpdatedAt = now
		s.sessions[key(tenantID, session.ID)] = session
		return &session, nil, nil, nil
	}
	diff := math.Abs(first.Score - second.Score)
	session.ScoreDifference = floatPtr(diff)
	if diff <= session.Threshold {
		score, err := resolveScore(session.ResolutionStrategy, first.Score, second.Score)
		if err != nil {
			return nil, nil, nil, err
		}
		finalGrade := s.createFinalGradeLocked(tenantID, actorID, session, score, "double_mark_auto", "", session.ResolutionStrategy)
		session.Status = "auto_finalized"
		session.FinalGradeID = finalGrade.ID
		session.UpdatedAt = now
		s.sessions[key(tenantID, session.ID)] = session
		s.completeSessionReviewTasksLocked(tenantID, session, now)
		return &session, &finalGrade, nil, nil
	}
	policy, _ := s.effectivePolicyLocked(tenantID, session.ExamID, session.QuestionID)
	reason := fmt.Sprintf("score difference %.2f exceeds threshold %.2f", diff, session.Threshold)
	arbitrationTask, err := s.createArbitrationTaskLocked(tenantID, actorID, session, first, second, CreateArbitrationTaskInput{DifferenceReason: reason}, policy.AllowSameArbitrator)
	if err != nil {
		return nil, nil, nil, err
	}
	session.Status = "needs_arbitration"
	session.ArbitrationTaskID = arbitrationTask.ID
	session.UpdatedAt = now
	s.sessions[key(tenantID, session.ID)] = session
	return &session, nil, &arbitrationTask, nil
}

func (s *MemoryStore) createArbitrationTaskLocked(tenantID string, actorID string, session DoubleMarkSession, first HumanGrade, second HumanGrade, input CreateArbitrationTaskInput, allowSame bool) (ArbitrationTask, error) {
	// 已指定仲裁人时先检查阅卷人冲突，再读取答题上下文；保持原来的拒绝顺序。
	if input.AssignedTo != "" {
		candidate := ArbitrationTask{FirstReviewerID: session.FirstReviewerID, SecondReviewerID: session.SecondReviewerID, AllowSameArbitrator: allowSame}
		if !arbitratorAllowed(candidate, input.AssignedTo) {
			return ArbitrationTask{}, ErrForbidden
		}
	}
	ctx, ok := s.contexts[key(tenantID, session.AnswerSegmentID)]
	if !ok {
		return ArbitrationTask{}, ErrNotFound
	}
	diff := math.Abs(first.Score - second.Score)
	now := time.Now().UTC()
	status := "pending"
	if input.AssignedTo != "" {
		status = "assigned"
	}
	task := ArbitrationTask{
		ID:                  s.id("arbitration-task"),
		TenantID:            tenantID,
		DoubleMarkSessionID: session.ID,
		ExamID:              session.ExamID,
		QuestionID:          session.QuestionID,
		QuestionNo:          session.QuestionNo,
		AnswerSegmentID:     session.AnswerSegmentID,
		SubmissionID:        session.SubmissionID,
		AnonymousCode:       session.AnonymousCode,
		FirstReviewerID:     session.FirstReviewerID,
		SecondReviewerID:    session.SecondReviewerID,
		FirstScore:          first.Score,
		SecondScore:         second.Score,
		ScoreDifference:     diff,
		DifferenceReason:    strings.TrimSpace(input.DifferenceReason),
		Status:              status,
		AssignedTo:          input.AssignedTo,
		AllowSameArbitrator: allowSame,
		Context:             contextForArbitration(ctx),
		Revision:            1,
		CreatedBy:           actorID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if task.DifferenceReason == "" {
		task.DifferenceReason = fmt.Sprintf("score difference %.2f exceeds threshold %.2f", diff, session.Threshold)
	}
	s.arbitrations[key(tenantID, task.ID)] = task
	return task, nil
}

func (s *MemoryStore) createFinalGradeLocked(tenantID string, actorID string, session DoubleMarkSession, score float64, source string, arbitrationTaskID string, strategy string) FinalGrade {
	ctx := s.contexts[key(tenantID, session.AnswerSegmentID)]
	now := time.Now().UTC()
	finalGrade := FinalGrade{
		ID:                  s.id("final-grade"),
		TenantID:            tenantID,
		ExamID:              session.ExamID,
		QuestionID:          session.QuestionID,
		QuestionNo:          session.QuestionNo,
		AnswerSegmentID:     session.AnswerSegmentID,
		SubmissionID:        session.SubmissionID,
		AnonymousCode:       session.AnonymousCode,
		Score:               score,
		MaxScore:            ctx.Question.Score,
		Source:              source,
		DoubleMarkSessionID: session.ID,
		ArbitrationTaskID:   arbitrationTaskID,
		ResolutionStrategy:  strategy,
		Locked:              false,
		CreatedBy:           actorID,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	s.finalGrades[key(tenantID, finalGrade.ID)] = finalGrade
	return finalGrade
}

func (s *MemoryStore) completeSessionReviewTasksLocked(tenantID string, session DoubleMarkSession, now time.Time) {
	for _, taskID := range []string{session.FirstReviewTaskID, session.SecondReviewTaskID} {
		taskKey := key(tenantID, taskID)
		task, ok := s.tasks[taskKey]
		if !ok {
			continue
		}
		task.Status = "completed"
		task.UpdatedAt = now
		s.tasks[taskKey] = task
	}
}

func (s *MemoryStore) latestGradeLocked(tenantID string, taskID string) (HumanGrade, bool) {
	grades := s.grades[key(tenantID, taskID)]
	if len(grades) == 0 {
		return HumanGrade{}, false
	}
	return grades[len(grades)-1], true
}

func normalizeCreateTask(input CreateTaskInput) CreateTaskInput {
	input.Source = strings.TrimSpace(input.Source)
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	input.GradeRound = normalizeGradeRound(input.GradeRound)
	return input
}

func validateCreateTask(input CreateTaskInput) error {
	if input.AnswerSegmentID == "" || !validSource(input.Source) || input.Priority < 0 || !validGradeRound(input.GradeRound) {
		return ErrInvalidInput
	}
	return nil
}

func normalizePolicyInput(input SetDoubleMarkPolicyInput) SetDoubleMarkPolicyInput {
	input.ResolutionStrategy = strings.TrimSpace(input.ResolutionStrategy)
	if input.ResolutionStrategy == "" {
		input.ResolutionStrategy = "average"
	}
	return input
}

func validatePolicyInput(input SetDoubleMarkPolicyInput) error {
	if input.Threshold < 0 || !validResolutionStrategy(input.ResolutionStrategy) {
		return ErrInvalidInput
	}
	return nil
}

func validateSubmit(input SubmitGradeInput, ctx Context) error {
	if !validScore(input.Score) || !validScore(ctx.Question.Score) || input.Score > ctx.Question.Score {
		return ErrInvalidInput
	}
	hasRubric := ctx.Rubric.ID != "" || len(ctx.Rubric.Points) > 0
	if hasRubric && (!validScore(ctx.Rubric.MaxScore) || input.Score > ctx.Rubric.MaxScore) {
		return ErrInvalidInput
	}
	// 先拒绝服务端细则的无效或重复定义，再校验客户端选择及逐项累计值。
	rubricPoints, err := submitRubricPointScores(ctx)
	if err != nil {
		return err
	}
	selectedScore, err := selectedRubricScore(input.RubricSelections, rubricPoints)
	if err != nil {
		return err
	}
	// 有细则时总分须等于所选得分点之和；只容忍 0.0001 分的浮点误差，不在这里改分或四舍五入。
	if hasRubric && math.Abs(input.Score-selectedScore) > 0.0001 {
		return ErrInvalidInput
	}
	return nil
}

func submitRubricPointScores(ctx Context) (map[string]float64, error) {
	rubricPoints := map[string]float64{}
	for _, point := range ctx.Rubric.Points {
		if point.ID == "" || !validScore(point.Score) {
			return nil, ErrInvalidInput
		}
		if _, duplicate := rubricPoints[point.ID]; duplicate {
			return nil, ErrInvalidInput
		}
		rubricPoints[point.ID] = point.Score
	}
	return rubricPoints, nil
}

func selectedRubricScore(selections []RubricSelection, rubricPoints map[string]float64) (float64, error) {
	seenPoints := map[string]bool{}
	selectedScore := 0.0
	for _, selection := range selections {
		if selection.PointID == "" || !validScore(selection.Score) {
			return 0, ErrInvalidInput
		}
		pointMax, ok := rubricPoints[selection.PointID]
		if len(rubricPoints) == 0 || !ok || selection.Score > pointMax || seenPoints[selection.PointID] {
			return 0, ErrInvalidInput
		}
		seenPoints[selection.PointID] = true
		selectedScore += selection.Score
		if math.IsInf(selectedScore, 0) {
			return 0, ErrInvalidInput
		}
	}
	return selectedScore, nil
}

// aiGradeIDFromContext 使用提交时服务端加载的 AI 建议 ID，不接受客户端指定关联。
// 没有有效 ID 时返回空串；数据库写入时将其转为 NULL。
func aiGradeIDFromContext(ctx Context) string {
	value, _ := ctx.AISuggestion["ai_grade_id"].(string)
	return strings.TrimSpace(value)
}

func validScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0
}

func canReturnTask(status string) bool {
	return status != "submitted" && status != "completed"
}

func validSource(source string) bool {
	switch source {
	case "ai_low_confidence", "ocr_low_confidence", "subjective_default_review", "evidence_verification_failed", "double_mark_required", "score_anomaly", "manual_sample", "ai_human_disagreement":
		return true
	default:
		return false
	}
}

func canSubmit(status string) bool {
	switch status {
	case "assigned", "in_progress", "returned":
		return true
	default:
		return false
	}
}

func normalizeGradeRound(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "single"
	}
	return value
}

func validGradeRound(value string) bool {
	switch value {
	case "single", "first_mark", "second_mark", "appeal_review":
		return true
	default:
		return false
	}
}

func validResolutionStrategy(value string) bool {
	switch value {
	case "average", "first", "second", "higher", "lower":
		return true
	default:
		return false
	}
}

func resolveScore(strategy string, first float64, second float64) (float64, error) {
	switch strategy {
	case "average":
		return (first + second) / 2, nil
	case "first":
		return first, nil
	case "second":
		return second, nil
	case "higher":
		return math.Max(first, second), nil
	case "lower":
		return math.Min(first, second), nil
	default:
		return 0, ErrInvalidInput
	}
}

func arbitratorAllowed(task ArbitrationTask, arbitratorID string) bool {
	if task.AllowSameArbitrator {
		return true
	}
	return arbitratorID != task.FirstReviewerID && arbitratorID != task.SecondReviewerID
}

func contextForArbitration(ctx Context) ReviewContext {
	question := ctx.Question
	rubric := ctx.Rubric
	question.Rubric = nil
	return cloneReviewContext(ReviewContext{
		Question:     &question,
		Rubric:       &rubric,
		RawAnswer:    ctx.RawAnswer,
		OCRText:      ctx.OCRText,
		AISuggestion: cloneMap(ctx.AISuggestion),
	})
}

func cloneReviewContext(in ReviewContext) ReviewContext {
	// 内存实现也复制嵌套评分点；调用方编辑返回材料不能修改已冻结的仲裁依据。
	raw, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out ReviewContext
	if err := json.Unmarshal(raw, &out); err != nil {
		return in
	}
	return out
}

func key(tenantID string, id string) string {
	return tenantID + "|" + id
}

func policyKey(tenantID string, examID string, questionID string) string {
	if questionID == "" {
		return tenantID + "|exam|" + examID
	}
	return tenantID + "|question|" + questionID
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func floatPtr(in float64) *float64 {
	out := in
	return &out
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneSelections(in []RubricSelection) []RubricSelection {
	out := make([]RubricSelection, len(in))
	copy(out, in)
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneTask(in ReviewTask) ReviewTask {
	in.DueAt = cloneTime(in.DueAt)
	return in
}

func cloneGrade(in HumanGrade) HumanGrade {
	in.RubricSelections = cloneSelections(in.RubricSelections)
	return in
}

func clonePolicy(in DoubleMarkPolicy) DoubleMarkPolicy {
	return in
}

func cloneSession(in DoubleMarkSession) DoubleMarkSession {
	if in.ScoreDifference != nil {
		in.ScoreDifference = floatPtr(*in.ScoreDifference)
	}
	return in
}

func cloneSessionPtr(in *DoubleMarkSession) *DoubleMarkSession {
	if in == nil {
		return nil
	}
	out := cloneSession(*in)
	return &out
}

func cloneArbitration(in ArbitrationTask) ArbitrationTask {
	if in.FinalScore != nil {
		in.FinalScore = floatPtr(*in.FinalScore)
	}
	in.Context = cloneReviewContext(in.Context)
	return in
}

func cloneArbitrationPtr(in *ArbitrationTask) *ArbitrationTask {
	if in == nil {
		return nil
	}
	out := cloneArbitration(*in)
	return &out
}

func cloneFinalGrade(in FinalGrade) FinalGrade {
	return in
}

func cloneFinalGradePtr(in *FinalGrade) *FinalGrade {
	if in == nil {
		return nil
	}
	out := cloneFinalGrade(*in)
	return &out
}

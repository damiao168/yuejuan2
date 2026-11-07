package review

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

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

func (s *MemoryStore) ListArbitrationTasks(ctx context.Context, tenantID string, filter ArbitrationFilter) ([]ArbitrationTask, error) {
	if err := filter.Access.Validate(tenantID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ArbitrationTask{}
	scopeFilter := ListFilter{AssignedTo: filter.AssignedTo, ScopeMode: filter.ScopeMode, ScopeActorID: filter.ScopeActorID, ScopeTaskIDs: filter.ScopeTaskIDs, ScopeExamIDs: filter.ScopeExamIDs}
	if filter.Access.Scope != nil && filter.Access.Management {
		// 管理集合按实际考试学校过滤；导航考试不能排除本校记录，也不能纳入外校记录。
		scopeFilter.ScopeMode = "tenant"
	}
	for _, task := range s.arbitrations {
		if !arbitrationTaskMatchesFilter(task, tenantID, filter) || !memoryTaskScopeAllows(task.ID, task.ExamID, task.AssignedTo, scopeFilter) {
			continue
		}
		allowed, err := filter.Access.Allows(ctx, tenantID, "exam", task.ExamID, task.ExamID)
		if err != nil {
			return nil, err
		}
		if !allowed {
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

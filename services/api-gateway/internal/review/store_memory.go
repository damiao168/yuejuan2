package review

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu            sync.RWMutex
	next          int
	contexts      map[string]Context
	tasks         map[string]ReviewTask
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
		if task.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.AssignedTo != "" && task.AssignedTo != filter.AssignedTo {
			continue
		}
		if filter.ExamID != "" && task.ExamID != filter.ExamID {
			continue
		}
		out = append(out, cloneTask(task))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority == out[j].Priority {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].Priority > out[j].Priority
	})
	return out, nil
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
	if input.AssignedTo == "" {
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
	task.AssignedTo = input.AssignedTo
	task.Status = "assigned"
	task.ReturnReason = ""
	task.UpdatedAt = time.Now().UTC()
	s.tasks[key(tenantID, id)] = task
	return cloneTask(task), nil
}

func (s *MemoryStore) BatchAssignTasks(_ context.Context, tenantID string, _ string, input BatchAssignInput) ([]ReviewTask, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" || len(input.TaskIDs) == 0 {
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
	}
	now := time.Now().UTC()
	out := make([]ReviewTask, 0, len(input.TaskIDs))
	for _, id := range input.TaskIDs {
		task := s.tasks[key(tenantID, id)]
		task.AssignedTo = input.AssignedTo
		task.Status = "assigned"
		task.ReturnReason = ""
		task.UpdatedAt = now
		s.tasks[key(tenantID, id)] = task
		out = append(out, cloneTask(task))
	}
	return out, nil
}

func (s *MemoryStore) SubmitGrade(_ context.Context, tenantID string, id string, reviewerID string, input SubmitGradeInput) (SubmitResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	ctx, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	if !ok {
		return SubmitResult{}, ErrNotFound
	}
	if err := validateSubmit(input, ctx); err != nil {
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
	return SubmitResult{
		Task:              cloneTask(task),
		Grade:             cloneGrade(grade),
		DoubleMarkSession: cloneSessionPtr(session),
		FinalGrade:        cloneFinalGradePtr(finalGrade),
		ArbitrationTask:   cloneArbitrationPtr(arbitrationTask),
	}, nil
}

func (s *MemoryStore) ReturnTask(_ context.Context, tenantID string, id string, _ string, input ReturnTaskInput) (ReviewTask, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" {
		return ReviewTask{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[key(tenantID, id)]
	if !ok {
		return ReviewTask{}, ErrNotFound
	}
	if !canReturnTask(task.Status) {
		return ReviewTask{}, ErrInvalidTransition
	}
	task.Status = "returned"
	task.ReturnReason = input.Reason
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

func (s *MemoryStore) ListDoubleMarkPolicies(_ context.Context, tenantID string, filter PolicyFilter) ([]DoubleMarkPolicy, error) {
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

func (s *MemoryStore) ListDoubleMarkSessions(_ context.Context, tenantID string, filter DoubleMarkSessionFilter) ([]DoubleMarkSession, error) {
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
	task, err := s.createArbitrationTaskLocked(tenantID, actorID, session, first, second, input.DifferenceReason, input.AssignedTo, policy.AllowSameArbitrator)
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
	for _, task := range s.arbitrations {
		if task.TenantID != tenantID {
			continue
		}
		if filter.Status != "" && task.Status != filter.Status {
			continue
		}
		if filter.AssignedTo != "" && task.AssignedTo != filter.AssignedTo {
			continue
		}
		out = append(out, cloneArbitration(task))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (s *MemoryStore) GetArbitrationTask(_ context.Context, tenantID string, id string) (ArbitrationTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.arbitrations[key(tenantID, id)]
	if !ok {
		return ArbitrationTask{}, ErrNotFound
	}
	return cloneArbitration(task), nil
}

func (s *MemoryStore) AssignArbitrationTask(_ context.Context, tenantID string, id string, _ string, input AssignArbitrationTaskInput) (ArbitrationTask, error) {
	input.AssignedTo = strings.TrimSpace(input.AssignedTo)
	if input.AssignedTo == "" {
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
	if !arbitratorAllowed(task, input.AssignedTo) {
		return ArbitrationTask{}, ErrForbidden
	}
	task.AssignedTo = input.AssignedTo
	task.Status = "assigned"
	task.UpdatedAt = time.Now().UTC()
	s.arbitrations[key(tenantID, id)] = task
	return cloneArbitration(task), nil
}

func (s *MemoryStore) SubmitArbitration(_ context.Context, tenantID string, id string, arbitratorID string, input SubmitArbitrationInput) (ArbitrationTask, FinalGrade, error) {
	input.Reason = strings.TrimSpace(input.Reason)
	input.StudentFeedback = strings.TrimSpace(input.StudentFeedback)
	if input.Reason == "" {
		return ArbitrationTask{}, FinalGrade{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.arbitrations[key(tenantID, id)]
	if !ok {
		return ArbitrationTask{}, FinalGrade{}, ErrNotFound
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
	task.UpdatedAt = now
	s.arbitrations[key(tenantID, task.ID)] = task
	session.Status = "arbitrated"
	session.FinalGradeID = finalGrade.ID
	session.ArbitrationTaskID = task.ID
	session.UpdatedAt = now
	s.sessions[key(tenantID, session.ID)] = session
	s.completeSessionReviewTasksLocked(tenantID, session, now)
	return cloneArbitration(task), cloneFinalGrade(finalGrade), nil
}

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
	arbitrationTask, err := s.createArbitrationTaskLocked(tenantID, actorID, session, first, second, reason, "", policy.AllowSameArbitrator)
	if err != nil {
		return nil, nil, nil, err
	}
	session.Status = "needs_arbitration"
	session.ArbitrationTaskID = arbitrationTask.ID
	session.UpdatedAt = now
	s.sessions[key(tenantID, session.ID)] = session
	return &session, nil, &arbitrationTask, nil
}

func (s *MemoryStore) createArbitrationTaskLocked(tenantID string, actorID string, session DoubleMarkSession, first HumanGrade, second HumanGrade, reason string, assignedTo string, allowSame bool) (ArbitrationTask, error) {
	if assignedTo != "" {
		candidate := ArbitrationTask{FirstReviewerID: session.FirstReviewerID, SecondReviewerID: session.SecondReviewerID, AllowSameArbitrator: allowSame}
		if !arbitratorAllowed(candidate, assignedTo) {
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
	if assignedTo != "" {
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
		DifferenceReason:    strings.TrimSpace(reason),
		Status:              status,
		AssignedTo:          assignedTo,
		AllowSameArbitrator: allowSame,
		Context:             contextForArbitration(ctx),
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
	rubricPoints := map[string]float64{}
	for _, point := range ctx.Rubric.Points {
		if point.ID == "" || !validScore(point.Score) {
			return ErrInvalidInput
		}
		if _, duplicate := rubricPoints[point.ID]; duplicate {
			return ErrInvalidInput
		}
		rubricPoints[point.ID] = point.Score
	}
	seenPoints := map[string]bool{}
	selectedScore := 0.0
	for _, selection := range input.RubricSelections {
		if selection.PointID == "" || !validScore(selection.Score) {
			return ErrInvalidInput
		}
		pointMax, ok := rubricPoints[selection.PointID]
		if len(rubricPoints) == 0 || !ok || selection.Score > pointMax || seenPoints[selection.PointID] {
			return ErrInvalidInput
		}
		seenPoints[selection.PointID] = true
		selectedScore += selection.Score
		if math.IsInf(selectedScore, 0) {
			return ErrInvalidInput
		}
	}
	if hasRubric && math.Abs(input.Score-selectedScore) > 0.0001 {
		return ErrInvalidInput
	}
	return nil
}

// aiGradeIDFromContext resolves the AI suggestion identifier from the
// server-loaded task context. Client input is never consulted: the value is
// copied from the ai_grade row the reviewer saw when submitting. An empty
// string means no AI suggestion existed and NULL is stored.
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
	case "ai_low_confidence", "ocr_low_confidence", "subjective_default_review", "evidence_verification_failed", "double_mark_required", "score_anomaly", "manual_sample":
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
	return ReviewContext{
		RawAnswer:    ctx.RawAnswer,
		OCRText:      ctx.OCRText,
		AISuggestion: cloneMap(ctx.AISuggestion),
	}
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
	in.Context.AISuggestion = cloneMap(in.Context.AISuggestion)
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

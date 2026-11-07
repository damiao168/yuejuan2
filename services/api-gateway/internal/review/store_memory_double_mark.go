package review

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

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

func policyKey(tenantID string, examID string, questionID string) string {
	if questionID == "" {
		return tenantID + "|exam|" + examID
	}
	return tenantID + "|question|" + questionID
}

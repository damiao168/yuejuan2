package seedquality

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type samplingCursor struct {
	ClaimsSinceSeed int
	ForceAt         int
}

type memoryPolicy struct {
	tenantID string
	policy   Policy
}

type memoryTask struct {
	tenantID string
	task     Task
}

type memoryObservation struct {
	tenantID string
	value    Observation
}

type MemoryStore struct {
	receipts     commandreceipt.Memory
	mu           sync.RWMutex
	policies     map[string]memoryPolicy
	cursors      map[string]samplingCursor
	tasks        map[string]memoryTask
	observations map[string]memoryObservation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{policies: map[string]memoryPolicy{}, cursors: map[string]samplingCursor{},
		tasks: map[string]memoryTask{}, observations: map[string]memoryObservation{}}
}

func (s *MemoryStore) PutPolicy(_ context.Context, tenantID, examID, questionID, actorID string, input PutPolicyInput, fingerprint string) (Policy, error) {
	if tenantID == "" || examID == "" || questionID == "" || actorID == "" || fingerprint == "" {
		return Policy{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := policyKey(tenantID, examID, questionID)
	current, exists := s.policies[key]
	if exists && current.policy.Revision != input.ExpectedRevision || !exists && input.ExpectedRevision != 0 {
		return Policy{}, ErrConflict
	}
	now := time.Now().UTC()
	policy := Policy{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID, Rate: input.Rate,
		MinInterval: input.MinInterval, MaxInterval: input.MaxInterval, ActiveGoldFingerprint: fingerprint,
		Status: input.Status, Revision: 1, CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	if exists {
		policy.ID, policy.CreatedBy, policy.CreatedAt, policy.Revision = current.policy.ID, current.policy.CreatedBy, current.policy.CreatedAt, current.policy.Revision+1
	}
	s.policies[key] = memoryPolicy{tenantID: tenantID, policy: policy}
	return policy, nil
}

func (s *MemoryStore) GetPolicy(_ context.Context, tenantID, examID, questionID string) (Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.policies[policyKey(tenantID, examID, questionID)]
	if !ok {
		return Policy{}, ErrNotFound
	}
	return item.policy, nil
}

// 内存实现也要在同一把锁内推进游标和创建任务，保持测试行为与生产事务一致。
func (s *MemoryStore) AdvanceAndMaybeCreate(_ context.Context, tenantID string, decision IssueDecision) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if decision.Policy.Status != PolicyActive || decision.GraderID == "" || decision.Gold.GoldPaperID == "" || decision.NextInterval <= 0 {
		return Task{}, false, ErrInvalidInput
	}
	for _, item := range s.tasks {
		if item.tenantID == tenantID && item.task.ExamID == decision.Policy.ExamID && item.task.QuestionID == decision.Policy.QuestionID &&
			item.task.AssignedTo == decision.GraderID && item.task.Status == "in_progress" {
			return cloneTask(item.task), true, nil
		}
	}
	key := cursorKey(tenantID, decision.Policy.ExamID, decision.Policy.QuestionID, decision.GraderID)
	cursor := s.cursors[key]
	if cursor.ForceAt == 0 {
		cursor.ForceAt = decision.NextInterval
	}
	cursor.ClaimsSinceSeed++
	due := cursor.ClaimsSinceSeed >= cursor.ForceAt ||
		(cursor.ClaimsSinceSeed >= decision.Policy.MinInterval && decision.Probability < decision.Policy.Rate)
	if !due {
		s.cursors[key] = cursor
		return Task{}, false, nil
	}
	cursor.ClaimsSinceSeed, cursor.ForceAt = 0, decision.NextInterval
	s.cursors[key] = cursor
	now := decision.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	id := uuid.NewString()
	task := Task{ID: id, ExamID: decision.Policy.ExamID, QuestionID: decision.Policy.QuestionID,
		QuestionNo: decision.QuestionNo, AnonymousCode: anonymousCode(id),
		AnswerImageURL: "/api/v1/review-tasks/" + id + "/segment-image", Source: "manual",
		Status: "in_progress", Priority: 0, AssignedTo: decision.GraderID, MaxScore: decision.Gold.MaxScore,
		Revision: 1, CreatedAt: now, UpdatedAt: now, GoldPaperID: decision.Gold.GoldPaperID,
		GoldVersion: decision.Gold.GoldVersion, SnapshotID: decision.Gold.SnapshotID,
		ReferenceScore: decision.Gold.ReferenceScore, ExpectedCriteria: cloneObject(decision.Gold.ExpectedCriteria),
		ArchetypeCode: decision.Gold.ArchetypeCode, SourceImageURL: decision.Gold.AnswerImageURL}
	s.tasks[taskKey(tenantID, id)] = memoryTask{tenantID: tenantID, task: task}
	return cloneTask(task), true, nil
}

func (s *MemoryStore) GetTask(_ context.Context, tenantID, id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.tasks[taskKey(tenantID, id)]
	if !ok {
		return Task{}, ErrNotFound
	}
	return cloneTask(item.task), nil
}

// 命令收据和任务状态一起更新，重复提交返回第一次结果，不重复写观察记录。
func (s *MemoryStore) CompleteTask(ctx context.Context, tenantID, taskID, graderID string, input SubmitInput, observation Observation) (Task, Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay commandResult
	if found, err := s.receipts.Load(ctx, tenantID, graderID, "review.submit", taskID, input, &replay); err != nil || found {
		return Task{ID: replay.Task.ID, Status: replay.Task.Status, Revision: replay.Task.Revision}, Observation{}, err
	}
	key := taskKey(tenantID, taskID)
	item, ok := s.tasks[key]
	if !ok {
		return Task{}, Observation{}, ErrNotFound
	}
	if item.task.AssignedTo != graderID {
		return Task{}, Observation{}, ErrSeedTaskForbidden
	}
	if item.task.Status != "in_progress" || item.task.Revision != input.ExpectedRevision {
		return Task{}, Observation{}, ErrConflict
	}
	item.task.Status, item.task.Revision, item.task.UpdatedAt = "completed", item.task.Revision+1, observation.ObservedAt.UTC()
	s.tasks[key] = item
	observation.ID = uuid.NewString()
	observation.MaxScore = item.task.MaxScore
	observation.RubricSelections = cloneObject(observation.RubricSelections)
	observation.TraitObservation = cloneObjectOrNil(observation.TraitObservation)
	observation.CriterionObservation = cloneObjectOrNil(observation.CriterionObservation)
	s.observations[taskKey(tenantID, observation.ID)] = memoryObservation{tenantID: tenantID, value: observation}
	if err := s.receipts.Save(ctx, tenantID, graderID, "review.submit", taskID, input, seedCommandResult(item.task)); err != nil {
		return Task{}, Observation{}, err
	}
	return cloneTask(item.task), cloneObservation(observation), nil
}

func (s *MemoryStore) ListObservations(_ context.Context, tenantID string, filter ObservationFilter) ([]Observation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Observation{}
	for _, item := range s.observations {
		value := item.value
		if item.tenantID != tenantID || filter.ExamID != "" && value.ExamID != filter.ExamID ||
			filter.QuestionID != "" && value.QuestionID != filter.QuestionID || filter.GraderID != "" && value.GraderID != filter.GraderID {
			continue
		}
		out = append(out, cloneObservation(value))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObservedAt.After(out[j].ObservedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func policyKey(tenantID, examID, questionID string) string {
	return tenantID + "\x00" + examID + "\x00" + questionID
}
func cursorKey(tenantID, examID, questionID, graderID string) string {
	return policyKey(tenantID, examID, questionID) + "\x00" + graderID
}
func taskKey(tenantID, id string) string { return tenantID + "\x00" + id }
func anonymousCode(id string) string {
	compact := ""
	for _, value := range id {
		if value != '-' {
			compact += string(value)
		}
	}
	if len(compact) > 10 {
		compact = compact[:10]
	}
	return "ANON-" + compact
}
func cloneTask(value Task) Task {
	value.ExpectedCriteria = cloneObject(value.ExpectedCriteria)
	return value
}
func cloneObjectOrNil(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	return cloneObject(value)
}
func cloneObservation(value Observation) Observation {
	value.RubricSelections = cloneObject(value.RubricSelections)
	value.TraitObservation = cloneObjectOrNil(value.TraitObservation)
	value.CriterionObservation = cloneObjectOrNil(value.CriterionObservation)
	if value.RubricAgreement != nil {
		copy := *value.RubricAgreement
		value.RubricAgreement = &copy
	}
	return value
}

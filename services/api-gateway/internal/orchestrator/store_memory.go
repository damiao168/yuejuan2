package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu    sync.RWMutex
	next  int
	runs  map[string]Run
	tasks map[string]Task
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, runs: map[string]Run{}, tasks: map[string]Task{}}
}

func (s *MemoryStore) CreateRun(_ context.Context, tenantID string, actorID string, input CreateRunInput) (Run, error) {
	input = NormalizeRunInput(input)
	if err := ValidateRunInput(input); err != nil {
		return Run{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run := Run{
		ID:           s.id("orchestration"),
		TenantID:     tenantID,
		WorkflowType: input.WorkflowType,
		TargetType:   input.TargetType,
		TargetID:     input.TargetID,
		Status:       "created",
		CreatedBy:    actorID,
		CreatedAt:    time.Now().UTC(),
	}
	s.runs[run.ID] = run
	return run, nil
}

func (s *MemoryStore) GetRun(_ context.Context, tenantID string, id string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[id]
	if !ok || run.TenantID != tenantID {
		return Run{}, ErrNotFound
	}
	run.Tasks = s.tasksForRunLocked(tenantID, id)
	return run, nil
}

func (s *MemoryStore) ListTasks(_ context.Context, tenantID string, runID string) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if run, ok := s.runs[runID]; !ok || run.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return s.tasksForRunLocked(tenantID, runID), nil
}

func (s *MemoryStore) CreateTask(_ context.Context, tenantID string, runID string, actorID string, input CreateTaskInput) (Task, error) {
	input = NormalizeTaskInput(input)
	if err := ValidateTaskInput(input); err != nil {
		return Task{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	task := Task{
		ID:                 s.id("agent-task"),
		TenantID:           tenantID,
		OrchestrationRunID: runID,
		AgentType:          input.AgentType,
		Status:             "queued",
		InputRef:           cloneMap(input.InputRef),
		OutputRef:          map[string]any{},
		EvidenceRef:        map[string]any{},
		AttemptNo:          1,
		MaxAttempts:        input.MaxAttempts,
		CreatedBy:          actorID,
		CreatedAt:          time.Now().UTC(),
	}
	s.tasks[task.ID] = task
	run.Status = "running"
	s.runs[runID] = run
	return task, nil
}

func (s *MemoryStore) StartTask(_ context.Context, tenantID string, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if !CanStart(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = "running"
	task.StartedAt = &now
	s.tasks[id] = task
	return task, nil
}

// 完成任务时按置信度决定是否转人工复核；任务和运行状态在同一把锁内更新。
func (s *MemoryStore) CompleteTask(_ context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if !CanComplete(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	if input.Confidence != nil && (*input.Confidence < 0 || *input.Confidence > 1) {
		return Task{}, ErrInvalidInput
	}
	// 低置信度或显式复核都会进入人工复核；成功状态不能掩盖不可靠的智能体输出。
	status := "succeeded"
	if input.RequiresHumanReview || (input.Confidence != nil && *input.Confidence < 0.8) {
		status = "requires_human_review"
	}
	now := time.Now().UTC()
	task.Status = status
	task.OutputRef = cloneMap(input.OutputRef)
	task.EvidenceRef = cloneMap(input.EvidenceRef)
	task.Confidence = input.Confidence
	task.CompletedAt = &now
	s.tasks[id] = task
	s.refreshRunStatusLocked(tenantID, task.OrchestrationRunID)
	return task, nil
}

func (s *MemoryStore) FailTask(_ context.Context, tenantID string, id string, errorMessage string) (Task, error) {
	errorMessage = strings.TrimSpace(errorMessage)
	if errorMessage == "" {
		return Task{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if !CanFail(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	task.Status = "failed"
	task.ErrorMessage = errorMessage
	task.CompletedAt = &now
	s.tasks[id] = task
	s.refreshRunStatusLocked(tenantID, task.OrchestrationRunID)
	return task, nil
}

func (s *MemoryStore) RetryTask(_ context.Context, tenantID string, id string) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	if !ok || task.TenantID != tenantID {
		return Task{}, ErrNotFound
	}
	if !CanRetry(task.Status, task.AttemptNo, task.MaxAttempts) {
		return Task{}, ErrRetryExhausted
	}
	task.Status = "queued"
	task.AttemptNo++
	task.ErrorMessage = ""
	task.StartedAt = nil
	task.CompletedAt = nil
	s.tasks[id] = task
	s.refreshRunStatusLocked(tenantID, task.OrchestrationRunID)
	return task, nil
}

func (s *MemoryStore) tasksForRunLocked(tenantID string, runID string) []Task {
	out := []Task{}
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.OrchestrationRunID == runID {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// 运行状态由所有任务的最严重状态汇总，失败和待人工复核优先于“进行中”。
func (s *MemoryStore) refreshRunStatusLocked(tenantID string, runID string) {
	run, ok := s.runs[runID]
	if !ok || run.TenantID != tenantID {
		return
	}
	tasks := s.tasksForRunLocked(tenantID, runID)
	if len(tasks) == 0 {
		run.Status = "created"
		s.runs[runID] = run
		return
	}
	hasFailed := false
	hasHumanReview := false
	hasActive := false
	for _, task := range tasks {
		switch task.Status {
		case "failed":
			hasFailed = true
		case "requires_human_review":
			hasHumanReview = true
		case "queued", "running":
			hasActive = true
		}
	}
	switch {
	case hasFailed:
		run.Status = "failed"
	case hasHumanReview:
		run.Status = "requires_human_review"
	case hasActive:
		run.Status = "running"
	default:
		run.Status = "completed"
	}
	s.runs[runID] = run
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

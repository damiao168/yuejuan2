package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateRun(ctx context.Context, tenantID string, actorID string, input CreateRunInput) (Run, error) {
	input = NormalizeRunInput(input)
	if err := ValidateRunInput(input); err != nil {
		return Run{}, err
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO orchestration_run (tenant_id, workflow_type, target_type, target_id, status, created_by)
VALUES ($1, $2, $3, $4, 'created', $5)
RETURNING id::text, tenant_id::text, workflow_type, target_type, target_id::text, status, created_by::text, created_at
`, tenantID, input.WorkflowType, input.TargetType, input.TargetID, actorID)
	var out Run
	if err := scanRun(row, &out); err != nil {
		return Run{}, err
	}
	return out, nil
}

func (s *PostgresStore) GetRun(ctx context.Context, tenantID string, id string) (Run, error) {
	run, err := s.getRun(ctx, tenantID, id)
	if err != nil {
		return Run{}, err
	}
	tasks, err := s.ListTasks(ctx, tenantID, id)
	if err != nil {
		return Run{}, err
	}
	run.Tasks = tasks
	return run, nil
}

func (s *PostgresStore) ListTasks(ctx context.Context, tenantID string, runID string) ([]Task, error) {
	if _, err := s.getRun(ctx, tenantID, runID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
FROM agent_task
WHERE tenant_id = $1 AND orchestration_run_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at, id
`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Task{}
	for rows.Next() {
		var task Task
		if err := scanTask(rows, &task); err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

// 建立任务和把运行标为 running 必须同一事务提交，避免只写了一半导致流程状态失真。
func (s *PostgresStore) CreateTask(ctx context.Context, tenantID string, runID string, actorID string, input CreateTaskInput) (Task, error) {
	input = NormalizeTaskInput(input)
	if err := ValidateTaskInput(input); err != nil {
		return Task{}, err
	}
	if _, err := s.getRun(ctx, tenantID, runID); err != nil {
		return Task{}, err
	}
	inputRef, _ := json.Marshal(input.InputRef)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
INSERT INTO agent_task (
  tenant_id, orchestration_run_id, agent_type, status, input_ref, max_attempts, created_by
)
VALUES ($1, $2, $3, 'queued', $4, $5, $6)
RETURNING id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
`, tenantID, runID, input.AgentType, inputRef, input.MaxAttempts, actorID)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE orchestration_run
SET status = 'running', updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, runID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return out, nil
}

func (s *PostgresStore) StartTask(ctx context.Context, tenantID string, id string) (Task, error) {
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanStart(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE agent_task
SET status = 'running', started_at = now(), updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
`, tenantID, id)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	return out, nil
}

// 任务结果与运行汇总状态同一事务落库，提交失败时不会留下半完成的智能体任务。
func (s *PostgresStore) CompleteTask(ctx context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanComplete(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	if input.Confidence != nil && (*input.Confidence < 0 || *input.Confidence > 1) {
		return Task{}, ErrInvalidInput
	}
	status := "succeeded"
	if input.RequiresHumanReview || (input.Confidence != nil && *input.Confidence < 0.8) {
		status = "requires_human_review"
	}
	outputRef, _ := json.Marshal(cloneMap(input.OutputRef))
	evidenceRef, _ := json.Marshal(cloneMap(input.EvidenceRef))
	var confidence any
	if input.Confidence != nil {
		confidence = *input.Confidence
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE agent_task
SET status = $3,
  output_ref = $4,
  evidence_ref = $5,
  confidence = $6,
  completed_at = now(),
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
`, tenantID, id, status, outputRef, evidenceRef, confidence)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	if err := refreshRunStatusTx(ctx, tx, tenantID, out.OrchestrationRunID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return out, nil
}

func (s *PostgresStore) FailTask(ctx context.Context, tenantID string, id string, errorMessage string) (Task, error) {
	errorMessage = strings.TrimSpace(errorMessage)
	if errorMessage == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanFail(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE agent_task
SET status = 'failed',
  error_message = $3,
  completed_at = now(),
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
`, tenantID, id, errorMessage)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	if err := refreshRunStatusTx(ctx, tx, tenantID, out.OrchestrationRunID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return out, nil
}

func (s *PostgresStore) RetryTask(ctx context.Context, tenantID string, id string) (Task, error) {
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if !CanRetry(task.Status, task.AttemptNo, task.MaxAttempts) {
		return Task{}, ErrRetryExhausted
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE agent_task
SET status = 'queued',
  attempt_no = attempt_no + 1,
  error_message = NULL,
  started_at = NULL,
  completed_at = NULL,
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
`, tenantID, id)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	if err := refreshRunStatusTx(ctx, tx, tenantID, out.OrchestrationRunID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return out, nil
}

func (s *PostgresStore) getRun(ctx context.Context, tenantID string, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, workflow_type, target_type, target_id::text, status, created_by::text, created_at
FROM orchestration_run
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	var out Run
	if err := scanRun(row, &out); err != nil {
		return Run{}, err
	}
	return out, nil
}

func (s *PostgresStore) getTask(ctx context.Context, tenantID string, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, orchestration_run_id::text, agent_type, status,
  input_ref, output_ref, evidence_ref, confidence::float8, attempt_no, max_attempts,
  COALESCE(error_message, ''), created_by::text, started_at, completed_at, created_at
FROM agent_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	return out, nil
}

func refreshRunStatusTx(ctx context.Context, tx *sql.Tx, tenantID string, runID string) error {
	rows, err := tx.QueryContext(ctx, `
SELECT status
FROM agent_task
WHERE tenant_id = $1 AND orchestration_run_id::text = $2 AND deleted_at IS NULL
`, tenantID, runID)
	if err != nil {
		return err
	}
	defer rows.Close()
	total := 0
	hasFailed := false
	hasHumanReview := false
	hasActive := false
	for rows.Next() {
		total++
		var status string
		if err := rows.Scan(&status); err != nil {
			return err
		}
		switch status {
		case "failed":
			hasFailed = true
		case "requires_human_review":
			hasHumanReview = true
		case "queued", "running":
			hasActive = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	status := "created"
	switch {
	case total == 0:
		status = "created"
	case hasFailed:
		status = "failed"
	case hasHumanReview:
		status = "requires_human_review"
	case hasActive:
		status = "running"
	default:
		status = "completed"
	}
	_, err = tx.ExecContext(ctx, `
UPDATE orchestration_run
SET status = $3, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, runID, status)
	return err
}

type runScanner interface {
	Scan(dest ...any) error
}

func scanRun(row runScanner, out *Run) error {
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.WorkflowType,
		&out.TargetType,
		&out.TargetID,
		&out.Status,
		&out.CreatedBy,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return nil
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner, out *Task) error {
	var inputRaw, outputRaw, evidenceRaw []byte
	var confidence sql.NullFloat64
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.OrchestrationRunID,
		&out.AgentType,
		&out.Status,
		&inputRaw,
		&outputRaw,
		&evidenceRaw,
		&confidence,
		&out.AttemptNo,
		&out.MaxAttempts,
		&out.ErrorMessage,
		&out.CreatedBy,
		&startedAt,
		&completedAt,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	out.InputRef = map[string]any{}
	out.OutputRef = map[string]any{}
	out.EvidenceRef = map[string]any{}
	_ = json.Unmarshal(inputRaw, &out.InputRef)
	_ = json.Unmarshal(outputRaw, &out.OutputRef)
	_ = json.Unmarshal(evidenceRaw, &out.EvidenceRef)
	if confidence.Valid {
		value := confidence.Float64
		out.Confidence = &value
	}
	if startedAt.Valid {
		value := startedAt.Time.UTC()
		out.StartedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		out.CompletedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return nil
}

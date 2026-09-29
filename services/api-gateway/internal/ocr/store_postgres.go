package ocr

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateTask(ctx context.Context, tenantID string, submissionID string, actorID string, input CreateTaskInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := createSourceTaskInTx(ctx, tx, tenantID, submissionID, actorID, input)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// 插入失败后按幂等键锁读已有任务，确保并发创建只产生一个源任务且参数一致。
func createSourceTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, submissionID string, actorID string, input CreateTaskInput) (Task, error) {
	if tx == nil || tenantID == "" || submissionID == "" || actorID == "" {
		return Task{}, ErrInvalidInput
	}
	input, err := PrepareCreateInput(submissionID, input)
	if err != nil {
		return Task{}, err
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO ocr_task (
  tenant_id, submission_id, status, engine, engine_version, min_confidence,
  requested_by, idempotency_key
)
VALUES ($1, $2, 'queued', $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, idempotency_key) WHERE deleted_at IS NULL DO NOTHING
RETURNING id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
`, tenantID, submissionID, input.Engine, input.EngineVersion, input.MinConfidence, actorID, input.IdempotencyKey)
	var task Task
	if err := scanTask(row, &task); err == nil {
		return task, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Task{}, err
	}
	row = tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
FROM ocr_task
WHERE tenant_id = $1 AND idempotency_key = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, input.IdempotencyKey)
	if err := scanTask(row, &task); err != nil {
		return Task{}, err
	}
	if !sameCreateRequest(task, submissionID, input) {
		return Task{}, ErrIdempotencyConflict
	}
	return task, nil
}

func (s *PostgresStore) ListPending(ctx context.Context, tenantID string, limit int) ([]Task, error) {
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
FROM ocr_task
WHERE tenant_id = $1 AND status = 'queued' AND deleted_at IS NULL
ORDER BY created_at ASC
LIMIT $2
`, tenantID, limit)
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

func (s *PostgresStore) ListBySubmission(ctx context.Context, tenantID string, submissionID string, filter TaskListFilter) ([]Task, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 101 {
		limit = 21
	}
	var cursorCreatedAt any
	if !filter.CursorCreatedAt.IsZero() {
		cursorCreatedAt = filter.CursorCreatedAt
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
FROM ocr_task
WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
  AND ($3::timestamptz IS NULL OR created_at < $3 OR (created_at = $3 AND id::text < $4))
ORDER BY created_at DESC, id DESC
LIMIT $5
`, tenantID, submissionID, cursorCreatedAt, filter.CursorID, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	args := []any{tenantID}
	placeholders := make([]string, 0, len(out))
	for _, task := range out {
		args = append(args, task.ID)
		placeholders = append(placeholders, fmt.Sprintf("$%d::uuid", len(args)))
	}
	resultRows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, ocr_task_id::text, submission_id::text, submission_page_id::text,
  text, bbox, confidence::float8, ocr_engine, ocr_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(preprocess_profile, ''), COALESCE(source_image_file_id::text, ''), created_at
FROM ocr_result
WHERE tenant_id = $1 AND ocr_task_id IN (`+strings.Join(placeholders, ",")+`) AND deleted_at IS NULL
ORDER BY ocr_task_id, created_at, id
`, args...)
	if err != nil {
		return nil, err
	}
	defer resultRows.Close()
	byTask := make(map[string][]Result, len(out))
	for resultRows.Next() {
		var result Result
		if err := scanResult(resultRows, &result); err != nil {
			return nil, err
		}
		byTask[result.TaskID] = append(byTask[result.TaskID], result)
	}
	if err := resultRows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Results = byTask[out[i].ID]
	}
	return out, nil
}

func (s *PostgresStore) GetTask(ctx context.Context, tenantID string, id string) (Task, error) {
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	results, err := s.results(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	task.Results = results
	return task, nil
}

func (s *PostgresStore) StartTask(ctx context.Context, tenantID string, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
UPDATE ocr_task
SET status = 'processing', started_at = now(), completed_at = NULL,
  error_message = NULL, updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND status IN ('queued', 'failed') AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
`, tenantID, id)
	var out Task
	if err := scanTask(row, &out); err == nil {
		return out, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Task{}, err
	}

	// A second worker runtime lease may legitimately resume a source task that
	// is already processing after the previous lease expired. Do not rewrite
	// timestamps or any result metadata in that idempotent case.
	task, err := s.getTask(ctx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if IsStarted(task.Status) {
		return task, nil
	}
	return Task{}, ErrInvalidTransition
}

func (s *PostgresStore) CompleteTask(ctx context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	out, err := completeSourceTaskInTx(ctx, tx, tenantID, id, input)
	if err != nil {
		return Task{}, err
	}
	return out, tx.Commit()
}

// 行锁覆盖结果写入和状态更新；重复完成比较完整载荷，避免只按状态返回错误结果。
func completeSourceTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, id string, input CompleteTaskInput) (Task, error) {
	if tx == nil || len(input.Results) == 0 || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	task, err := getSourceTaskForUpdate(ctx, tx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if task.Status == "completed" {
		task.Results, err = resultsFrom(ctx, tx, tenantID, id)
		if err != nil {
			return Task{}, err
		}
		if sameCompletion(task, input) {
			return task, nil
		}
		return Task{}, ErrResultConflict
	}
	if !CanComplete(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	requiresReview := false
	resultCount := 0
	for _, item := range input.Results {
		if err := ValidateResultInput(item); err != nil {
			return Task{}, err
		}
		if err := ensurePageBelongsToSubmission(ctx, tx, tenantID, task.SubmissionID, item.SubmissionPageID); err != nil {
			return Task{}, err
		}
		if item.Confidence < task.MinConfidence {
			requiresReview = true
		}
		bbox, _ := json.Marshal(item.BBox)
		if _, err := tx.ExecContext(ctx, `
INSERT INTO ocr_result (
  tenant_id, ocr_task_id, submission_id, submission_page_id,
  text, bbox, confidence, ocr_engine, ocr_version,
  model_version, config_hash, input_hash, preprocess_profile, source_image_file_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''), NULLIF($11, ''), NULLIF($12, ''), NULLIF($13, ''), NULLIF($14, '')::uuid)
`, tenantID, id, task.SubmissionID, item.SubmissionPageID,
			item.Text, bbox, item.Confidence, task.Engine, task.EngineVersion,
			input.ModelVersion, input.ConfigHash, input.InputHash, input.PreprocessProfile, item.SourceImageFileID); err != nil {
			return Task{}, err
		}
		resultCount++
	}
	row := tx.QueryRowContext(ctx, `
UPDATE ocr_task
SET status = 'completed',
  result_count = $3,
  requires_human_review = $4,
  model_version = NULLIF($5, ''),
  config_hash = NULLIF($6, ''),
  input_hash = NULLIF($7, ''),
  duration_ms = $8,
  worker_id = NULLIF($9, ''),
  attempt_count = attempt_count + 1,
  completed_at = now(),
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
`, tenantID, id, resultCount, requiresReview, input.ModelVersion, input.ConfigHash, input.InputHash, input.DurationMS, input.WorkerID)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	results, err := resultsFrom(ctx, tx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	out.Results = results
	return out, nil
}

func (s *PostgresStore) FailTask(ctx context.Context, tenantID string, id string, errorMessage string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	out, err := failSourceTaskInTx(ctx, tx, tenantID, id, errorMessage)
	if err != nil {
		return Task{}, err
	}
	return out, tx.Commit()
}

func failSourceTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, id string, errorMessage string) (Task, error) {
	if tx == nil || errorMessage == "" {
		return Task{}, ErrInvalidInput
	}
	task, err := getSourceTaskForUpdate(ctx, tx, tenantID, id)
	if err != nil {
		return Task{}, err
	}
	if task.Status == "failed" && task.ErrorMessage == errorMessage {
		return task, nil
	}
	if !CanFail(task.Status) {
		return Task{}, ErrInvalidTransition
	}
	row := tx.QueryRowContext(ctx, `
UPDATE ocr_task
SET status = 'failed', error_message = $3, completed_at = now(), updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND status IN ('queued', 'processing') AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
`, tenantID, id, errorMessage)
	var out Task
	if err := scanTask(row, &out); err != nil {
		return Task{}, err
	}
	return out, nil
}

func (s *PostgresStore) getTask(ctx context.Context, tenantID string, id string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
FROM ocr_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	var task Task
	if err := scanTask(row, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func getSourceTaskForUpdate(ctx context.Context, tx *sql.Tx, tenantID string, id string) (Task, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, status, engine, engine_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(duration_ms, 0), COALESCE(worker_id, ''), attempt_count,
  min_confidence::float8, result_count, requires_human_review, COALESCE(error_message, ''),
  requested_by::text, started_at, completed_at, created_at
FROM ocr_task
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id)
	var task Task
	if err := scanTask(row, &task); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *PostgresStore) results(ctx context.Context, tenantID string, taskID string) ([]Result, error) {
	return resultsFrom(ctx, s.db, tenantID, taskID)
}

type resultQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func resultsFrom(ctx context.Context, queryer resultQueryer, tenantID string, taskID string) ([]Result, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT id::text, tenant_id::text, ocr_task_id::text, submission_id::text, submission_page_id::text,
  text, bbox, confidence::float8, ocr_engine, ocr_version,
  COALESCE(model_version, ''), COALESCE(config_hash, ''), COALESCE(input_hash, ''),
  COALESCE(preprocess_profile, ''), COALESCE(source_image_file_id::text, ''), created_at
FROM ocr_result
WHERE tenant_id = $1 AND ocr_task_id::text = $2 AND deleted_at IS NULL
ORDER BY created_at, id
`, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Result{}
	for rows.Next() {
		var result Result
		if err := scanResult(rows, &result); err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	return out, rows.Err()
}

func ensurePageBelongsToSubmission(ctx context.Context, tx *sql.Tx, tenantID string, submissionID string, pageID string) error {
	var exists int
	err := tx.QueryRowContext(ctx, `
SELECT 1
FROM submission_page
WHERE tenant_id = $1 AND submission_id = $2 AND id::text = $3 AND deleted_at IS NULL
`, tenantID, submissionID, pageID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvalidInput
	}
	return err
}

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(row taskScanner, out *Task) error {
	var startedAt, completedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.SubmissionID,
		&out.Status,
		&out.Engine,
		&out.EngineVersion,
		&out.ModelVersion,
		&out.ConfigHash,
		&out.InputHash,
		&out.DurationMS,
		&out.WorkerID,
		&out.AttemptCount,
		&out.MinConfidence,
		&out.ResultCount,
		&out.RequiresHumanReview,
		&out.ErrorMessage,
		&out.RequestedBy,
		&startedAt,
		&completedAt,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if startedAt.Valid {
		value := startedAt.Time.UTC()
		out.StartedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		out.CompletedAt = &value
	}
	return nil
}

func scanResult(row taskScanner, out *Result) error {
	var bbox []byte
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.TaskID,
		&out.SubmissionID,
		&out.SubmissionPageID,
		&out.Text,
		&bbox,
		&out.Confidence,
		&out.OCREngine,
		&out.OCRVersion,
		&out.ModelVersion,
		&out.ConfigHash,
		&out.InputHash,
		&out.PreprocessProfile,
		&out.SourceImageFileID,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(bbox, &out.BBox)
	return nil
}

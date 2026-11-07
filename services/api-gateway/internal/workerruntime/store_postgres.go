package workerruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

type taskQueryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const taskColumns = `
id::text, tenant_id::text, task_type, queue_name, source_type, source_id::text,
status, priority, payload, payload_schema_version, result, progress,
COALESCE(result_schema_version, ''), COALESCE(result_payload_hash, ''),
idempotency_key, COALESCE(dedupe_key, ''), max_attempts, attempt_count,
retry_backoff_seconds, not_before, COALESCE(lease_token, ''), lease_expires_at,
COALESCE(leased_by, ''), COALESCE(worker_service, ''), COALESCE(worker_instance_id, ''),
started_at, completed_at, cancelled_at, COALESCE(duration_ms, 0),
COALESCE(error_code, ''), error_detail, COALESCE(created_by::text, ''), created_at, updated_at, revision`

func (s *PostgresStore) CreateTask(ctx context.Context, tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	return createTask(ctx, s.db, tenantID, actorID, input)
}

// CreateTaskInTx 在调用方的 PostgreSQL 事务内创建幂等 Worker Runtime 任务。
func CreateTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	if tx == nil {
		return Task{}, ErrInvalidInput
	}
	return createTask(ctx, tx, tenantID, actorID, input)
}

func createTask(ctx context.Context, queryer taskQueryRower, tenantID string, actorID string, input CreateTaskInput) (Task, error) {
	input = normalizeCreateInput(input)
	if tenantID == "" || validateCreateInput(input) != nil {
		return Task{}, ErrInvalidInput
	}
	payload, _ := json.Marshal(input.Payload)
	row := queryer.QueryRowContext(ctx, `
INSERT INTO agent_worker_task (
  tenant_id, task_type, queue_name, source_type, source_id, priority, payload,
  payload_schema_version, idempotency_key, dedupe_key, max_attempts,
  retry_backoff_seconds, created_by
)
VALUES ($1, $2, $3, $4, $5::uuid, $6, $7, $8, $9, NULLIF($10, ''), $11, $12, NULLIF($13, '')::uuid)
ON CONFLICT (tenant_id, task_type, idempotency_key)
DO UPDATE SET idempotency_key = EXCLUDED.idempotency_key
RETURNING `+taskColumns,
		tenantID, input.TaskType, input.QueueName, input.SourceType, input.SourceID,
		input.Priority, payload, input.PayloadSchemaVersion, input.IdempotencyKey,
		input.DedupeKey, input.MaxAttempts, input.RetryBackoffSeconds, actorID)
	return scanTask(row)
}

// CreateSucceededTaskInTx 预置可信的已完成任务，不虚构 worker 租约。
// 它比 Complete 更严格：只有从未尝试的 queued 任务可提升状态，相同的 succeeded 任务则幂等返回。
func CreateSucceededTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, actorID string, create CreateTaskInput, complete CompleteInput) (Task, error) {
	if tx == nil || complete.ResultSchemaVersion == "" || complete.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	hash, err := ResultPayloadHash(complete.ResultSchemaVersion, complete.Result)
	if err != nil {
		return Task{}, ErrInvalidInput
	}
	task, err := CreateTaskInTx(ctx, tx, tenantID, actorID, create)
	if err != nil {
		return Task{}, err
	}
	if task.Status == StatusSucceeded {
		if task.ResultPayloadHash != hash {
			return Task{}, ErrConflict
		}
		return task, nil
	}
	if task.Status != StatusQueued || task.AttemptCount != 0 {
		return Task{}, ErrInvalidTransition
	}
	result, err := json.Marshal(complete.Result)
	if err != nil {
		return Task{}, ErrInvalidInput
	}
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'succeeded', result = $3, result_schema_version = $4,
  result_payload_hash = $5, duration_ms = $6, completed_at = now(), updated_at = now(),
  error_code = NULL, error_detail = '{}', revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid AND status = 'queued' AND attempt_count = 0
RETURNING `+taskColumns, tenantID, task.ID, result, complete.ResultSchemaVersion, hash, complete.DurationMS)
	return scanTask(row)
}

func (s *PostgresStore) Requeue(ctx context.Context, tenantID string, taskID string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := RequeueTaskInTx(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// RequeueTaskInTx 让来源协调器同时恢复来源与运行时状态。调用方负责事务和来源锁。
func RequeueTaskInTx(ctx context.Context, tx *sql.Tx, tenantID, taskID string) (Task, error) {
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusDeadLetter && task.Status != StatusFailed {
		return Task{}, ErrInvalidTransition
	}
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'queued', not_before = NULL, lease_token = NULL, lease_expires_at = NULL,
  leased_by = NULL, completed_at = NULL,
  max_attempts = GREATEST(max_attempts, attempt_count + 1), updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *PostgresStore) Get(ctx context.Context, tenantID string, taskID string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM agent_worker_task WHERE tenant_id = $1 AND id = $2::uuid`, tenantID, taskID)
	task, err := scanTask(row)
	if err != nil {
		return Task{}, err
	}
	task.Attempts, err = s.listAttempts(ctx, tenantID, taskID)
	return task, err
}

func (s *PostgresStore) GetBySource(ctx context.Context, tenantID string, sourceType string, sourceID string) (Task, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT `+taskColumns+` FROM agent_worker_task
WHERE tenant_id = $1 AND source_type = $2 AND source_id = $3::uuid
ORDER BY created_at DESC LIMIT 1
`, tenantID, sourceType, sourceID)
	return scanTask(row)
}

func (s *PostgresStore) listAttempts(ctx context.Context, tenantID string, taskID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, task_id::text, attempt_no, worker_service,
  worker_instance_id, lease_token, status, started_at, heartbeat_at, completed_at,
  COALESCE(duration_ms, 0), COALESCE(error_code, ''), error_detail
FROM agent_worker_task_attempt
WHERE tenant_id = $1 AND task_id = $2::uuid ORDER BY attempt_no
`, tenantID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var item Attempt
		var heartbeat, completed sql.NullTime
		var detail []byte
		if err := rows.Scan(&item.ID, &item.TenantID, &item.TaskID, &item.AttemptNo, &item.WorkerService, &item.WorkerInstanceID, &item.LeaseToken, &item.Status, &item.StartedAt, &heartbeat, &completed, &item.DurationMS, &item.ErrorCode, &detail); err != nil {
			return nil, err
		}
		if heartbeat.Valid {
			value := heartbeat.Time.UTC()
			item.HeartbeatAt = &value
		}
		if completed.Valid {
			value := completed.Time.UTC()
			item.CompletedAt = &value
		}
		_ = json.Unmarshal(detail, &item.ErrorDetail)
		out = append(out, item)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTask(row rowScanner) (Task, error) {
	var task Task
	var payload, result, progress, errorDetail []byte
	var notBefore, leaseExpires, started, completed, cancelled sql.NullTime
	err := row.Scan(
		&task.ID, &task.TenantID, &task.TaskType, &task.QueueName, &task.SourceType, &task.SourceID,
		&task.Status, &task.Priority, &payload, &task.PayloadSchemaVersion, &result, &progress,
		&task.ResultSchemaVersion, &task.ResultPayloadHash, &task.IdempotencyKey, &task.DedupeKey,
		&task.MaxAttempts, &task.AttemptCount, &task.RetryBackoffSeconds, &notBefore,
		&task.LeaseToken, &leaseExpires, &task.LeasedBy, &task.WorkerService, &task.WorkerInstanceID,
		&started, &completed, &cancelled, &task.DurationMS, &task.ErrorCode, &errorDetail,
		&task.CreatedBy, &task.CreatedAt, &task.UpdatedAt, &task.Revision,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	if notBefore.Valid {
		value := notBefore.Time.UTC()
		task.NotBefore = &value
	}
	if leaseExpires.Valid {
		value := leaseExpires.Time.UTC()
		task.LeaseExpiresAt = &value
	}
	if started.Valid {
		value := started.Time.UTC()
		task.StartedAt = &value
	}
	if completed.Valid {
		value := completed.Time.UTC()
		task.CompletedAt = &value
	}
	if cancelled.Valid {
		value := cancelled.Time.UTC()
		task.CancelledAt = &value
	}
	_ = json.Unmarshal(payload, &task.Payload)
	_ = json.Unmarshal(result, &task.Result)
	_ = json.Unmarshal(progress, &task.Progress)
	_ = json.Unmarshal(errorDetail, &task.ErrorDetail)
	if task.Payload == nil {
		task.Payload = map[string]any{}
	}
	if task.Result == nil {
		task.Result = map[string]any{}
	}
	if task.Progress == nil {
		task.Progress = map[string]any{}
	}
	if task.ErrorDetail == nil {
		task.ErrorDetail = map[string]any{}
	}
	return task, nil
}

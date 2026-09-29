package workerruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
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

// CreateTaskInTx creates an idempotent Worker Runtime task inside the caller's
// PostgreSQL transaction.
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

// CreateSucceededTaskInTx seeds a trusted, already-completed task without
// manufacturing a worker lease. It is intentionally stricter than Complete:
// only a never-attempted queued task may be promoted, while an identical
// succeeded task is returned idempotently.
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

func (s *PostgresStore) Claim(ctx context.Context, tenantID string, input ClaimInput) ([]Task, error) {
	return s.claim(ctx, tenantID, false, input)
}

func (s *PostgresStore) ClaimAcrossTenants(ctx context.Context, platformTenantID string, input ClaimInput) ([]Task, error) {
	return s.claim(ctx, platformTenantID, true, input)
}

func (s *PostgresStore) claim(ctx context.Context, heartbeatTenantID string, acrossTenants bool, input ClaimInput) ([]Task, error) {
	input = normalizeClaimInput(input)
	if validateClaimInput(input) != nil {
		return nil, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return nil, err
	}
	// Process-owned executors do not impersonate a tenant merely to publish a
	// heartbeat. External platform workers still provide a real tenant ID.
	if heartbeatTenantID != "" {
		if err := upsertWorkerHeartbeat(ctx, tx, heartbeatTenantID, input.WorkerService, input.WorkerInstanceID, input.QueueName, dbNow, map[string]any{"state": "polling"}); err != nil {
			return nil, err
		}
	} else if !acrossTenants {
		return nil, ErrInvalidInput
	}
	var tenantScope any = heartbeatTenantID
	if acrossTenants && heartbeatTenantID == "" {
		// Passing an empty string still makes PostgreSQL cast $1 to uuid even
		// when the across-tenant branch of the OR is true. NULL keeps the
		// process-owned executor tenant-neutral without an invalid uuid cast.
		tenantScope = nil
	}
	exhaustedRows, err := tx.QueryContext(ctx, `
SELECT tenant_id::text, id::text, status
FROM agent_worker_task
WHERE ($5 OR tenant_id = $1)
  AND (NOT $5 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
  AND queue_name = $2
  AND (
    (status = 'queued' AND (not_before IS NULL OR not_before <= $4))
    OR (status IN ('leased', 'running') AND lease_expires_at < $4)
  )
  AND attempt_count >= max_attempts
  -- Import exhaustion must finish its business run in job/run -> task order.
  -- The import reconciler owns this case, including process-death recovery.
  AND paper_import_run_id IS NULL
ORDER BY priority ASC, created_at ASC, id ASC
LIMIT $3
FOR UPDATE SKIP LOCKED
`, tenantScope, input.QueueName, input.Limit, dbNow, acrossTenants)
	if err != nil {
		return nil, err
	}
	type exhaustedTask struct {
		tenantID string
		id       string
		status   string
	}
	exhausted := make([]exhaustedTask, 0)
	for exhaustedRows.Next() {
		var item exhaustedTask
		if err := exhaustedRows.Scan(&item.tenantID, &item.id, &item.status); err != nil {
			exhaustedRows.Close()
			return nil, err
		}
		exhausted = append(exhausted, item)
	}
	if err = exhaustedRows.Err(); err != nil {
		exhaustedRows.Close()
		return nil, err
	}
	if err := exhaustedRows.Close(); err != nil {
		return nil, err
	}
	for _, item := range exhausted {
		errorCode := "max_attempts_exhausted"
		if item.status == StatusLeased || item.status == StatusRunning {
			errorCode = "lease_expired"
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'lease_expired', completed_at = $3, error_code = 'lease_expired',
  error_detail = '{"reason":"max_attempts_exhausted"}'::jsonb
WHERE tenant_id = $1 AND task_id = $2::uuid AND completed_at IS NULL
`, item.tenantID, item.id, dbNow); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task
SET status = 'dead_letter', max_attempts = GREATEST(max_attempts, attempt_count),
  lease_token = NULL, lease_expires_at = NULL, leased_by = NULL,
  error_code = $3, error_detail = '{"reason":"max_attempts_exhausted"}'::jsonb,
  completed_at = $4, updated_at = $4, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
`, item.tenantID, item.id, errorCode, dbNow); err != nil {
			return nil, err
		}
	}

	rows, err := tx.QueryContext(ctx, `
SELECT tenant_id::text, id::text, status
FROM agent_worker_task
WHERE ($5 OR tenant_id = $1)
  AND (NOT $5 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
  AND queue_name = $2
  AND (
    (status = 'queued' AND (not_before IS NULL OR not_before <= $4))
    OR (status IN ('leased', 'running') AND lease_expires_at < $4)
  )
  AND attempt_count < max_attempts
ORDER BY priority ASC, created_at ASC, id ASC
LIMIT $3
FOR UPDATE SKIP LOCKED
`, tenantScope, input.QueueName, input.Limit, dbNow, acrossTenants)
	if err != nil {
		return nil, err
	}
	type claimableTask struct {
		tenantID string
		id       string
		status   string
	}
	claimable := make([]claimableTask, 0, input.Limit)
	for rows.Next() {
		var item claimableTask
		if err := rows.Scan(&item.tenantID, &item.id, &item.status); err != nil {
			rows.Close()
			return nil, err
		}
		claimable = append(claimable, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	out := make([]Task, 0, len(claimable))
	for _, item := range claimable {
		if item.status == StatusLeased || item.status == StatusRunning {
			if _, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'lease_expired', completed_at = $3, error_code = 'lease_expired'
WHERE tenant_id = $1 AND task_id = $2::uuid AND completed_at IS NULL
`, item.tenantID, item.id, dbNow); err != nil {
				return nil, err
			}
		}
		token := newLeaseToken()
		expires := dbNow.Add(time.Duration(input.LeaseSeconds) * time.Second)
		row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'leased', attempt_count = attempt_count + 1, not_before = NULL,
  lease_token = $3, lease_expires_at = $4, leased_by = $5,
  worker_service = $6, worker_instance_id = $7, updated_at = $8, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid AND attempt_count < max_attempts
RETURNING `+taskColumns,
			item.tenantID, item.id, token, expires, input.WorkerService+":"+input.WorkerInstanceID,
			input.WorkerService, input.WorkerInstanceID, dbNow)
		task, err := scanTask(row)
		if err != nil {
			return nil, err
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO agent_worker_task_attempt (
  tenant_id, task_id, attempt_no, worker_service, worker_instance_id, lease_token, status, started_at
)
VALUES ($1, $2::uuid, $3, $4, $5, $6, 'leased', $7)
`, item.tenantID, item.id, task.AttemptCount, input.WorkerService, input.WorkerInstanceID, token, dbNow)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *PostgresStore) Heartbeat(ctx context.Context, tenantID string, taskID string, input HeartbeatInput) (Task, error) {
	input = normalizeHeartbeatInput(input)
	if input.LeaseToken == "" || input.WorkerService == "" || input.WorkerInstanceID == "" || (input.State != "" && input.State != StatusRunning) {
		return Task{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, input.LeaseToken, dbNow); err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	if task.WorkerService != input.WorkerService || task.WorkerInstanceID != input.WorkerInstanceID {
		return Task{}, ErrLeaseMismatch
	}
	metadataValue := cloneMap(input.Progress)
	metadataValue["state"] = StatusRunning
	progressJSON, _ := json.Marshal(input.Progress)
	expires := dbNow.Add(time.Duration(input.LeaseSeconds) * time.Second)
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'running', started_at = COALESCE(started_at, $3),
  lease_expires_at = $4, progress = $5, updated_at = $3, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, dbNow, expires, progressJSON)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'running', heartbeat_at = $4
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3
`, tenantID, taskID, task.AttemptCount, dbNow)
	if err != nil {
		return Task{}, err
	}
	if err := upsertWorkerHeartbeat(ctx, tx, tenantID, input.WorkerService, input.WorkerInstanceID, task.QueueName, dbNow, metadataValue); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

func upsertWorkerHeartbeat(ctx context.Context, tx *sql.Tx, tenantID string, workerService string, workerInstanceID string, queueName string, seenAt time.Time, metadataValue map[string]any) error {
	metadata, _ := json.Marshal(metadataValue)
	_, err := tx.ExecContext(ctx, `
INSERT INTO agent_worker_heartbeat (tenant_id, worker_service, worker_instance_id, queue_name, last_seen_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, worker_service, worker_instance_id, queue_name)
DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, metadata = EXCLUDED.metadata
`, tenantID, workerService, workerInstanceID, queueName, seenAt, metadata)
	return err
}

func (s *PostgresStore) Complete(ctx context.Context, tenantID string, taskID string, input CompleteInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := CompleteTaskInTx(ctx, tx, tenantID, taskID, input)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// CompleteTaskInTx validates and completes a Worker Runtime task inside the
// caller's PostgreSQL transaction. Source adapters use it when the task result
// and source-domain facts must commit together.
func CompleteTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, taskID string, input CompleteInput) (Task, error) {
	if tx == nil || input.LeaseToken == "" || input.ResultSchemaVersion == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	hash := payloadHash(input.ResultSchemaVersion, input.Result)
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status == StatusSucceeded {
		if task.LeaseToken != input.LeaseToken {
			return Task{}, ErrLeaseMismatch
		}
		if task.ResultPayloadHash != hash {
			return Task{}, ErrConflict
		}
		return task, nil
	}
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, input.LeaseToken, dbNow); err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	result, _ := json.Marshal(input.Result)
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = 'succeeded', result = $3, result_schema_version = $4,
  result_payload_hash = $5, duration_ms = $6, completed_at = $7, updated_at = $7
  , error_code = NULL, error_detail = '{}', revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, result, input.ResultSchemaVersion, hash, input.DurationMS, dbNow)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	attemptResult, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'succeeded', duration_ms = $4, completed_at = $5, error_code = NULL, error_detail = '{}'
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, input.DurationMS, dbNow)
	if err != nil {
		return Task{}, err
	}
	if rows, rowsErr := attemptResult.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return Task{}, rowsErr
		}
		return Task{}, ErrConflict
	}
	return task, nil
}

func (s *PostgresStore) Fail(ctx context.Context, tenantID string, taskID string, input FailInput) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := FailTaskInTx(ctx, tx, tenantID, taskID, input)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// FailTaskInTx validates and fails (or schedules a retry for) a Worker Runtime
// task inside the caller's PostgreSQL transaction.
func FailTaskInTx(ctx context.Context, tx *sql.Tx, tenantID string, taskID string, input FailInput) (Task, error) {
	if input.LeaseToken == "" || input.ErrorCode == "" || input.DurationMS < 0 {
		return Task{}, ErrInvalidInput
	}
	if tx == nil {
		return Task{}, ErrInvalidInput
	}
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	dbNow, err := databaseNow(ctx, tx)
	if err != nil {
		return Task{}, err
	}
	if err := validateTaskLeaseAt(task, input.LeaseToken, dbNow); err != nil {
		return Task{}, err
	}
	if task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	status := StatusFailed
	var notBefore any
	if input.Retryable && task.AttemptCount < task.MaxAttempts {
		status = StatusQueued
		notBefore = dbNow.Add(retryDelay(task.RetryBackoffSeconds, task.AttemptCount))
	} else if input.Retryable {
		status = StatusDeadLetter
	}
	detail, _ := json.Marshal(input.ErrorDetail)
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = $3, not_before = $4, lease_token = CASE WHEN $3 = 'queued' THEN NULL ELSE lease_token END,
  lease_expires_at = CASE WHEN $3 = 'queued' THEN NULL ELSE lease_expires_at END,
  leased_by = CASE WHEN $3 = 'queued' THEN NULL ELSE leased_by END,
  error_code = $5, error_detail = $6, duration_ms = $7,
  completed_at = CASE WHEN $3 IN ('failed', 'dead_letter') THEN $8::timestamptz ELSE NULL END,
  updated_at = $8, revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, status, notBefore, input.ErrorCode, detail, input.DurationMS, dbNow)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	attemptResult, err := tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt
SET status = 'failed', duration_ms = $4, error_code = $5, error_detail = $6, completed_at = $7
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, input.DurationMS, input.ErrorCode, detail, dbNow)
	if err != nil {
		return Task{}, err
	}
	if rows, rowsErr := attemptResult.RowsAffected(); rowsErr != nil || rows != 1 {
		if rowsErr != nil {
			return Task{}, rowsErr
		}
		return Task{}, ErrConflict
	}
	return task, nil
}

func (s *PostgresStore) Cancel(ctx context.Context, tenantID string, taskID string) (Task, error) {
	return s.setTerminalStatus(ctx, tenantID, taskID, StatusCancelled)
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

// RequeueTaskInTx lets a source coordinator restore source and runtime state
// together. The caller owns the transaction and source lock.
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

func (s *PostgresStore) AuthorizeLease(ctx context.Context, taskID string, leaseToken string, workerService string, workerInstanceID string, now time.Time) (Task, error) {
	if taskID == "" || leaseToken == "" || workerService == "" || workerInstanceID == "" {
		return Task{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
SELECT `+taskColumns+`
FROM agent_worker_task
WHERE id=$1::uuid
  AND tenant_id IN (SELECT id FROM tenant WHERE status='active' AND deleted_at IS NULL)
  AND lease_token=$2
  AND worker_service=$3
  AND worker_instance_id=$4
  AND status IN ('leased','running')
  AND lease_expires_at>$5
`, taskID, leaseToken, workerService, workerInstanceID, now)
	task, err := scanTask(row)
	if errors.Is(err, ErrNotFound) {
		return Task{}, ErrLeaseMismatch
	}
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

func (s *PostgresStore) Metrics(ctx context.Context, tenantID string) (Metrics, error) {
	return s.metrics(ctx, tenantID, false)
}

func (s *PostgresStore) MetricsAcrossTenants(ctx context.Context, platformTenantID string) (Metrics, error) {
	return s.metrics(ctx, platformTenantID, true)
}

func (s *PostgresStore) metrics(ctx context.Context, tenantID string, acrossTenants bool) (Metrics, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT queue_name,
  count(*) FILTER (WHERE status = 'queued'),
  count(*) FILTER (WHERE status = 'leased'),
  count(*) FILTER (WHERE status = 'running'),
  count(*) FILTER (WHERE status = 'succeeded'),
  count(*) FILTER (WHERE status = 'failed'),
  count(*) FILTER (WHERE status = 'dead_letter'),
  count(*) FILTER (WHERE status = 'cancelled'),
  count(*) FILTER (WHERE status IN ('failed', 'dead_letter') AND updated_at >= now() - interval '1 hour'),
  COALESCE(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) FILTER (WHERE duration_ms IS NOT NULL), 0)::int
FROM agent_worker_task
WHERE ($2 OR tenant_id = $1)
  AND (NOT $2 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
GROUP BY queue_name
ORDER BY queue_name
`, tenantID, acrossTenants)
	if err != nil {
		return Metrics{}, err
	}
	out := Metrics{Queues: []QueueMetrics{}, Workers: []WorkerHeartbeat{}}
	for rows.Next() {
		var metric QueueMetrics
		if err := rows.Scan(&metric.QueueName, &metric.Queued, &metric.Leased, &metric.Running, &metric.Succeeded, &metric.Failed, &metric.DeadLetter, &metric.Cancelled, &metric.FailedLastHour, &metric.P95DurationMS); err != nil {
			rows.Close()
			return Metrics{}, err
		}
		out.Queues = append(out.Queues, metric)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Metrics{}, err
	}
	if err := rows.Close(); err != nil {
		return Metrics{}, err
	}
	for index := range out.Queues {
		if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM agent_worker_task_attempt a
JOIN agent_worker_task t ON t.tenant_id = a.tenant_id AND t.id = a.task_id
WHERE ($3 OR a.tenant_id = $1) AND t.queue_name = $2 AND a.status = 'failed'
  AND (NOT $3 OR a.tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
  AND a.completed_at >= now() - interval '1 hour' AND a.attempt_no < t.max_attempts
`, tenantID, out.Queues[index].QueueName, acrossTenants).Scan(&out.Queues[index].RetryLastHour); err != nil {
			return Metrics{}, err
		}
	}
	workerRows, err := s.db.QueryContext(ctx, `
SELECT tenant_id::text, worker_service, worker_instance_id, queue_name, last_seen_at, metadata
FROM agent_worker_heartbeat
WHERE ($2 OR tenant_id = $1)
  AND (NOT $2 OR tenant_id IN (
    SELECT id FROM tenant WHERE status = 'active' AND deleted_at IS NULL
  ))
ORDER BY last_seen_at DESC
`, tenantID, acrossTenants)
	if err != nil {
		return Metrics{}, err
	}
	defer workerRows.Close()
	for workerRows.Next() {
		var worker WorkerHeartbeat
		var metadata []byte
		if err := workerRows.Scan(&worker.TenantID, &worker.WorkerService, &worker.WorkerInstanceID, &worker.QueueName, &worker.LastSeenAt, &metadata); err != nil {
			return Metrics{}, err
		}
		_ = json.Unmarshal(metadata, &worker.Metadata)
		out.Workers = append(out.Workers, worker)
	}
	return out, workerRows.Err()
}

func (s *PostgresStore) setTerminalStatus(ctx context.Context, tenantID string, taskID string, status string) (Task, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusQueued && task.Status != StatusLeased && task.Status != StatusRunning {
		return Task{}, ErrInvalidTransition
	}
	row := tx.QueryRowContext(ctx, `
UPDATE agent_worker_task
SET status = $3, cancelled_at = now(), completed_at = now(), updated_at = now(), revision = revision + 1
WHERE tenant_id = $1 AND id = $2::uuid
RETURNING `+taskColumns, tenantID, taskID, status)
	task, err = scanTask(row)
	if err != nil {
		return Task{}, err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE agent_worker_task_attempt SET status = $4, completed_at = now(), error_code = $4
WHERE tenant_id = $1 AND task_id = $2::uuid AND attempt_no = $3 AND completed_at IS NULL
`, tenantID, taskID, task.AttemptCount, status)
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

func getTaskForUpdate(ctx context.Context, tx *sql.Tx, tenantID string, taskID string) (Task, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM agent_worker_task WHERE tenant_id = $1 AND id = $2::uuid FOR UPDATE`, tenantID, taskID)
	return scanTask(row)
}

// 租约判断和到期时间使用同一事务的数据库时钟，避免 API 实例时钟偏差影响接管。
func databaseNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT transaction_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

func validateTaskLeaseAt(task Task, token string, now time.Time) error {
	if token == "" || task.LeaseToken != token {
		return ErrLeaseMismatch
	}
	if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.Before(now) {
		return ErrLeaseExpired
	}
	return nil
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

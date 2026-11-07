package workerruntime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

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
	// 进程所属执行器不为发布心跳而冒充租户。外部平台 worker 仍提供真实租户 ID。
	if heartbeatTenantID != "" {
		if err := upsertWorkerHeartbeat(ctx, tx, WorkerHeartbeat{
			TenantID: heartbeatTenantID, WorkerService: input.WorkerService, WorkerInstanceID: input.WorkerInstanceID,
			QueueName: input.QueueName, LastSeenAt: dbNow, Metadata: map[string]any{"state": "polling"},
		}); err != nil {
			return nil, err
		}
	} else if !acrossTenants {
		return nil, ErrInvalidInput
	}
	var tenantScope any = heartbeatTenantID
	if acrossTenants && heartbeatTenantID == "" {
		// 即使 OR 的跨租户分支为 true，传入空字符串仍使 PostgreSQL 将 $1 转为 uuid。
		// NULL 让进程所属执行器保持不绑定租户，并避免无效 uuid 转换。
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
	task, err := getTaskForUpdate(ctx, tx, tenantID, taskID)
	if err != nil {
		return Task{}, err
	}
	// 等待任务行锁期间租约可能到期；取得锁后才采样本次心跳的实际数据库时间。
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
	if err := upsertWorkerHeartbeat(ctx, tx, WorkerHeartbeat{
		TenantID: tenantID, WorkerService: input.WorkerService, WorkerInstanceID: input.WorkerInstanceID,
		QueueName: task.QueueName, LastSeenAt: dbNow, Metadata: metadataValue,
	}); err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// 心跳时间由调用方的数据库时钟提供，与任务租约使用同一时刻；提交或回滚由该事务负责。
func upsertWorkerHeartbeat(ctx context.Context, tx *sql.Tx, heartbeat WorkerHeartbeat) error {
	metadata, _ := json.Marshal(heartbeat.Metadata)
	_, err := tx.ExecContext(ctx, `
INSERT INTO agent_worker_heartbeat (tenant_id, worker_service, worker_instance_id, queue_name, last_seen_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (tenant_id, worker_service, worker_instance_id, queue_name)
DO UPDATE SET last_seen_at = EXCLUDED.last_seen_at, metadata = EXCLUDED.metadata
`, heartbeat.TenantID, heartbeat.WorkerService, heartbeat.WorkerInstanceID, heartbeat.QueueName, heartbeat.LastSeenAt, metadata)
	return err
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

func getTaskForUpdate(ctx context.Context, tx *sql.Tx, tenantID string, taskID string) (Task, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM agent_worker_task WHERE tenant_id = $1 AND id = $2::uuid FOR UPDATE`, tenantID, taskID)
	return scanTask(row)
}

// 租约判断和新到期时间共用一次实际数据库时钟采样；验证旧租约须在取得任务锁后读取。
// 领取轮询的采样用于筛选候选，不代表已验证某个旧租约。
// transaction_timestamp 固定在事务开始，不能判断等锁结束后的租约是否已经到期。
func databaseNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, err
	}
	return now.UTC(), nil
}

func validateTaskLeaseAt(task Task, token string, now time.Time) error {
	if token == "" || task.LeaseToken != token {
		return ErrLeaseMismatch
	}
	// 与执行能力授权保持同一排他边界；恰好到期时已经没有继续写入的租约。
	if task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(now) {
		return ErrLeaseExpired
	}
	return nil
}

package workerruntime

import (
	"context"
	"errors"
	"time"
)

const (
	StatusQueued     = "queued"
	StatusLeased     = "leased"
	StatusRunning    = "running"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusDeadLetter = "dead_letter"
	StatusCancelled  = "cancelled"
)

var (
	ErrNotFound          = errors.New("worker task not found")
	ErrInvalidInput      = errors.New("invalid worker task input")
	ErrInvalidTransition = errors.New("invalid worker task transition")
	ErrLeaseMismatch     = errors.New("worker task lease mismatch")
	ErrLeaseExpired      = errors.New("worker task lease expired")
	ErrConflict          = errors.New("worker task result conflict")
	ErrSourceActivation  = errors.New("worker task requires source activation")
)

type Task struct {
	ID                   string         `json:"id"`
	TenantID             string         `json:"tenant_id"`
	TaskType             string         `json:"task_type"`
	QueueName            string         `json:"queue_name"`
	SourceType           string         `json:"source_type"`
	SourceID             string         `json:"source_id"`
	Status               string         `json:"status"`
	// Priority 数值越小越优先；同优先级按创建时间和 ID 排序。
	Priority             int            `json:"priority"`
	Payload              map[string]any `json:"payload"`
	PayloadSchemaVersion string         `json:"payload_schema_version"`
	Result               map[string]any `json:"result,omitempty"`
	Progress             map[string]any `json:"progress,omitempty"`
	ResultSchemaVersion  string         `json:"result_schema_version,omitempty"`
	ResultPayloadHash    string         `json:"-"`
	IdempotencyKey       string         `json:"idempotency_key"`
	DedupeKey            string         `json:"dedupe_key,omitempty"`
	MaxAttempts          int            `json:"max_attempts"`
	AttemptCount         int            `json:"attempt_count"`
	RetryBackoffSeconds  int            `json:"retry_backoff_seconds"`
	NotBefore            *time.Time     `json:"not_before,omitempty"`
	LeaseToken           string         `json:"lease_token,omitempty"`
	LeaseExpiresAt       *time.Time     `json:"lease_expires_at,omitempty"`
	LeasedBy             string         `json:"leased_by,omitempty"`
	WorkerService        string         `json:"worker_service,omitempty"`
	WorkerInstanceID     string         `json:"worker_instance_id,omitempty"`
	StartedAt            *time.Time     `json:"started_at,omitempty"`
	CompletedAt          *time.Time     `json:"completed_at,omitempty"`
	CancelledAt          *time.Time     `json:"cancelled_at,omitempty"`
	DurationMS           int            `json:"duration_ms,omitempty"`
	ErrorCode            string         `json:"error_code,omitempty"`
	ErrorDetail          map[string]any `json:"error_detail,omitempty"`
	Revision             int64          `json:"revision"`
	CreatedBy            string         `json:"created_by,omitempty"`
	CreatedAt            time.Time      `json:"created_at"`
	UpdatedAt            time.Time      `json:"updated_at"`
	Attempts             []Attempt      `json:"attempts,omitempty"`
}

type Attempt struct {
	ID               string         `json:"id"`
	TenantID         string         `json:"tenant_id"`
	TaskID           string         `json:"task_id"`
	AttemptNo        int            `json:"attempt_no"`
	WorkerService    string         `json:"worker_service"`
	WorkerInstanceID string         `json:"worker_instance_id"`
	LeaseToken       string         `json:"-"`
	Status           string         `json:"status"`
	StartedAt        time.Time      `json:"started_at"`
	HeartbeatAt      *time.Time     `json:"heartbeat_at,omitempty"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	DurationMS       int            `json:"duration_ms,omitempty"`
	ErrorCode        string         `json:"error_code,omitempty"`
	ErrorDetail      map[string]any `json:"error_detail,omitempty"`
}

type WorkerHeartbeat struct {
	TenantID         string         `json:"tenant_id"`
	WorkerService    string         `json:"worker_service"`
	WorkerInstanceID string         `json:"worker_instance_id"`
	QueueName        string         `json:"queue_name"`
	LastSeenAt       time.Time      `json:"last_seen_at"`
	Metadata         map[string]any `json:"metadata"`
}

type CreateTaskInput struct {
	TaskType             string         `json:"task_type"`
	QueueName            string         `json:"queue_name"`
	SourceType           string         `json:"source_type"`
	SourceID             string         `json:"source_id"`
	Priority             int            `json:"priority"`
	Payload              map[string]any `json:"payload"`
	PayloadSchemaVersion string         `json:"payload_schema_version"`
	IdempotencyKey       string         `json:"idempotency_key"`
	DedupeKey            string         `json:"dedupe_key"`
	MaxAttempts          int            `json:"max_attempts"`
	RetryBackoffSeconds  int            `json:"retry_backoff_seconds"`
}

type ClaimInput struct {
	QueueName        string `json:"queue_name"`
	WorkerService    string `json:"worker_service"`
	WorkerInstanceID string `json:"worker_instance_id"`
	Limit            int    `json:"limit"`
	LeaseSeconds     int    `json:"lease_seconds"`
}

type HeartbeatInput struct {
	LeaseToken       string         `json:"lease_token"`
	WorkerService    string         `json:"worker_service"`
	WorkerInstanceID string         `json:"worker_instance_id"`
	State            string         `json:"state"`
	Progress         map[string]any `json:"progress"`
	LeaseSeconds     int            `json:"lease_seconds"`
}

type CompleteInput struct {
	LeaseToken          string         `json:"lease_token"`
	ResultSchemaVersion string         `json:"result_schema_version"`
	Result              map[string]any `json:"result"`
	DurationMS          int            `json:"duration_ms"`
}

type FailInput struct {
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type QueueMetrics struct {
	QueueName      string `json:"queue_name"`
	Queued         int    `json:"queued"`
	Leased         int    `json:"leased"`
	Running        int    `json:"running"`
	Succeeded      int    `json:"succeeded"`
	Failed         int    `json:"failed"`
	DeadLetter     int    `json:"dead_letter"`
	Cancelled      int    `json:"cancelled"`
	FailedLastHour int    `json:"failed_last_hour"`
	RetryLastHour  int    `json:"retry_last_hour"`
	P95DurationMS  int    `json:"p95_duration_ms"`
}

type Metrics struct {
	Queues  []QueueMetrics    `json:"queues"`
	Workers []WorkerHeartbeat `json:"workers"`
}

type Store interface {
	CreateTask(ctx context.Context, tenantID string, actorID string, input CreateTaskInput) (Task, error)
	Claim(ctx context.Context, tenantID string, input ClaimInput) ([]Task, error)
	ClaimAcrossTenants(ctx context.Context, platformTenantID string, input ClaimInput) ([]Task, error)
	Heartbeat(ctx context.Context, tenantID string, taskID string, input HeartbeatInput) (Task, error)
	Complete(ctx context.Context, tenantID string, taskID string, input CompleteInput) (Task, error)
	Fail(ctx context.Context, tenantID string, taskID string, input FailInput) (Task, error)
	Cancel(ctx context.Context, tenantID string, taskID string) (Task, error)
	Requeue(ctx context.Context, tenantID string, taskID string) (Task, error)
	Get(ctx context.Context, tenantID string, taskID string) (Task, error)
	AuthorizeLease(ctx context.Context, taskID string, leaseToken string, workerService string, workerInstanceID string, now time.Time) (Task, error)
	GetBySource(ctx context.Context, tenantID string, sourceType string, sourceID string) (Task, error)
	Metrics(ctx context.Context, tenantID string) (Metrics, error)
	MetricsAcrossTenants(ctx context.Context, platformTenantID string) (Metrics, error)
}

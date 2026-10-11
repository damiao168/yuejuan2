package imagequality

import (
	"context"
	"errors"
	"time"
)

const (
	QualityUnchecked = "unchecked"
	QualityPassed    = "passed"
	QualityReview    = "review"
	QualityFailed    = "failed"

	ProcessingPending        = "pending"
	ProcessingProcessing     = "processing"
	ProcessingCompleted      = "completed"
	ProcessingRetryableError = "retryable_error"
	ProcessingTerminalError  = "terminal_error"
)

var (
	ErrNotFound          = errors.New("image quality run not found")
	ErrInvalidInput      = errors.New("invalid image quality input")
	ErrInvalidTransition = errors.New("invalid image quality transition")
	ErrLeaseExpired      = errors.New("image quality lease expired")
	ErrLeaseMismatch     = errors.New("image quality lease mismatch")
	ErrConflict          = errors.New("image quality result conflict")
)

type Profile struct {
	Name                string `json:"name"`
	Version             string `json:"version"`
	ConfigHash          string `json:"config_hash"`
	MetricSchemaVersion string `json:"metric_schema_version"`
	ReportSchemaVersion string `json:"report_schema_version"`
}

func DefaultProfile() Profile {
	return Profile{
		Name:                "opencv-default",
		Version:             "v2",
		ConfigHash:          "sha256:opencv-default-v2-local-focus-hard-gates",
		MetricSchemaVersion: "image-quality-metrics-v2",
		ReportSchemaVersion: "image-quality-report-v2",
	}
}

type PageSource struct {
	SubmissionPageID  string `json:"submission_page_id"`
	PageNo            int    `json:"page_no"`
	SourceFileAssetID string `json:"source_file_asset_id"`
	SourceSHA256      string `json:"source_sha256"`
	DownloadURL       string `json:"download_url"`
}

type CreateRunsInput struct {
	SubmissionID string       `json:"submission_id"`
	Profile      Profile      `json:"profile"`
	Pages        []PageSource `json:"pages"`
}

type Issue struct {
	Code       string         `json:"code"`
	Severity   string         `json:"severity"`
	Metric     string         `json:"metric,omitempty"`
	Observed   any            `json:"observed,omitempty"`
	Threshold  any            `json:"threshold,omitempty"`
	RuleID     string         `json:"rule_id,omitempty"`
	Action     string         `json:"action"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

type Run struct {
	ID                     string         `json:"id"`
	TenantID               string         `json:"tenant_id"`
	SubmissionID           string         `json:"submission_id"`
	SubmissionPageID       string         `json:"submission_page_id"`
	PageNo                 int            `json:"page_no"`
	SourceFileAssetID      string         `json:"source_file_asset_id"`
	SourceSHA256           string         `json:"source_sha256"`
	DownloadURL            string         `json:"download_url,omitempty"`
	NormalizedFileAssetID  string         `json:"normalized_file_asset_id,omitempty"`
	ProcessingStatus       string         `json:"processing_status"`
	QualityStatus          string         `json:"quality_status,omitempty"`
	ProfileName            string         `json:"profile_name"`
	ProfileVersion         string         `json:"profile_version"`
	ProfileConfigHash      string         `json:"profile_config_hash"`
	MetricSchemaVersion    string         `json:"metric_schema_version"`
	ReportSchemaVersion    string         `json:"report_schema_version"`
	QualityReport          map[string]any `json:"quality_report"`
	QualityIssues          []Issue        `json:"quality_issues"`
	NormalizationTransform map[string]any `json:"normalization_transform"`
	WorkerService          string         `json:"worker_service,omitempty"`
	WorkerInstanceID       string         `json:"worker_instance_id,omitempty"`
	AttemptNo              int            `json:"attempt_no"`
	ResultVersion          string         `json:"result_version,omitempty"`
	ResultPayloadHash      string         `json:"-"`
	LeaseToken             string         `json:"-"`
	LeaseExpiresAt         *time.Time     `json:"lease_expires_at,omitempty"`
	StartedAt              *time.Time     `json:"started_at,omitempty"`
	CompletedAt            *time.Time     `json:"completed_at,omitempty"`
	DurationMS             int            `json:"duration_ms,omitempty"`
	ErrorCode              string         `json:"error_code,omitempty"`
	ErrorDetail            map[string]any `json:"error_detail"`
	CreatedAt              time.Time      `json:"created_at"`
}

type ClaimInput struct {
	WorkerInstanceID string `json:"worker_instance_id"`
	Limit            int    `json:"limit"`
	LeaseSeconds     int    `json:"lease_seconds"`
}

type ClaimedJob struct {
	RuntimeTaskID     string    `json:"runtime_task_id,omitempty"`
	ExamID            string    `json:"exam_id"`
	RunID             string    `json:"run_id"`
	SubmissionID      string    `json:"submission_id"`
	SubmissionPageID  string    `json:"submission_page_id"`
	PageNo            int       `json:"page_no"`
	SourceFileAssetID string    `json:"source_file_asset_id"`
	SourceSHA256      string    `json:"source_sha256"`
	DownloadURL       string    `json:"download_url"`
	LeaseToken        string    `json:"lease_token"`
	LeaseExpiresAt    time.Time `json:"lease_expires_at"`
	AttemptNo         int       `json:"attempt_no"`
	Profile           Profile   `json:"profile"`
}

type ResultInput struct {
	LeaseToken             string         `json:"lease_token"`
	AttemptNo              int            `json:"attempt_no"`
	ResultVersion          string         `json:"result_version"`
	DurationMS             int            `json:"duration_ms"`
	ProcessingStatus       string         `json:"processing_status"`
	QualityStatus          string         `json:"quality_status"`
	NormalizedFileAssetID  string         `json:"normalized_file_asset_id"`
	QualityReport          map[string]any `json:"quality_report"`
	QualityIssues          []Issue        `json:"quality_issues"`
	NormalizationTransform map[string]any `json:"normalization_transform"`
	ErrorCode              string         `json:"error_code"`
	ErrorDetail            map[string]any `json:"error_detail"`
}

type Store interface {
	CreateRuns(ctx context.Context, tenantID string, input CreateRunsInput) ([]Run, error)
	ListRunsForPage(ctx context.Context, tenantID string, submissionPageID string) ([]Run, error)
	Claim(ctx context.Context, tenantID string, input ClaimInput) ([]ClaimedJob, error)
	LeaseRun(ctx context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error)
	RenewLease(ctx context.Context, tenantID string, runID string, workerInstanceID string, leaseToken string, leaseExpiresAt time.Time, attemptNo int) (Run, error)
	CompleteRun(ctx context.Context, tenantID string, runID string, input ResultInput) (Run, error)
	GetRun(ctx context.Context, tenantID string, runID string) (Run, error)
}

// TransactionalStore 由生产 PostgreSQL 存储实现，负责领域写入和 Worker Runtime 写入须共同提交的命令。
type TransactionalStore interface {
	Store
	CreateRunsWithTasks(ctx context.Context, tenantID, actorID string, input CreateRunsInput) ([]Run, error)
	SubmitResultCommand(ctx context.Context, tenantID, actorID, runID string, input ResultInput) (Run, error)
}

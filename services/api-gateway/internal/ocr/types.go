package ocr

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("ocr task not found")
	ErrInvalidInput        = errors.New("invalid ocr input")
	ErrInvalidTransition   = errors.New("invalid ocr task status transition")
	ErrSubmissionNotReady  = errors.New("submission is not ready for ocr")
	ErrResultConflict      = errors.New("ocr result conflicts with completed task")
	ErrIdempotencyConflict = errors.New("ocr idempotency key conflicts with another request")
)

type Task struct {
	ID                    string     `json:"id"`
	TenantID              string     `json:"tenant_id"`
	SubmissionID          string     `json:"submission_id"`
	Status                string     `json:"status"`
	Engine                string     `json:"engine"`
	EngineVersion         string     `json:"engine_version"`
	ModelVersion          string     `json:"model_version,omitempty"`
	ConfigHash            string     `json:"config_hash,omitempty"`
	InputHash             string     `json:"input_hash,omitempty"`
	DurationMS            int        `json:"duration_ms,omitempty"`
	WorkerID              string     `json:"worker_id,omitempty"`
	AttemptCount          int        `json:"attempt_count"`
	MinConfidence         float64    `json:"min_confidence"`
	ResultCount           int        `json:"result_count"`
	RequiresHumanReview   bool       `json:"requires_human_review"`
	ErrorMessage          string     `json:"error_message,omitempty"`
	RequestedBy           string     `json:"requested_by"`
	StartedAt             *time.Time `json:"started_at,omitempty"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	Results               []Result   `json:"results,omitempty"`
	CompletionPayloadHash string     `json:"-"`
}

type Result struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id"`
	TaskID            string    `json:"ocr_task_id"`
	SubmissionID      string    `json:"submission_id"`
	SubmissionPageID  string    `json:"submission_page_id"`
	Text              string    `json:"text"`
	BBox              []float64 `json:"bbox"`
	Confidence        float64   `json:"confidence"`
	OCREngine         string    `json:"ocr_engine"`
	OCRVersion        string    `json:"ocr_version"`
	ModelVersion      string    `json:"model_version,omitempty"`
	ConfigHash        string    `json:"config_hash,omitempty"`
	InputHash         string    `json:"input_hash,omitempty"`
	PreprocessProfile string    `json:"preprocess_profile,omitempty"`
	SourceImageFileID string    `json:"source_image_file_id,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type CreateTaskInput struct {
	Engine             string   `json:"engine"`
	EngineVersion      string   `json:"engine_version"`
	MinConfidence      float64  `json:"min_confidence"`
	IdempotencyKey     string   `json:"idempotency_key"`
	SourceFileAssetIDs []string `json:"-"`
}

type ResultInput struct {
	SubmissionPageID  string    `json:"submission_page_id"`
	Text              string    `json:"text"`
	BBox              []float64 `json:"bbox"`
	Confidence        float64   `json:"confidence"`
	SourceImageFileID string    `json:"source_image_file_id"`
}

type CompleteTaskInput struct {
	Results           []ResultInput `json:"results"`
	ModelVersion      string        `json:"model_version"`
	ConfigHash        string        `json:"config_hash"`
	InputHash         string        `json:"input_hash"`
	DurationMS        int           `json:"duration_ms"`
	WorkerID          string        `json:"worker_id"`
	PreprocessProfile string        `json:"preprocess_profile"`
	RuntimeTaskID     string        `json:"runtime_task_id"`
	RuntimeLeaseToken string        `json:"runtime_lease_token"`
}

type FailTaskInput struct {
	ErrorMessage      string `json:"error_message"`
	RuntimeTaskID     string `json:"runtime_task_id"`
	RuntimeLeaseToken string `json:"runtime_lease_token"`
	Retryable         bool   `json:"retryable"`
}

type TaskListFilter struct {
	Limit           int
	CursorCreatedAt time.Time
	CursorID        string
}

type Store interface {
	CreateTask(ctx context.Context, tenantID string, submissionID string, actorID string, input CreateTaskInput) (Task, error)
	ListPending(ctx context.Context, tenantID string, limit int) ([]Task, error)
	ListBySubmission(ctx context.Context, tenantID string, submissionID string, filter TaskListFilter) ([]Task, error)
	GetTask(ctx context.Context, tenantID string, id string) (Task, error)
	StartTask(ctx context.Context, tenantID string, id string) (Task, error)
	CompleteTask(ctx context.Context, tenantID string, id string, input CompleteTaskInput) (Task, error)
	FailTask(ctx context.Context, tenantID string, id string, errorMessage string) (Task, error)
}

type Queue interface {
	Enqueue(ctx context.Context, task Task) error
}

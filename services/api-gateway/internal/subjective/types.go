package subjective

import (
	"context"
	"errors"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

const (
	MockGraderType = "mock_llm_subjective"
	LLMGraderType  = "llm_subjective"
)

var (
	ErrNotFound                = errors.New("subjective grading resource not found")
	ErrInvalidInput            = errors.New("invalid subjective grading input")
	ErrUnsupportedQuestionType = errors.New("unsupported question type for subjective grading")
	ErrAnswerMissing           = errors.New("answer segment has no recorded answer")
	ErrRubricMissing           = errors.New("question has no rubric")
	ErrInvalidModelOutput      = errors.New("invalid model output")
	ErrIdempotencyConflict     = errors.New("subjective grading idempotency conflict")
	ErrMathHumanReviewRequired = errors.New("math evidence requires human review")
)

const (
	RunQueued     = "queued"
	RunProcessing = "processing"
	RunSucceeded  = "succeeded"
	RunFailed     = "failed"
	RunConflict   = "conflict"
)

const (
	AgentRoleSingle   = "single"
	AgentRolePrimaryA = "primary_a"
	AgentRolePrimaryB = "primary_b"
	AgentRoleArbiter  = "arbiter"
)

type ModelPolicy struct {
	ModelVersion  string  `json:"model_version"`
	PromptVersion string  `json:"prompt_version"`
	MinConfidence float64 `json:"min_confidence"`
}

type GradeRequest struct {
	ModelPolicy    ModelPolicy `json:"model_policy"`
	IdempotencyKey string      `json:"idempotency_key,omitempty"`
}

type Context struct {
	SegmentID          string
	AssessmentSnapshot assessment.ExamQuestionSnapshot
	AnswerVersion      string
	Subject            string
	GradeLevel         string
	Question           paper.Question
	Rubric             paper.Rubric
	AnswerText         string
	AnswerImageRef     map[string]any
	OCRConfidence      *float64
	AnswerCreatedAt    time.Time
	MathEvidence       *MathEvidenceContext
}

type AdapterInput struct {
	RequestID          string                          `json:"request_id"`
	SegmentID          string                          `json:"answer_segment_id"`
	Subject            string                          `json:"subject"`
	GradeLevel         string                          `json:"grade_level"`
	AgentRole          string                          `json:"agent_role"`
	Question           paper.Question                  `json:"question"`
	Rubric             paper.Rubric                    `json:"rubric"`
	AnswerText         string                          `json:"answer_text"`
	AnswerImageRef     map[string]any                  `json:"answer_image_ref"`
	OCRConfidence      *float64                        `json:"ocr_confidence,omitempty"`
	AssessmentSnapshot assessment.ExamQuestionSnapshot `json:"assessment_snapshot"`
	ModelPolicy        ModelPolicy                     `json:"model_policy"`
	PromptGuard        PromptGuard                     `json:"prompt_guard"`
	// OutputConstraint is produced by the admission gate.  It travels with the
	// request to every adapter so a provider cannot reinterpret an admitted
	// call as authority to issue an independently final score.
	OutputConstraint aieligibility.OutputConstraint `json:"output_constraint"`
	MathEvidence     *MathEvidenceContext           `json:"math_evidence,omitempty"`
	// ActiveCrop is verified locally and encoded only by the v2 adapter. It is
	// never included by generic JSON serialization or persisted with a run.
	ActiveCrop *ResolvedActiveCrop `json:"-"`
}

type PromptGuard struct {
	StudentAnswerIsUntrusted bool     `json:"student_answer_is_untrusted"`
	SuspectedInjection       bool     `json:"suspected_injection"`
	Signals                  []string `json:"signals"`
	Instruction              string   `json:"instruction"`
}

type AdapterOutput struct {
	SchemaVersion                string                         `json:"schema_version,omitempty"`
	RequestID                    string                         `json:"request_id"`
	SuggestedScore               float64                        `json:"suggested_score"`
	Confidence                   float64                        `json:"confidence"`
	MatchedPoints                []grading.PointResult          `json:"matched_points"`
	MissingPoints                []grading.PointResult          `json:"missing_points"`
	Evidence                     []grading.Evidence             `json:"evidence"`
	RiskFlags                    []string                       `json:"risk_flags"`
	NeedsHumanReview             bool                           `json:"needs_human_review"`
	StudentFeedback              string                         `json:"student_feedback"`
	TeacherNote                  string                         `json:"teacher_note"`
	ModelVersion                 string                         `json:"model_version"`
	PromptVersion                string                         `json:"prompt_version"`
	RubricVersion                string                         `json:"rubric_version"`
	DeliveryMode                 string                         `json:"delivery_mode"`
	CapabilityProfile            string                         `json:"capability_profile"`
	Telemetry                    AdapterTelemetry               `json:"telemetry"`
	RawOutput                    map[string]any                 `json:"raw_output"`
	Mock                         bool                           `json:"mock"`
	MathCandidates               []MathCriterionCandidate       `json:"math_candidates,omitempty"`
	AlternativeSolutionCandidate bool                           `json:"alternative_solution_candidate,omitempty"`
	MathScore                    *mathunderstanding.RubricScore `json:"math_score,omitempty"`
}

type AdapterTelemetry struct {
	Adapter         string          `json:"adapter"`
	Provider        string          `json:"provider"`
	Deployment      string          `json:"deployment"`
	Region          string          `json:"region"`
	Attempts        int             `json:"attempts"`
	RepairAttempted bool            `json:"repair_attempted"`
	PriorErrorCodes []string        `json:"prior_error_codes"`
	ElapsedMS       int64           `json:"elapsed_ms"`
	Usage           ModelTokenUsage `json:"usage,omitempty"`
}

type ModelTokenUsage struct {
	InputTokens       int64 `json:"input_tokens,omitempty"`
	CachedInputTokens int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens      int64 `json:"output_tokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoning_tokens,omitempty"`
	TotalTokens       int64 `json:"total_tokens,omitempty"`
}

type Grade struct {
	ID                     string                `json:"id"`
	TenantID               string                `json:"tenant_id"`
	AnswerSegmentID        string                `json:"answer_segment_id"`
	QuestionID             string                `json:"question_id"`
	QuestionNo             string                `json:"question_no"`
	QuestionType           string                `json:"question_type"`
	AnswerVersion          string                `json:"answer_version"`
	GraderType             string                `json:"grader_type"`
	ModelVersion           string                `json:"model_version"`
	ModelConfigID          string                `json:"model_config_id,omitempty"`
	ModelName              string                `json:"model_name,omitempty"`
	PromptVersion          string                `json:"prompt_version"`
	RubricVersion          string                `json:"rubric_version"`
	DeliveryMode           string                `json:"delivery_mode"`
	CapabilityProfile      string                `json:"capability_profile"`
	AdapterRequestID       string                `json:"adapter_request_id"`
	RunID                  string                `json:"subjective_grading_run_id,omitempty"`
	AdapterName            string                `json:"adapter_name"`
	ProviderKey            string                `json:"provider_key"`
	DeploymentKey          string                `json:"deployment_key"`
	DeploymentRegion       string                `json:"deployment_region"`
	AdapterAttempts        int                   `json:"adapter_attempts"`
	AdapterLatencyMS       int64                 `json:"adapter_latency_ms"`
	AdapterRepairAttempted bool                  `json:"adapter_repair_attempted"`
	InputTokens            int64                 `json:"input_tokens,omitempty"`
	CachedInputTokens      int64                 `json:"cached_input_tokens,omitempty"`
	OutputTokens           int64                 `json:"output_tokens,omitempty"`
	ReasoningTokens        int64                 `json:"reasoning_tokens,omitempty"`
	TotalTokens            int64                 `json:"total_tokens,omitempty"`
	SuggestedScore         float64               `json:"suggested_score"`
	MaxScore               float64               `json:"max_score"`
	Confidence             float64               `json:"confidence"`
	MatchedPoints          []grading.PointResult `json:"matched_points"`
	MissingPoints          []grading.PointResult `json:"missing_points"`
	Evidence               []grading.Evidence    `json:"evidence"`
	RiskFlags              []string              `json:"risk_flags"`
	NeedsHumanReview       bool                  `json:"needs_human_review"`
	StudentFeedback        string                `json:"student_feedback"`
	TeacherNote            string                `json:"teacher_note"`
	Mock                   bool                  `json:"mock"`
	Status                 string                `json:"status"`
	FailureReason          string                `json:"failure_reason,omitempty"`
	RawOutput              map[string]any        `json:"raw_output"`
	MathArtifactID         string                `json:"math_artifact_id,omitempty"`
	MathArtifactVersion    int64                 `json:"math_artifact_version,omitempty"`
	MathCorrectionRevision int64                 `json:"math_correction_revision,omitempty"`
	MathScoringVersion     string                `json:"math_scoring_version,omitempty"`
	CreatedBy              string                `json:"created_by"`
	CreatedAt              time.Time             `json:"created_at"`
}

type GradingRun struct {
	ID                     string     `json:"id"`
	TenantID               string     `json:"tenant_id"`
	AnswerSegmentID        string     `json:"answer_segment_id"`
	BatchID                string     `json:"batch_id,omitempty"`
	AnswerVersion          string     `json:"answer_version"`
	QuestionID             string     `json:"question_id"`
	RubricVersion          string     `json:"rubric_version"`
	ModelVersion           string     `json:"model_version"`
	PromptVersion          string     `json:"prompt_version"`
	MinConfidence          float64    `json:"min_confidence"`
	RequestID              string     `json:"request_id"`
	PanelID                string     `json:"panel_id,omitempty"`
	AgentRole              string     `json:"agent_role"`
	MathArtifactID         string     `json:"math_artifact_id,omitempty"`
	MathArtifactVersion    int64      `json:"math_artifact_version,omitempty"`
	MathCorrectionRevision int64      `json:"math_correction_revision,omitempty"`
	MathScoringVersion     string     `json:"math_scoring_version,omitempty"`
	Status                 string     `json:"status"`
	AttemptCount           int        `json:"attempt_count"`
	GradeID                string     `json:"grade_id,omitempty"`
	ErrorCode              string     `json:"error_code,omitempty"`
	StartedAt              *time.Time `json:"started_at,omitempty"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

type CreateRunInput struct {
	AnswerSegmentID        string
	BatchID                string
	AnswerVersion          string
	QuestionID             string
	RubricVersion          string
	ModelVersion           string
	PromptVersion          string
	MinConfidence          float64
	RequestID              string
	PanelID                string
	AgentRole              string
	MathArtifactID         string
	MathArtifactVersion    int64
	MathCorrectionRevision int64
	MathScoringVersion     string
}

type UpdateRunInput struct {
	Status       string
	GradeID      string
	ErrorCode    string
	AttemptCount int
}

type WorkerResultInput struct {
	TaskID              string        `json:"task_id"`
	LeaseToken          string        `json:"lease_token"`
	ResultSchemaVersion string        `json:"result_schema_version"`
	DurationMS          int           `json:"duration_ms"`
	Output              AdapterOutput `json:"output"`
}

type WorkerFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type GradingBatch struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	IdempotencyKey  string    `json:"idempotency_key"`
	Status          string    `json:"status"`
	SegmentIDs      []string  `json:"segment_ids"`
	TotalCount      int       `json:"total_count"`
	QueuedCount     int       `json:"queued_count"`
	ProcessingCount int       `json:"processing_count"`
	SucceededCount  int       `json:"succeeded_count"`
	FailedCount     int       `json:"failed_count"`
	CreatedBy       string    `json:"created_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CreateBatchInput struct {
	IdempotencyKey string   `json:"idempotency_key"`
	SegmentIDs     []string `json:"segment_ids"`
}

type UpdateBatchInput struct {
	Status          string
	QueuedCount     int
	ProcessingCount int
	SucceededCount  int
	FailedCount     int
}

type BatchEnqueueFailure struct {
	SegmentID string `json:"segment_id"`
	Code      string `json:"code"`
}

type BatchEnqueueResult struct {
	RequestedCount int                   `json:"requested_count"`
	AcceptedCount  int                   `json:"accepted_count"`
	TaskCount      int                   `json:"task_count"`
	FailedCount    int                   `json:"failed_count"`
	PartialSuccess bool                  `json:"partial_success"`
	Failures       []BatchEnqueueFailure `json:"failures"`
}

type BatchContextStore interface {
	LoadContexts(ctx context.Context, tenantID string, segmentIDs []string) ([]Context, error)
}

type Store interface {
	CompleteEnqueuePlan(context.Context, string, string, string) error
	RecoverBatchCommand(context.Context, string, string, string) (BatchCommandRecovery, error)
	GetEnqueuePlan(context.Context, string, string, string) (BatchEnqueuePlan, error)
	SaveEnqueuePlan(context.Context, string, string, BatchEnqueuePlan) (BatchEnqueuePlan, error)
	LoadContext(ctx context.Context, tenantID string, segmentID string) (Context, error)
	GetRun(ctx context.Context, tenantID string, runID string) (GradingRun, error)
	GetGradeByAdapterRequestID(ctx context.Context, tenantID string, requestID string) (Grade, error)
	CreateGrade(ctx context.Context, tenantID string, actorID string, grade Grade) (Grade, error)
	GetOrCreateRun(ctx context.Context, tenantID string, actorID string, input CreateRunInput) (GradingRun, error)
	UpdateRun(ctx context.Context, tenantID string, runID string, input UpdateRunInput) (GradingRun, error)
	CreateBatch(ctx context.Context, tenantID string, actorID string, input CreateBatchInput) (GradingBatch, error)
	GetBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error)
	RefreshBatch(ctx context.Context, tenantID string, batchID string) (GradingBatch, error)
	UpdateBatch(ctx context.Context, tenantID string, batchID string, input UpdateBatchInput) (GradingBatch, error)
}

type LLMGradingAdapter interface {
	Name() string
	Grade(ctx context.Context, input AdapterInput) (AdapterOutput, error)
}

type GovernedPolicyProvider interface {
	Policy() ModelPolicy
}

package grading

import (
	"context"
	"time"
)

type ScoringRun struct {
	ID                  string     `json:"id"`
	TenantID            string     `json:"tenant_id"`
	ExamID              string     `json:"exam_id"`
	IdempotencyKey      string     `json:"idempotency_key"`
	Status              string     `json:"status"`
	TotalCount          int        `json:"total_count"`
	QueuedCount         int        `json:"queued_count"`
	AutoConfirmedCount  int        `json:"auto_confirmed_count"`
	HumanConfirmedCount int        `json:"human_confirmed_count"`
	ReviewCount         int        `json:"review_count"`
	FailedCount         int        `json:"failed_count"`
	StartedBy           string     `json:"started_by"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type StartScoringRunInput struct {
	IdempotencyKey string `json:"idempotency_key"`
}

type ScoringQuestionSummary struct {
	QuestionID   string `json:"question_id"`
	QuestionNo   string `json:"question_no"`
	QuestionType string `json:"question_type"`
	Total        int    `json:"total"`
	Queued       int    `json:"queued"`
	Confirmed    int    `json:"confirmed"`
	Review       int    `json:"review"`
	Failed       int    `json:"failed"`
}

type ScoringSummary struct {
	Run       *ScoringRun              `json:"run,omitempty"`
	Questions []ScoringQuestionSummary `json:"questions"`
}

type ScoringReadinessCheck struct {
	Code     string `json:"code"`
	Label    string `json:"label"`
	Passed   bool   `json:"passed"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Count    int    `json:"count,omitempty"`
}

type ScoringReadiness struct {
	Ready                  bool                    `json:"ready"`
	ExamStatus             string                  `json:"exam_status"`
	TotalQuestions         int                     `json:"total_questions"`
	TotalSegments          int                     `json:"total_segments"`
	ReadySegments          int                     `json:"ready_segments"`
	AutomaticCandidates    int                     `json:"automatic_candidates"`
	ManualReviewCandidates int                     `json:"manual_review_candidates"`
	ActiveRun              *ScoringRun             `json:"active_run,omitempty"`
	Checks                 []ScoringReadinessCheck `json:"checks"`
}

// ScoringRunItem is the operator-facing state of one answer segment in a run.
// It intentionally contains no student identity; the grading workspace remains
// the only place that resolves an anonymous task to protected answer assets.
type ScoringRunItem struct {
	AnswerSegmentID       string             `json:"answer_segment_id"`
	SubmissionID          string             `json:"submission_id"`
	SubmissionPageID      string             `json:"submission_page_id"`
	PageNo                int                `json:"page_no"`
	PageFileAssetID       string             `json:"page_file_asset_id"`
	QuestionID            string             `json:"question_id"`
	QuestionNo            string             `json:"question_no"`
	QuestionType          string             `json:"question_type"`
	AssessmentSnapshotID  string             `json:"assessment_snapshot_id"`
	AnonymousCode         string             `json:"anonymous_code"`
	NormalizedBBox        map[string]float64 `json:"normalized_bbox"`
	State                 string             `json:"state"`
	RecognitionSource     string             `json:"recognition_source,omitempty"`
	RecognizedAnswer      string             `json:"recognized_answer,omitempty"`
	RecognitionDecision   string             `json:"recognition_decision,omitempty"`
	RecognitionConfidence *float64           `json:"recognition_confidence,omitempty"`
	StandardAnswer        any                `json:"standard_answer,omitempty"`
	RuleType              string             `json:"rule_type,omitempty"`
	Score                 *float64           `json:"score,omitempty"`
	MaxScore              *float64           `json:"max_score,omitempty"`
	GradeSource           string             `json:"grade_source,omitempty"`
	OMRRunID              string             `json:"omr_run_id,omitempty"`
	RuntimeTaskID         string             `json:"runtime_task_id,omitempty"`
	RuntimeStatus         string             `json:"runtime_status,omitempty"`
	ReviewTaskID          string             `json:"review_task_id,omitempty"`
	ReviewStatus          string             `json:"review_status,omitempty"`
	ReasonCode            string             `json:"reason_code,omitempty"`
	ErrorCode             string             `json:"error_code,omitempty"`
}

type ScoringRunDetail struct {
	Run   ScoringRun       `json:"run"`
	Items []ScoringRunItem `json:"items"`
}

type ExamAutomationResults struct {
	Items []ScoringRunItem `json:"items"`
}

type FailedOMRTask struct {
	OMRRunID      string `json:"omr_run_id"`
	RuntimeTaskID string `json:"runtime_task_id"`
}

// OMRRun 中的资格、阈值和校准摘要由服务端排队时冻结，不能采用 Worker 回传值代替。
type OMRRun struct {
	ID                       string           `json:"id"`
	TenantID                 string           `json:"tenant_id"`
	ScoringRunID             string           `json:"scoring_run_id"`
	AnswerSegmentID          string           `json:"answer_segment_id"`
	ExamID                   string           `json:"exam_id"`
	RuntimeTaskID            string           `json:"runtime_task_id"`
	Status                   string           `json:"status"`
	Decision                 string           `json:"decision,omitempty"`
	Confidence               *float64         `json:"confidence,omitempty"`
	Measurements             []map[string]any `json:"measurements"`
	SelectedOptions          []string         `json:"selected_options"`
	OverlayFileAssetID       string           `json:"overlay_file_asset_id,omitempty"`
	CropSHA256               string           `json:"crop_sha256"`
	ReferenceFileAssetID     string           `json:"reference_file_asset_id,omitempty"`
	ReferenceSHA256          string           `json:"reference_sha256,omitempty"`
	CalibrationSessionID     string           `json:"calibration_session_id,omitempty"`
	CalibrationEvidenceHash  string           `json:"calibration_evidence_hash,omitempty"`
	ProfileVersion           string           `json:"profile_version"`
	ProfileHash              string           `json:"profile_hash"`
	AutoConfirmMinConfidence float64          `json:"auto_confirm_min_confidence"`
	AutoConfirmEligible      bool             `json:"auto_confirm_eligible"`
	AutoConfirmReason        string           `json:"auto_confirm_reason"`
}

type OMRResultInput struct {
	TaskID             string           `json:"task_id"`
	LeaseToken         string           `json:"lease_token"`
	ResultVersion      string           `json:"result_version"`
	DurationMS         int              `json:"duration_ms"`
	Decision           string           `json:"decision"`
	Selected           []string         `json:"selected"`
	Confidence         float64          `json:"confidence"`
	NeedsHumanReview   bool             `json:"needs_human_review"`
	Measurements       []map[string]any `json:"measurements"`
	ProfileVersion     string           `json:"profile_version"`
	ProfileHash        string           `json:"profile_hash"`
	ReferenceSHA256    string           `json:"reference_sha256,omitempty"`
	Thresholds         map[string]any   `json:"thresholds"`
	OverlayFileAssetID string           `json:"overlay_file_asset_id"`
	OverlaySHA256      string           `json:"overlay_sha256"`
}

type OMRFailureInput struct {
	TaskID      string         `json:"task_id"`
	LeaseToken  string         `json:"lease_token"`
	Retryable   bool           `json:"retryable"`
	ErrorCode   string         `json:"error_code"`
	ErrorDetail map[string]any `json:"error_detail"`
	DurationMS  int            `json:"duration_ms"`
}

type ScoringRunStore interface {
	RecoverScoringCommand(context.Context, string, string, string, string) (ScoringCommandRecovery, error)
	GetScoringReadiness(context.Context, string, string) (ScoringReadiness, error)
	StartScoringRun(context.Context, string, string, string, StartScoringRunInput) (ScoringRun, error)
	GetScoringSummary(context.Context, string, string) (ScoringSummary, error)
	GetOMRRun(context.Context, string, string) (OMRRun, error)
	ApplyOMRResult(context.Context, string, string, string, OMRResultInput, *Engine) (OMRRun, *QuestionGrade, error)
	ApplyOMRFailure(context.Context, string, string, string, map[string]any, bool) (OMRRun, error)
	ConfirmRuleGrade(context.Context, string, string, string, Grade) (QuestionGrade, error)
	ProcessRuleCandidates(context.Context, string, string, string, *Engine) error
}

// ScoringRecoveryStore holds the operations that coordinate scoring facts with
// Worker Runtime recovery. It is separate from normal grading operations so a
// caller cannot accidentally invoke recovery behavior through a basic store.
type ScoringRecoveryStore interface {
	GetScoringRunDetail(context.Context, string, string) (ScoringRunDetail, error)
	GetExamAutomationResults(context.Context, string, string) (ExamAutomationResults, error)
	BeginScoringRunCancellation(context.Context, string, string) (ScoringRun, []string, error)
	FinalizeScoringRunCancellation(context.Context, string, string) (ScoringRun, error)
	ListFailedOMRTasks(context.Context, string, string) ([]FailedOMRTask, error)
	PrepareOMRRetry(context.Context, string, string) (FailedOMRTask, error)
	RestoreOMRRetry(context.Context, string, string) error
	RefreshScoringRun(context.Context, string, string) (ScoringRun, error)
	ReprocessSegmentScore(context.Context, string, string, string, StartScoringRunInput) (ScoringRun, error)
}

type QuestionGrade struct {
	ID              string         `json:"id"`
	AnswerSegmentID string         `json:"answer_segment_id"`
	QuestionID      string         `json:"question_id"`
	Score           float64        `json:"score"`
	MaxScore        float64        `json:"max_score"`
	Source          string         `json:"source"`
	Version         int            `json:"version"`
	Evidence        map[string]any `json:"evidence"`
	CreatedAt       time.Time      `json:"created_at"`
}

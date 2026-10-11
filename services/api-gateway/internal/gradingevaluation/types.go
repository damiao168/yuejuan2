// Package gradingevaluation 记录受治理评分运行与已对齐 Gold 或人工参考之间的真实离线比较，只提供证据，不调用模型、暴露答案或作生产路由决策。
package gradingevaluation

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound      = errors.New("grading evaluation resource not found")
	ErrInvalidInput  = errors.New("invalid grading evaluation input")
	ErrStateConflict = errors.New("grading evaluation state conflict")
	ErrConflict      = errors.New("grading evaluation conflict")
)

type RunStatus string

const (
	RunDraft     RunStatus = "draft"
	RunCompleted RunStatus = "completed"
	RunInvalid   RunStatus = "invalidated"
)

type ReferenceKind string

const (
	ReferenceGold             ReferenceKind = "gold"
	ReferenceHumanAdjudicated ReferenceKind = "human_adjudicated"
)

const (
	SliceSubject          = "subject"
	SliceArchetype        = "archetype"
	SliceScoreBand        = "score_band"
	SliceOCRQuality       = "ocr_quality"
	SliceAnswerLength     = "answer_length"
	SliceRubricComplexity = "rubric_complexity"
	SliceErrorSource      = "error_source"
)

type ErrorSource string

const (
	ErrorNone               ErrorSource = "none"
	ErrorImageQuality       ErrorSource = "image_quality"
	ErrorPageMatching       ErrorSource = "page_matching"
	ErrorAnswerCrop         ErrorSource = "answer_crop"
	ErrorHandwritingOCR     ErrorSource = "handwriting_ocr"
	ErrorFormulaRecognition ErrorSource = "formula_recognition"
	ErrorAnswerStructuring  ErrorSource = "answer_structuring"
	ErrorRubric             ErrorSource = "rubric"
	ErrorModelScoring       ErrorSource = "model_scoring"
	ErrorScoreCalculation   ErrorSource = "score_calculation"
	ErrorSystem             ErrorSource = "system"
	ErrorUnattributed       ErrorSource = "unattributed"
)

type Run struct {
	ID                 string     `json:"id"`
	TenantID           string     `json:"tenant_id,omitempty"`
	Key                string     `json:"key"`
	DisplayName        string     `json:"display_name"`
	ModelReference     string     `json:"model_reference"`
	PromptVersion      string     `json:"prompt_version"`
	RubricVersion      string     `json:"rubric_version"`
	DatasetReference   string     `json:"dataset_reference"`
	DatasetSHA256      string     `json:"dataset_sha256"`
	Status             RunStatus  `json:"status"`
	ObservationCount   int        `json:"observation_count"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	InvalidatedAt      *time.Time `json:"invalidated_at,omitempty"`
	InvalidationReason string     `json:"invalidation_reason,omitempty"`
	CreatedBy          string     `json:"created_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// Observation 仅包含不透明响应键、指纹和对齐分数；答案文本、图片及 Gold 内容不进入此组件。
// 六个切片字段在观察时记录，防止后续元数据变化改写历史评测证据。
type Observation struct {
	ID                   string        `json:"id"`
	RunID                string        `json:"run_id"`
	ResponseKey          string        `json:"response_key"`
	ResponseFingerprint  string        `json:"response_fingerprint"`
	ReferenceKind        ReferenceKind `json:"reference_kind"`
	Subject              string        `json:"subject"`
	Archetype            string        `json:"archetype"`
	OCRQuality           string        `json:"ocr_quality"`
	AnswerLength         string        `json:"answer_length"`
	RubricComplexity     string        `json:"rubric_complexity"`
	ReferenceScore       float64       `json:"reference_score"`
	ModelScore           float64       `json:"model_score"`
	MaxScore             float64       `json:"max_score"`
	ReferenceScoreBand   string        `json:"reference_score_band"`
	PageMatchCorrect     *bool         `json:"page_match_correct,omitempty"`
	CropIoU              *float64      `json:"crop_iou,omitempty"`
	TranscriptionCER     *float64      `json:"transcription_cer,omitempty"`
	FormulaExact         *bool         `json:"formula_exact,omitempty"`
	RubricAgreement      *float64      `json:"rubric_criterion_agreement,omitempty"`
	ErrorSource          ErrorSource   `json:"error_source"`
	NeedsHumanReview     bool          `json:"needs_human_review"`
	ReferenceReviewers   int           `json:"reference_reviewer_count"`
	ReferenceAdjudicated bool          `json:"reference_adjudicated"`
	ObservedAt           time.Time     `json:"observed_at"`
}

type CoveredRate struct {
	ObservedCount int      `json:"observed_count"`
	Rate          *float64 `json:"rate,omitempty"`
}

type CoveredMean struct {
	ObservedCount int      `json:"observed_count"`
	Mean          *float64 `json:"mean,omitempty"`
}

type ErrorAttribution struct {
	Source             ErrorSource `json:"source"`
	Count              int         `json:"count"`
	Rate               float64     `json:"rate"`
	SevereErrorCount   int         `json:"severe_error_count"`
	HumanReviewCount   int         `json:"human_review_count"`
	HumanRoutingRecall float64     `json:"human_routing_recall"`
}

type QualitySummary struct {
	SampleCount                  int                `json:"sample_count"`
	PageMatchAccuracy            CoveredRate        `json:"page_match_accuracy"`
	MeanCropIoU                  CoveredMean        `json:"mean_crop_iou"`
	MeanTranscriptionCER         CoveredMean        `json:"mean_transcription_cer"`
	FormulaExactRate             CoveredRate        `json:"formula_exact_rate"`
	MeanRubricCriterionAgreement CoveredMean        `json:"mean_rubric_criterion_agreement"`
	HumanReviewRate              float64            `json:"human_review_rate"`
	RiskyErrorRoutingRecall      float64            `json:"risky_error_routing_recall"`
	ErrorAttribution             []ErrorAttribution `json:"error_attribution"`
}

type Metrics struct {
	SampleCount          int      `json:"sample_count"`
	MAE                  float64  `json:"mae"`
	ExactRate            float64  `json:"exact_rate"`
	WithinOneRate        float64  `json:"within_one_rate"`
	SevereErrorRate      float64  `json:"severe_error_rate"`
	FalseZeroRate        float64  `json:"false_zero_rate"`
	FalseFullRate        float64  `json:"false_full_rate"`
	QWK                  *float64 `json:"qwk,omitempty"`
	QWKAvailable         bool     `json:"qwk_available"`
	QWKUnavailableReason string   `json:"qwk_unavailable_reason,omitempty"`
}

type SliceMetric struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	Dimension  string    `json:"dimension"`
	Value      string    `json:"value"`
	Metrics    Metrics   `json:"metrics"`
	ComputedAt time.Time `json:"computed_at"`
}

// ResponseDifficulty 是运行范围内、基于实证的疑难案例指标；它不标记学生能力，也不能作为生产分数。
type ResponseDifficulty struct {
	ID                  string    `json:"id"`
	RunID               string    `json:"run_id"`
	ResponseKey         string    `json:"response_key"`
	ResponseFingerprint string    `json:"response_fingerprint"`
	DifficultyScore     float64   `json:"difficulty_score"`
	DifficultyBand      string    `json:"difficulty_band"`
	NormalizedError     float64   `json:"normalized_error"`
	SevereError         bool      `json:"severe_error"`
	OCRQuality          string    `json:"ocr_quality"`
	AnswerLength        string    `json:"answer_length"`
	RubricComplexity    string    `json:"rubric_complexity"`
	EvidenceNote        string    `json:"evidence_note"`
	ComputedAt          time.Time `json:"computed_at"`
}

type CreateRunInput struct {
	Key              string `json:"key"`
	DisplayName      string `json:"display_name"`
	ModelReference   string `json:"model_reference"`
	PromptVersion    string `json:"prompt_version"`
	RubricVersion    string `json:"rubric_version"`
	DatasetReference string `json:"dataset_reference"`
	DatasetSHA256    string `json:"dataset_sha256"`
}

type AddObservationInput struct {
	ResponseKey          string        `json:"response_key"`
	ResponseFingerprint  string        `json:"response_fingerprint"`
	ReferenceKind        ReferenceKind `json:"reference_kind"`
	Subject              string        `json:"subject"`
	Archetype            string        `json:"archetype"`
	OCRQuality           string        `json:"ocr_quality"`
	AnswerLength         string        `json:"answer_length"`
	RubricComplexity     string        `json:"rubric_complexity"`
	ReferenceScore       float64       `json:"reference_score"`
	ModelScore           float64       `json:"model_score"`
	MaxScore             float64       `json:"max_score"`
	PageMatchCorrect     *bool         `json:"page_match_correct,omitempty"`
	CropIoU              *float64      `json:"crop_iou,omitempty"`
	TranscriptionCER     *float64      `json:"transcription_cer,omitempty"`
	FormulaExact         *bool         `json:"formula_exact,omitempty"`
	RubricAgreement      *float64      `json:"rubric_criterion_agreement,omitempty"`
	ErrorSource          ErrorSource   `json:"error_source,omitempty"`
	NeedsHumanReview     bool          `json:"needs_human_review"`
	ReferenceReviewers   int           `json:"reference_reviewer_count,omitempty"`
	ReferenceAdjudicated bool          `json:"reference_adjudicated"`
}

type RunFilter struct {
	Limit int
}

type Store interface {
	CreateRun(context.Context, string, string, CreateRunInput) (Run, error)
	GetRun(context.Context, string, string) (Run, error)
	ListRuns(context.Context, string, RunFilter) ([]Run, error)
	AddObservation(context.Context, string, string, AddObservationInput) (Observation, error)
	ListObservations(context.Context, string, string) ([]Observation, error)
	ReplaceComputed(context.Context, string, string, int, []SliceMetric, []ResponseDifficulty, time.Time) (Run, error)
	ListSliceMetrics(context.Context, string, string) ([]SliceMetric, error)
	ListResponseDifficulty(context.Context, string, string) ([]ResponseDifficulty, error)
	InvalidateRun(context.Context, string, string, string, time.Time) (Run, error)
}

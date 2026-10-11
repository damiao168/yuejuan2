// Package modelcalibration 将模型报告的置信度转换为受版本范围限制的实证估计。
// 它只提供证据：校准产物可使候选项弃权，但不能发布分数或授予模型最终评分权。
package modelcalibration

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound            = errors.New("model calibration resource not found")
	ErrInvalidInput        = errors.New("invalid model calibration input")
	ErrStateConflict       = errors.New("model calibration state conflict")
	ErrConflict            = errors.New("model calibration conflict")
	ErrEvaluationRequired  = errors.New("completed aligned evaluation is required")
	ErrEvidenceUnavailable = errors.New("aligned evaluation observation is unavailable")
)

type Method string

const (
	MethodAuto      Method = "auto"
	MethodIsotonic  Method = "isotonic"
	MethodLogistic  Method = "logistic"
	MethodConformal Method = "conformal"
)

func (m Method) Valid() bool {
	return m == MethodAuto || m == MethodIsotonic || m == MethodLogistic || m == MethodConformal
}

type Status string

const (
	StatusDraft       Status = "draft"
	StatusCompleted   Status = "completed"
	StatusApproved    Status = "approved"
	StatusInvalidated Status = "invalidated"
)

// Axis 刻意包含完整维度，尤其将提示词和评分细则版本纳入身份；后续模型、提示词或评分细则不能继承早期部署的校准证据。
type Axis struct {
	ModelReference string `json:"model_reference"`
	PromptVersion  string `json:"prompt_version"`
	RubricVersion  string `json:"rubric_version"`
	Subject        string `json:"subject"`
	Archetype      string `json:"archetype"`
	// SliceKey 的值为 all、score_band:{zero|partial|full} 或 ocr_quality:{high|medium|low|unknown}，用于可复现地记录是否使用了更细的安全切片。
	SliceKey string `json:"slice_key"`
}

type Calibration struct {
	ID                 string     `json:"id"`
	TenantID           string     `json:"tenant_id,omitempty"`
	Key                string     `json:"key"`
	EvaluationRunID    string     `json:"evaluation_run_id"`
	Axis               Axis       `json:"axis"`
	Method             Method     `json:"method"`
	Status             Status     `json:"status"`
	CalibrationN       int        `json:"calibration_n"`
	ArtifactURI        string     `json:"artifact_uri,omitempty"`
	ArtifactSHA256     string     `json:"artifact_sha256,omitempty"`
	Artifact           Artifact   `json:"artifact,omitempty"`
	CreatedBy          string     `json:"created_by,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	ApprovedBy         string     `json:"approved_by,omitempty"`
	InvalidatedAt      *time.Time `json:"invalidated_at,omitempty"`
	InvalidatedBy      string     `json:"invalidated_by,omitempty"`
	InvalidationReason string     `json:"invalidation_reason,omitempty"`
}

// CalibrationEvidence 是有效且对齐的 A16 观察，附带被校准的精确受治理模型、提示词输出的置信度；
// 它不含答案正文、图片、OCR 文本或 Gold 理由。
type CalibrationEvidence struct {
	ID              string    `json:"id"`
	CalibrationID   string    `json:"calibration_id"`
	EvaluationRunID string    `json:"evaluation_run_id"`
	ResponseKey     string    `json:"response_key"`
	RawConfidence   float64   `json:"raw_confidence"`
	Correct         bool      `json:"correct"`
	SevereError     bool      `json:"severe_error"`
	ScoreBand       string    `json:"score_band"`
	OCRQuality      string    `json:"ocr_quality"`
	ObservedAt      time.Time `json:"observed_at"`
}

type Bin struct {
	MinRawConfidence     float64 `json:"min_raw_confidence"`
	MaxRawConfidence     float64 `json:"max_raw_confidence"`
	CalibratedConfidence float64 `json:"calibrated_confidence"`
	SampleCount          int     `json:"sample_count"`
	CorrectCount         int     `json:"correct_count"`
}

// RiskCoveragePoint 基于实证，仅来自为此产物保存的对齐观察；覆盖率不承诺自动评分。
type RiskCoveragePoint struct {
	Threshold       float64 `json:"threshold"`
	Coverage        float64 `json:"coverage"`
	SampleCount     int     `json:"sample_count"`
	EmpiricalRisk   float64 `json:"empirical_risk"`
	SevereErrorRate float64 `json:"severe_error_rate"`
}

type Metrics struct {
	SampleCount              int      `json:"sample_count"`
	BrierScore               float64  `json:"brier_score"`
	ExpectedCalibrationError float64  `json:"expected_calibration_error"`
	MiddleScoreSampleCount   int      `json:"middle_score_sample_count"`
	MiddleScoreBrierScore    *float64 `json:"middle_score_brier_score,omitempty"`
}

type Artifact struct {
	SchemaVersion     int                 `json:"schema_version"`
	Method            Method              `json:"method"`
	Bins              []Bin               `json:"bins"`
	Metrics           Metrics             `json:"metrics"`
	RiskCoverageCurve []RiskCoveragePoint `json:"risk_coverage_curve"`
}

// ArtifactReference 把同一不可变校准产物的存储位置与内容摘要绑定，不包含执行依赖或校准算法输入。
type ArtifactReference struct {
	URI    string
	SHA256 string
}

type CreateInput struct {
	Key             string `json:"key"`
	EvaluationRunID string `json:"evaluation_run_id"`
	Axis            Axis   `json:"axis"`
	Method          Method `json:"method"`
}

type AddEvidenceInput struct {
	ResponseKey   string  `json:"response_key"`
	RawConfidence float64 `json:"raw_confidence"`
}

// Candidate 是不透明评分候选项的不可变置信度决策。刻意不包含 SuggestedScore；此表不是成绩事实，也不能当作成绩使用。
type Candidate struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id,omitempty"`
	CandidateKey         string    `json:"candidate_key"`
	Axis                 Axis      `json:"axis"`
	RawConfidence        float64   `json:"raw_confidence"`
	CalibratedConfidence *float64  `json:"calibrated_confidence,omitempty"`
	CalibrationID        string    `json:"calibration_id,omitempty"`
	TargetRisk           *float64  `json:"target_risk,omitempty"`
	AbstainReason        string    `json:"abstain_reason,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}

type RecordCandidateInput struct {
	CandidateKey  string  `json:"candidate_key"`
	Axis          Axis    `json:"axis"`
	RawConfidence float64 `json:"raw_confidence"`
	// TargetRisk 是已观察严重错误率的上限。提供此值时，只有校准置信度达到实证严重风险满足目标的阈值，候选项才不弃权。
	TargetRisk *float64 `json:"target_risk,omitempty"`
}

// ApprovedEvidence 是供 A14 门禁使用的小型投影；调用方只取得产物引用，不取得训练观察。
type ApprovedEvidence struct {
	Available       bool   `json:"available"`
	CalibrationID   string `json:"calibration_id,omitempty"`
	CalibrationRef  string `json:"calibration_ref,omitempty"`
	CalibrationN    int    `json:"calibration_n,omitempty"`
	EvaluationRunID string `json:"evaluation_run_id,omitempty"`
	// SevereErrorRate 是不可变产物在阈值 0 下的实证比率，供 A14 复用同一证据，避免从无关切片构造另一项总体指标。
	SevereErrorRate float64 `json:"severe_error_rate,omitempty"`
}

// EvaluationReader 由 gradingevaluation.Service 实现。接口保留在此处，避免重复访问路径，使本包不能自行制造对齐数据。
type EvaluationReader interface {
	GetRun(context.Context, string, string) (EvaluationRun, error)
	ListObservations(context.Context, string, string) ([]EvaluationObservation, error)
}

type EvaluationRun struct {
	ID             string
	ModelReference string
	PromptVersion  string
	RubricVersion  string
	Status         string
}

type EvaluationObservation struct {
	ResponseKey    string
	Subject        string
	Archetype      string
	OCRQuality     string
	ScoreBand      string
	ReferenceScore float64
	ModelScore     float64
	MaxScore       float64
}

type Store interface {
	Create(context.Context, string, string, CreateInput) (Calibration, error)
	Get(context.Context, string, string) (Calibration, error)
	List(context.Context, string, Axis, int) ([]Calibration, error)
	AddEvidence(context.Context, string, string, CalibrationEvidence) (CalibrationEvidence, error)
	ListEvidence(context.Context, string, string) ([]CalibrationEvidence, error)
	Complete(context.Context, string, string, int, Artifact, ArtifactReference, time.Time) (Calibration, error)
	Approve(context.Context, string, string, string, time.Time) (Calibration, error)
	Invalidate(context.Context, string, string, string, string, time.Time) (Calibration, error)
	FindApproved(context.Context, string, Axis) (Calibration, error)
	CreateOrGetCandidate(context.Context, string, Candidate) (Candidate, error)
}

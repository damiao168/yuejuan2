// Package graderdrift 将隐藏 Seed 观察转为范围受限、仅管理员可见的复核者质量证据；它不接收或返回 Gold 答案内容。
package graderdrift

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("grader drift resource not found")
	ErrInvalidInput = errors.New("invalid grader drift input")
)

type WindowStatus string

const (
	WindowInsufficientData WindowStatus = "insufficient_data"
	WindowStable           WindowStatus = "stable"
	WindowWarning          WindowStatus = "warning"
	WindowCritical         WindowStatus = "critical"
)

type IncidentType string

const (
	IncidentBiasHigh           IncidentType = "grader_bias_high"
	IncidentBiasLow            IncidentType = "grader_bias_low"
	IncidentHighInconsistency  IncidentType = "high_inconsistency"
	IncidentSevereSeedFailure  IncidentType = "severe_seed_failure"
	IncidentRubricDisagreement IncidentType = "rubric_disagreement_spike"
	IncidentSuspiciousSpeed    IncidentType = "suspicious_speed"
)

type Severity string

const (
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type IncidentStatus string

const (
	IncidentOpen         IncidentStatus = "open"
	IncidentAcknowledged IncidentStatus = "acknowledged"
	IncidentResolved     IncidentStatus = "resolved"
)

// QualityWindow 只保留聚合信息，不保留 Gold ID、参考答案、已提交答案内容或学生信息。
type QualityWindow struct {
	ID                        string       `json:"id"`
	ExamID                    string       `json:"exam_id"`
	QuestionID                string       `json:"question_id"`
	GraderID                  string       `json:"grader_id"`
	WindowSize                int          `json:"window_size"`
	WindowStart               time.Time    `json:"window_start"`
	WindowEnd                 time.Time    `json:"window_end"`
	SampleCount               int          `json:"sample_count"`
	MeanError                 float64      `json:"mean_error"`
	MAE                       float64      `json:"mae"`
	ExactAgreement            float64      `json:"exact_agreement"`
	RubricAgreement           *float64     `json:"rubric_agreement,omitempty"`
	SevereRate                float64      `json:"severe_rate"`
	MiddleScoreSampleCount    int          `json:"middle_score_sample_count"`
	MiddleScoreMAE            *float64     `json:"middle_score_mae,omitempty"`
	MiddleScoreExactAgreement *float64     `json:"middle_score_exact_agreement,omitempty"`
	EWMABias                  *float64     `json:"ewma_bias,omitempty"`
	Status                    WindowStatus `json:"status"`
	ComputedAt                time.Time    `json:"computed_at"`
}

type Incident struct {
	ID             string         `json:"id"`
	ExamID         string         `json:"exam_id"`
	QuestionID     string         `json:"question_id"`
	GraderID       string         `json:"grader_id"`
	SourceWindowID string         `json:"source_window_id"`
	Type           IncidentType   `json:"type"`
	Severity       Severity       `json:"severity"`
	MetricSnapshot map[string]any `json:"metric_snapshot"`
	AffectedRange  map[string]any `json:"affected_range"`
	Status         IncidentStatus `json:"status"`
	CreatedAt      time.Time      `json:"created_at"`
	ResolvedAt     *time.Time     `json:"resolved_at,omitempty"`
}

type RefreshInput struct {
	ExamID     string
	QuestionID string
	GraderID   string
}

type RefreshResult struct {
	Windows          []QualityWindow `json:"windows"`
	CreatedIncidents []Incident      `json:"created_incidents"`
}

type WindowFilter struct {
	ExamID     string
	QuestionID string
	GraderID   string
	Limit      int
}

type IncidentFilter struct {
	ExamID     string
	QuestionID string
	GraderID   string
	Status     IncidentStatus
	Limit      int
}

type Store interface {
	UpsertWindow(context.Context, string, QualityWindow) (QualityWindow, error)
	CreateIncidentsIfMissing(context.Context, string, []Incident) ([]Incident, error)
	ListWindows(context.Context, string, WindowFilter) ([]QualityWindow, error)
	ListIncidents(context.Context, string, IncidentFilter) ([]Incident, error)
	ResolveIncident(context.Context, string, string, time.Time) (Incident, error)
}

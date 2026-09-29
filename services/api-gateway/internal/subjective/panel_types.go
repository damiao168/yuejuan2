package subjective

import (
	"context"
	"errors"
	"time"
)

const (
	PanelPrimaryPending     = "primary_pending"
	PanelComparing          = "comparing"
	PanelArbitrationPending = "arbitration_pending"
	PanelResolved           = "resolved"
	PanelHumanReview        = "human_review"
	PanelFailed             = "failed"
)

const (
	ResolutionPrimaryConsensus = "primary_consensus"
	ResolutionArbiter          = "arbiter"
	ResolutionHuman            = "human"
)

var (
	ErrPanelConfiguration  = errors.New("invalid subjective grading panel configuration")
	ErrPanelAgentFailure   = errors.New("subjective grading panel agent failed")
	ErrPanelRunInProgress  = errors.New("subjective grading panel role is already in progress")
	ErrPanelPolicyRequired = errors.New("approved subjective panel policy is required")
)

// PanelDecisionConfig is frozen with every panel so later calibration cannot
// rewrite why an answer was sent to arbitration or human review.
type PanelDecisionConfig struct {
	PolicyVersion               string   `json:"policy_version"`
	ScoreGapThreshold           float64  `json:"score_gap_threshold"`
	CriterionGapThreshold       float64  `json:"criterion_gap_threshold"`
	ConfidenceGapThreshold      float64  `json:"confidence_gap_threshold"`
	ArbiterMinConfidence        float64  `json:"arbiter_min_confidence"`
	ArbiterAgreementThreshold   float64  `json:"arbiter_agreement_threshold"`
	TriggerAnyCriterionConflict bool     `json:"trigger_any_criterion_conflict"`
	TriggerEvidenceConflict     bool     `json:"trigger_evidence_conflict"`
	HardRiskCodes               []string `json:"hard_risk_codes"`
}

// GradingPanel 保存两名主评、仲裁和人工复核的完整轨迹；ResolvedScore 只能由记录的决策来源产生。
type GradingPanel struct {
	ID                    string              `json:"id"`
	TenantID              string              `json:"tenant_id"`
	AnswerSegmentID       string              `json:"answer_segment_id"`
	AnswerVersion         string              `json:"answer_version"`
	QuestionID            string              `json:"question_id"`
	RubricVersion         string              `json:"rubric_version"`
	PolicyVersion         string              `json:"policy_version"`
	PrimaryARunID         string              `json:"primary_a_run_id,omitempty"`
	PrimaryBRunID         string              `json:"primary_b_run_id,omitempty"`
	ArbiterRunID          string              `json:"arbiter_run_id,omitempty"`
	ScoreA                *float64            `json:"score_a,omitempty"`
	ScoreB                *float64            `json:"score_b,omitempty"`
	ScoreC                *float64            `json:"score_c,omitempty"`
	MaxScore              float64             `json:"max_score"`
	ScoreGap              *float64            `json:"score_gap,omitempty"`
	CriterionGap          *float64            `json:"criterion_gap,omitempty"`
	RequiredPointConflict bool                `json:"required_point_conflict"`
	EvidenceConflict      bool                `json:"evidence_conflict"`
	ConfidenceConflict    bool                `json:"confidence_conflict"`
	TriggerCodes          []string            `json:"trigger_codes"`
	DecisionConfig        PanelDecisionConfig `json:"decision_config"`
	Status                string              `json:"status"`
	ResolvedScore         *float64            `json:"resolved_score,omitempty"`
	ResolutionSource      string              `json:"resolution_source,omitempty"`
	ReviewTaskID          string              `json:"review_task_id,omitempty"`
	CreatedBy             string              `json:"created_by"`
	CreatedAt             time.Time           `json:"created_at"`
	UpdatedAt             time.Time           `json:"updated_at"`
	CompletedAt           *time.Time          `json:"completed_at,omitempty"`
}

type CreatePanelInput struct {
	AnswerSegmentID string
	AnswerVersion   string
	QuestionID      string
	RubricVersion   string
	MaxScore        float64
	DecisionConfig  PanelDecisionConfig
}

type UpdatePanelInput struct {
	Status                string
	PrimaryARunID         string
	PrimaryBRunID         string
	ArbiterRunID          string
	ScoreA                *float64
	ScoreB                *float64
	ScoreC                *float64
	ScoreGap              *float64
	CriterionGap          *float64
	RequiredPointConflict bool
	EvidenceConflict      bool
	ConfidenceConflict    bool
	TriggerCodes          []string
	ResolvedScore         *float64
	ResolutionSource      string
	ReviewTaskID          string
}

type PanelStore interface {
	GetOrCreatePanel(context.Context, string, string, CreatePanelInput) (GradingPanel, error)
	GetPanel(context.Context, string, string) (GradingPanel, error)
	UpdatePanel(context.Context, string, string, UpdatePanelInput) (GradingPanel, error)
	RoutePanelHumanReview(context.Context, string, string, GradingPanel) (string, error)
}

type PanelRunClaimer interface {
	ClaimPanelRun(context.Context, string, string) (GradingRun, bool, error)
}

type PanelPersistence interface {
	Store
	PanelStore
	PanelRunClaimer
}

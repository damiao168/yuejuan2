package assessment

import "errors"

var (
	ErrNotFound         = errors.New("assessment resource not found")
	ErrInvalidInput     = errors.New("invalid assessment input")
	ErrPolicyViolation  = errors.New("assessment scoring policy violation")
	ErrExamFrozen       = errors.New("assessment configuration is frozen")
	ErrRevisionConflict = errors.New("assessment configuration revision conflict")
	ErrSnapshotConflict = errors.New("assessment snapshot conflict")
)

type ScoringMode string

const (
	ScoringRuleAuto      ScoringMode = "RULE_AUTO"
	ScoringAIAssist      ScoringMode = "AI_ASSIST"
	ScoringAIFastConfirm ScoringMode = "AI_FAST_CONFIRM"
	ScoringHumanPrimary  ScoringMode = "HUMAN_PRIMARY"
	ScoringDualHuman     ScoringMode = "DUAL_HUMAN"
	ScoringManualOnly    ScoringMode = "MANUAL_ONLY"
)

type RiskTier string

const (
	RiskR1 RiskTier = "R1"
	RiskR2 RiskTier = "R2"
	RiskR3 RiskTier = "R3"
)

type ScoringPolicy struct {
	Mode                       ScoringMode `json:"mode"`
	ConfidenceThreshold        *float64    `json:"confidence_threshold,omitempty"`
	RequireEvidence            bool        `json:"require_evidence"`
	HumanReviewBelowConfidence bool        `json:"human_review_below_confidence"`
}

func (m ScoringMode) Valid() bool {
	switch m {
	case ScoringRuleAuto, ScoringAIAssist, ScoringAIFastConfirm, ScoringHumanPrimary, ScoringDualHuman, ScoringManualOnly:
		return true
	default:
		return false
	}
}

func (r RiskTier) Valid() bool {
	return r == RiskR1 || r == RiskR2 || r == RiskR3
}

// ValidateScoringPolicy 校验题目配置的基本约束；真正调用外部 AI 前仍须经过 aieligibility 的证据准入。
func ValidateScoringPolicy(risk RiskTier, archetypeCode string, policy ScoringPolicy) error {
	if !risk.Valid() || !IsQuestionArchetype(archetypeCode) || !policy.Mode.Valid() {
		return ErrInvalidInput
	}
	if policy.ConfidenceThreshold != nil && (*policy.ConfidenceThreshold < 0 || *policy.ConfidenceThreshold > 1) {
		return ErrInvalidInput
	}
	if risk == RiskR3 && archetypeCode == "extended_response" && policy.Mode == ScoringAIFastConfirm {
		return ErrPolicyViolation
	}
	return nil
}

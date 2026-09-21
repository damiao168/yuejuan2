package subjective

import (
	"math"
	"strings"
)

func NormalizePanelDecisionConfig(config PanelDecisionConfig) PanelDecisionConfig {
	config.PolicyVersion = strings.TrimSpace(config.PolicyVersion)
	if config.PolicyVersion == "" {
		config.PolicyVersion = "subjective-panel-shadow-v1"
	}
	if config.ScoreGapThreshold == 0 {
		config.ScoreGapThreshold = 0.15
	}
	if config.CriterionGapThreshold == 0 {
		config.CriterionGapThreshold = 0.20
	}
	if config.ConfidenceGapThreshold == 0 {
		config.ConfidenceGapThreshold = 0.25
	}
	if config.ArbiterMinConfidence == 0 {
		config.ArbiterMinConfidence = 0.85
	}
	if config.ArbiterAgreementThreshold == 0 {
		config.ArbiterAgreementThreshold = 0.80
	}
	// Evidence disagreement is always safety-significant even when the two
	// rubric projections happen to produce the same numeric score.
	config.TriggerEvidenceConflict = true
	if config.HardRiskCodes == nil {
		config.HardRiskCodes = []string{
			"prompt_injection_suspected", "ocr_low_confidence", "low_ocr_confidence", "graph_uncertain",
			"required_rubric_uncertain", "alternative_solution_candidate",
			"low_model_confidence", "calibration_risk_coverage_abstained",
		}
	}
	return config
}

func ValidatePanelDecisionConfig(config PanelDecisionConfig) error {
	config = NormalizePanelDecisionConfig(config)
	if config.PolicyVersion == "" || !unitInterval(config.ScoreGapThreshold) ||
		!unitInterval(config.CriterionGapThreshold) || !unitInterval(config.ConfidenceGapThreshold) ||
		!unitInterval(config.ArbiterMinConfidence) || !unitInterval(config.ArbiterAgreementThreshold) {
		return ErrPanelConfiguration
	}
	seen := map[string]bool{}
	for _, code := range config.HardRiskCodes {
		code = strings.TrimSpace(code)
		if code == "" || seen[code] {
			return ErrPanelConfiguration
		}
		seen[code] = true
	}
	return nil
}

func validateCreatePanel(input CreatePanelInput) error {
	if strings.TrimSpace(input.AnswerSegmentID) == "" || strings.TrimSpace(input.AnswerVersion) == "" || strings.TrimSpace(input.QuestionID) == "" ||
		strings.TrimSpace(input.RubricVersion) == "" || math.IsNaN(input.MaxScore) || math.IsInf(input.MaxScore, 0) || input.MaxScore <= 0 ||
		ValidatePanelDecisionConfig(input.DecisionConfig) != nil {
		return ErrPanelConfiguration
	}
	return nil
}

func panelRunSnapshotMatches(panel GradingPanel, input CreateRunInput) bool {
	return panel.TenantID != "" && panel.ID == input.PanelID &&
		panel.AnswerSegmentID == input.AnswerSegmentID && panel.AnswerVersion == input.AnswerVersion &&
		panel.QuestionID == input.QuestionID && panel.RubricVersion == input.RubricVersion
}

func runPanelIdentityMatches(run GradingRun, input CreateRunInput) bool {
	return run.PanelID == input.PanelID && run.AgentRole == input.AgentRole &&
		run.AnswerSegmentID == input.AnswerSegmentID && run.AnswerVersion == input.AnswerVersion &&
		run.QuestionID == input.QuestionID && run.RubricVersion == input.RubricVersion
}

func activePanelStatus(status string) bool {
	return status == PanelPrimaryPending || status == PanelComparing || status == PanelArbitrationPending || status == PanelHumanReview
}

func validPanelStatus(status string) bool {
	return activePanelStatus(status) || status == PanelResolved || status == PanelFailed
}

func validPanelTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case PanelPrimaryPending:
		return to == PanelComparing || to == PanelHumanReview || to == PanelFailed
	case PanelComparing:
		return to == PanelArbitrationPending || to == PanelResolved || to == PanelHumanReview || to == PanelFailed
	case PanelArbitrationPending:
		return to == PanelResolved || to == PanelHumanReview || to == PanelFailed
	case PanelHumanReview:
		return to == PanelResolved
	default:
		return false
	}
}

func validatePanelState(panel GradingPanel) error {
	if panel.MaxScore <= 0 || !validPanelStatus(panel.Status) {
		return ErrPanelConfiguration
	}
	for _, score := range []*float64{panel.ScoreA, panel.ScoreB, panel.ScoreC, panel.ResolvedScore} {
		if score != nil && (math.IsNaN(*score) || math.IsInf(*score, 0) || *score < 0 || *score > panel.MaxScore) {
			return ErrPanelConfiguration
		}
	}
	for _, gap := range []*float64{panel.ScoreGap, panel.CriterionGap} {
		if gap != nil && !unitInterval(*gap) {
			return ErrPanelConfiguration
		}
	}
	if panel.Status == PanelResolved && (panel.ResolvedScore == nil ||
		(panel.ResolutionSource != ResolutionPrimaryConsensus && panel.ResolutionSource != ResolutionArbiter && panel.ResolutionSource != ResolutionHuman)) {
		return ErrPanelConfiguration
	}
	if panel.Status == PanelHumanReview && panel.ResolutionSource != "" && panel.ResolutionSource != ResolutionHuman {
		return ErrPanelConfiguration
	}
	return nil
}

func applyPanelUpdate(panel *GradingPanel, input UpdatePanelInput) {
	panel.Status = input.Status
	panel.PrimaryARunID = firstNonEmpty(input.PrimaryARunID, panel.PrimaryARunID)
	panel.PrimaryBRunID = firstNonEmpty(input.PrimaryBRunID, panel.PrimaryBRunID)
	panel.ArbiterRunID = firstNonEmpty(input.ArbiterRunID, panel.ArbiterRunID)
	if input.ScoreA != nil {
		panel.ScoreA = input.ScoreA
	}
	if input.ScoreB != nil {
		panel.ScoreB = input.ScoreB
	}
	if input.ScoreC != nil {
		panel.ScoreC = input.ScoreC
	}
	if input.ScoreGap != nil {
		panel.ScoreGap = input.ScoreGap
	}
	if input.CriterionGap != nil {
		panel.CriterionGap = input.CriterionGap
	}
	panel.RequiredPointConflict = input.RequiredPointConflict
	panel.EvidenceConflict = input.EvidenceConflict
	panel.ConfidenceConflict = input.ConfidenceConflict
	panel.TriggerCodes = cloneStrings(input.TriggerCodes)
	if input.ResolvedScore != nil {
		panel.ResolvedScore = input.ResolvedScore
	}
	if input.ResolutionSource != "" {
		panel.ResolutionSource = input.ResolutionSource
	}
	if input.ReviewTaskID != "" {
		panel.ReviewTaskID = input.ReviewTaskID
	}
}

func unitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

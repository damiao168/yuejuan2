package subjective

import (
	"context"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/modelcalibration"
)

// EligibilityGate 是外部评分模型的受限准入边界。
// 隔离的旧版测试可不提供它；生产组装始终注入真实 A14 门禁。
type EligibilityGate interface {
	Decide(context.Context, string, aieligibility.DecisionInput) (aieligibility.Decision, error)
}

func (h *Handler) decideEligibility(ctx context.Context, tenantID, runID string, value Context, policy ModelPolicy) (aieligibility.Decision, bool, error) {
	if h.eligibility == nil {
		return aieligibility.Decision{}, true, nil
	}
	evaluation := aieligibility.EvaluationEvidence{}
	if h.evaluation != nil {
		evidence, err := h.evaluation.AdmissionEvidenceFor(ctx, tenantID, policy.ModelVersion, policy.PromptVersion, value.Rubric.Version, value.AssessmentSnapshot.SubjectCode, value.AssessmentSnapshot.ArchetypeCode)
		if err != nil {
			return aieligibility.Decision{}, false, err
		}
		evaluation = aieligibility.EvaluationEvidence{Approved: evidence.Approved, SampleCount: evidence.SampleCount, SevereErrorRate: evidence.SevereErrorRate, EvaluationRef: evidence.EvaluationRef}
	}
	calibration := aieligibility.CalibrationEvidence{}
	if h.calibration != nil {
		evidence, err := h.calibration.Approved(ctx, tenantID, modelcalibration.Axis{
			ModelReference: policy.ModelVersion, PromptVersion: policy.PromptVersion, RubricVersion: value.Rubric.Version,
			Subject: string(value.AssessmentSnapshot.SubjectCode), Archetype: value.AssessmentSnapshot.ArchetypeCode, SliceKey: "all",
		})
		if err != nil {
			return aieligibility.Decision{}, false, err
		}
		calibration = aieligibility.CalibrationEvidence{Available: evidence.Available, CalibrationRef: evidence.CalibrationRef}
	}
	var parserQuality *float64
	if h.mathV2Enabled && mathSubject(value) {
		if value.MathEvidence != nil {
			quality := value.MathEvidence.Quality.Critical
			parserQuality = &quality
		}
	} else if h.parserQuality != nil && strings.TrimSpace(value.SegmentID) != "" {
		quality, err := h.parserQuality.ParserQualityForSegment(ctx, tenantID, value.SegmentID, value.AssessmentSnapshot.SubjectCode, value.AssessmentSnapshot.ArchetypeCode)
		if err != nil {
			return aieligibility.Decision{}, false, err
		}
		parserQuality = quality
	}
	decision, err := h.eligibility.Decide(ctx, tenantID, aieligibility.DecisionInput{
		InputProtocol: imageInputProtocol(value), InputVersionID: value.InputVersionID,
		StudentObservationAvailable: value.InputVersionID != "" && value.Observation != nil,
		RunItemID:                   runID,
		AssessmentSnapshot:          value.AssessmentSnapshot,
		RequestedMode:               requestedScoringMode(value),
		OCRQuality:                  value.OCRConfidence,
		// 缺失时保持 nil，让 A14 安全地放弃判断，而不将通用 OCR 信号视为专用解析器结果的证据。
		ParserQuality:     parserQuality,
		RubricComplete:    rubricIsComplete(value),
		EvidenceAvailable: hasRequiredMaterialEvidence(value),
		Evaluation:        evaluation,
		Calibration:       calibration,
	})
	if err != nil {
		return aieligibility.Decision{}, false, err
	}
	return decision, decision.CanCallExternalAI(), nil
}

func requestedScoringMode(value Context) assessment.ScoringMode {
	mode := value.AssessmentSnapshot.ScoringPolicySnapshot.Mode
	if !mode.Valid() {
		return assessment.ScoringHumanPrimary
	}
	return mode
}

func rubricIsComplete(value Context) bool {
	if len(value.Rubric.Points) == 0 {
		return false
	}
	var total float64
	for _, point := range value.Rubric.Points {
		if strings.TrimSpace(point.ID) == "" || point.Score <= 0 {
			return false
		}
		total += point.Score
	}
	return total+0.000001 >= value.Question.Score
}

func hasRequiredMaterialEvidence(value Context) bool {
	if value.AssessmentSnapshot.SubjectCode != assessment.SubjectHistory && value.AssessmentSnapshot.SubjectCode != assessment.SubjectEthicsPolitics {
		return true
	}
	return strings.TrimSpace(value.AnswerText) != "" && len(value.AnswerImageRef) > 0
}

// recordCalibrationCandidate 将原始模型置信度与成绩事实分开保存，并应用 A15 基于经验风险覆盖率的弃权规则。
// 它只在响应通过结构和证据校验后运行；所有分支返回的建议仍只供教师复核。
func (h *Handler) recordCalibrationCandidate(ctx context.Context, tenantID, runID string, value Context, policy ModelPolicy, decision aieligibility.Decision, output *AdapterOutput) error {
	if value.InputVersionID != "" && output != nil {
		// 新视觉提示词尚无校准时仍可提供教师建议；不写入旧文本置信度候选，也不授予自动确认资格。
		output.NeedsHumanReview = true
		for _, item := range decision.Reasons {
			if item.Code == "suggestion_uncalibrated" || item.Code == "suggestion_evaluation_unapproved" {
				output.RiskFlags = appendFlag(output.RiskFlags, item.Code)
			}
		}
		return nil
	}
	if h.calibration == nil || output == nil || !decision.CanCallExternalAI() {
		return nil
	}
	targetRisk := decision.OutputConstraint.MaxSevereErrorRisk
	candidate, err := h.calibration.RecordCandidate(ctx, tenantID, modelcalibration.RecordCandidateInput{
		CandidateKey: runID,
		Axis: modelcalibration.Axis{
			ModelReference: policy.ModelVersion, PromptVersion: policy.PromptVersion, RubricVersion: value.Rubric.Version,
			Subject: string(value.AssessmentSnapshot.SubjectCode), Archetype: value.AssessmentSnapshot.ArchetypeCode, SliceKey: "all",
		},
		RawConfidence: output.Confidence, TargetRisk: &targetRisk,
	})
	if err != nil {
		return err
	}
	if output.RawOutput == nil {
		output.RawOutput = map[string]any{}
	}
	output.RawOutput["model_score_candidate"] = map[string]any{
		"id": candidate.ID, "raw_confidence": candidate.RawConfidence,
		"calibrated_confidence": candidate.CalibratedConfidence, "calibration_id": candidate.CalibrationID,
		"abstain_reason": candidate.AbstainReason,
	}
	if candidate.AbstainReason != "" {
		output.NeedsHumanReview = true
		output.RiskFlags = appendFlag(output.RiskFlags, "calibration_risk_coverage_abstained")
	}
	return nil
}

func eligibilityAbstentionOutput(policy ModelPolicy, decision aieligibility.Decision) AdapterOutput {
	flags := []string{"ai_eligibility_abstained"}
	for _, item := range decision.Reasons {
		flags = appendFlag(flags, item.Code)
	}
	return AdapterOutput{
		ModelVersion: policy.ModelVersion, PromptVersion: policy.PromptVersion,
		RiskFlags: flags, NeedsHumanReview: true,
		TeacherNote: "AI scoring was not admitted for this frozen question context; route to human review.",
		RawOutput: map[string]any{
			"eligibility_decision_id": decision.ID,
			"external_ai_allowed":     false,
			"reasons":                 decision.Reasons,
		},
	}
}

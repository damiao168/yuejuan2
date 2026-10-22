package aieligibility

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"testing"
)

func TestImageObservationReplacesOnlyOCRAndParserPrerequisites(t *testing.T) {
	input := inputFor("image-run", snapshotFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1), assessment.ScoringAIAssist)
	policyInput := policyFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1, []assessment.ScoringMode{assessment.ScoringAIAssist})
	policy := Policy{MinOCRQuality: policyInput.MinOCRQuality, MinParserQuality: policyInput.MinParserQuality, MinEvalN: policyInput.MinEvalN, MaxSevereErrorRate: policyInput.MaxSevereErrorRate}
	input.OCRQuality, input.ParserQuality = nil, nil
	if reason, blocked := admissionEvidenceBlock(input, policy); !blocked || reason.Code != "ocr_quality_below_policy" {
		t.Fatal("legacy text gate weakened")
	}
	input.InputProtocol, input.InputVersionID, input.StudentObservationAvailable = "grading-image-v3", "immutable-input", true
	if reason, blocked := admissionEvidenceBlock(input, policy); blocked {
		t.Fatalf("actual image observation incorrectly required OCR: %+v", reason)
	}
	input.Calibration.Available = false
	if reason, blocked := admissionEvidenceBlock(input, policy); !blocked || reason.Code != "calibration_unavailable" {
		t.Fatal("image bypassed calibration")
	}
	input.Calibration.Available = true
	input.StudentObservationAvailable = false
	if _, blocked := admissionEvidenceBlock(input, policy); !blocked {
		t.Fatal("unobserved image bypassed admission")
	}
}

func TestImageAIAssistCreatesTeacherSuggestionWithoutAutomaticConfirmationEvidence(t *testing.T) {
	service := NewService(NewMemoryStore())
	input := inputFor("image-suggestion", snapshotFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1), assessment.ScoringAIAssist)
	input.InputProtocol, input.InputVersionID, input.StudentObservationAvailable = "grading-image-v3", "immutable-input", true
	input.OCRQuality, input.ParserQuality = nil, nil
	input.Evaluation, input.Calibration = EvaluationEvidence{}, CalibrationEvidence{}
	decision, err := service.Decide(context.Background(), "tenant-image", input)
	if err != nil || !decision.ExternalAIAllowed || decision.Decision != assessment.ScoringAIAssist || decision.OutputConstraint.AllowModelFinalScore || decision.OutputConstraint.FinalScoreAuthority != "human_review" {
		t.Fatalf("teacher suggestion must remain executable and non-final: %+v err=%v", decision, err)
	}
	if len(decision.Reasons) != 3 || decision.Reasons[1].Blocking || decision.Reasons[2].Blocking {
		t.Fatalf("missing evaluation/calibration must remain visible: %+v", decision.Reasons)
	}
	// 同一新协议不能把题目原本的人工或快速确认策略改成建议路线。
	for _, mode := range []assessment.ScoringMode{assessment.ScoringHumanPrimary, assessment.ScoringAIFastConfirm} {
		input.RunItemID = "mode-" + string(mode)
		input.AssessmentSnapshot.ScoringPolicySnapshot.Mode = mode
		decision, err := service.Decide(context.Background(), "tenant-image", input)
		if err != nil || decision.ExternalAIAllowed {
			t.Fatalf("non-assist frozen mode bypassed its gate: %+v err=%v", decision, err)
		}
	}
	input.RunItemID = "incomplete-image-rubric"
	input.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
	input.RubricComplete = false
	decision, err = service.Decide(context.Background(), "tenant-image", input)
	if err != nil || decision.ExternalAIAllowed || decision.Reasons[0].Code != "rubric_incomplete" {
		t.Fatalf("incomplete frozen rubric admitted: %+v err=%v", decision, err)
	}
}

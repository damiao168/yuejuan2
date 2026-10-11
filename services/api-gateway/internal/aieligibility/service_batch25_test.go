package aieligibility

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func TestBatch25EligibilityKeepsFirstBlockingEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DecisionInput)
		code   string
	}{
		{"unknown OCR", func(in *DecisionInput) { in.OCRQuality = nil; in.ParserQuality = nil }, "ocr_quality_below_policy"},
		{"parser before rubric", func(in *DecisionInput) { in.ParserQuality = ptr(.8); in.RubricComplete = false }, "parser_quality_below_policy"},
		{"rubric before material", func(in *DecisionInput) { in.RubricComplete = false; in.EvidenceAvailable = false }, "rubric_incomplete"},
		{"material before evaluation", func(in *DecisionInput) { in.EvidenceAvailable = false; in.Evaluation.Approved = false }, "material_evidence_missing"},
		{"evaluation before calibration", func(in *DecisionInput) { in.Evaluation.SampleCount = 19; in.Calibration.Available = false }, "approved_evaluation_insufficient"},
		{"calibration", func(in *DecisionInput) { in.Calibration.Available = false }, "calibration_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service := NewService(NewMemoryStore())
			policy := policyFor(assessment.SubjectHistory, "short_constructed", assessment.RiskR2, []assessment.ScoringMode{assessment.ScoringAIAssist})
			// 零阈值不能把缺失的 OCR 质量当成已达标；其余用例沿用正常阈值。
			if tc.name == "unknown OCR" {
				policy.MinOCRQuality = 0
			}
			stored, err := service.PutPolicy(ctx, "tenant-a", policy)
			if err != nil {
				t.Fatal(err)
			}
			input := inputFor(tc.name, snapshotFor(assessment.SubjectHistory, "short_constructed", assessment.RiskR2), assessment.ScoringAIAssist)
			tc.change(&input)
			result, err := service.Decide(ctx, "tenant-a", input)
			if err != nil || len(result.Reasons) != 1 {
				t.Fatalf("decision=%#v err=%v", result, err)
			}
			if result.Reasons[0].Code != tc.code || !result.Reasons[0].Blocking || result.ExternalAIAllowed || result.Decision != assessment.ScoringHumanPrimary || result.PolicyID != stored.ID || result.PolicyVersion != stored.Version || result.OutputConstraint.AllowModelFinalScore {
				t.Fatalf("first block or policy/output audit changed: %#v", result)
			}
		})
	}
}

func TestBatch25EligibilityKeepsHumanAndRulePriority(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewMemoryStore())
	policy := policyFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1, []assessment.ScoringMode{assessment.ScoringRuleAuto})
	if _, err := service.PutPolicy(ctx, "tenant-a", policy); err != nil {
		t.Fatal(err)
	}
	humanInput := inputFor("human-priority", snapshotFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1), assessment.ScoringDualHuman)
	humanInput.OCRQuality, humanInput.RubricComplete = nil, false
	human, err := service.Decide(ctx, "tenant-a", humanInput)
	if err != nil || human.Decision != assessment.ScoringDualHuman || human.ExternalAIAllowed || len(human.Reasons) != 1 || human.Reasons[0].Code != "requested_human_mode" {
		t.Fatalf("human mode priority changed: %#v %v", human, err)
	}
	// 请求模式不在允许列表中，仍应先按已获准的结构化规则决策。
	ruleInput := inputFor("rule-priority", snapshotFor(assessment.SubjectMathematics, "numeric_expression", assessment.RiskR1), assessment.ScoringAIFastConfirm)
	rule, err := service.Decide(ctx, "tenant-a", ruleInput)
	if err != nil || rule.Decision != assessment.ScoringRuleAuto || rule.ExternalAIAllowed || rule.OutputConstraint.AllowModelFinalScore || len(rule.Reasons) != 1 || rule.Reasons[0].Code != "prefer_structured_rule" || rule.OutputConstraint.FinalScoreAuthority != "server_rubric_and_deterministic_rule" {
		t.Fatalf("structured rule priority changed: %#v %v", rule, err)
	}
}

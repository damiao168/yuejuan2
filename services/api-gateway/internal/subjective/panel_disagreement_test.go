package subjective

import (
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/grading"
)

func TestPanelSameScoreDifferentEvidenceStillRequiresArbitration(t *testing.T) {
	ctx := panelTestContext()
	grade := func(excerpt string) Grade {
		return Grade{
			SuggestedScore: 2, MaxScore: 4, Confidence: .9,
			MatchedPoints: []grading.PointResult{{Code: "p1", Score: 2}},
			MissingPoints: []grading.PointResult{{Code: "p2"}},
			Evidence:      []grading.Evidence{{RubricPointID: "p1", AnswerText: excerpt}},
		}
	}
	decision := ComparePrimaryGrades(grade("alpha"), grade("beta"), ctx.Rubric, PanelDecisionConfig{})
	if !decision.Facts.EvidenceConflict || !decision.RequiresArbitration || decision.PrimaryConsensus ||
		!containsString(decision.TriggerCodes, "evidence_conflict") {
		t.Fatalf("equal numeric scores hid evidence disagreement: %#v", decision)
	}
}

func TestPanelSameMathScoreDifferentArtifactEvidenceRequiresArbitration(t *testing.T) {
	ctx := panelTestContext()
	grade := func(source string) Grade {
		return Grade{SuggestedScore: 2, MaxScore: 4, Confidence: .9,
			MatchedPoints: []grading.PointResult{{Code: "p1", Score: 2}},
			MissingPoints: []grading.PointResult{{Code: "p2"}},
			Evidence:      []grading.Evidence{{Type: "math_artifact", RubricPointID: "p1", EvidenceID: "e1:" + source, Location: source}},
		}
	}
	decision := ComparePrimaryGrades(grade("formula-1"), grade("formula-2"), ctx.Rubric, PanelDecisionConfig{})
	if !decision.Facts.EvidenceConflict || !decision.RequiresArbitration || decision.PrimaryConsensus {
		t.Fatalf("equal math scores hid artifact disagreement: %#v", decision)
	}
}

func TestPanelDisagreementTriggersCoverScoreCriterionConfidenceRequiredAndRisk(t *testing.T) {
	ctx := panelTestContext()
	left := Grade{SuggestedScore: 4, MaxScore: 4, Confidence: .95,
		MatchedPoints: []grading.PointResult{{Code: "p1", Score: 2}, {Code: "p2", Score: 2}},
		Evidence: []grading.Evidence{
			{RubricPointID: "p1", AnswerText: "alpha"}, {RubricPointID: "p2", AnswerText: "beta"},
		}, RiskFlags: []string{"alternative_solution_candidate"},
	}
	right := Grade{SuggestedScore: 0, MaxScore: 4, Confidence: .4,
		MissingPoints: []grading.PointResult{{Code: "p1"}, {Code: "p2"}},
	}
	decision := ComparePrimaryGrades(left, right, ctx.Rubric, PanelDecisionConfig{})
	for _, code := range []string{"score_gap", "criterion_gap", "required_point_conflict", "confidence_conflict", "hard_risk:alternative_solution_candidate"} {
		if !containsString(decision.TriggerCodes, code) {
			t.Fatalf("missing trigger %q in %#v", code, decision)
		}
	}
	if decision.Facts.NormalizedScoreGap != 1 || decision.Facts.NormalizedCriterionGap != 1 || !decision.RequiresArbitration {
		t.Fatalf("unexpected disagreement facts: %#v", decision)
	}
}

func TestPanelDisagreementThresholdsAreFrozenPerDecision(t *testing.T) {
	ctx := panelTestContext()
	left := Grade{SuggestedScore: 2, MaxScore: 4, Confidence: .9,
		MatchedPoints: []grading.PointResult{{Code: "p1", Score: 2}}, MissingPoints: []grading.PointResult{{Code: "p2"}},
		Evidence: []grading.Evidence{{RubricPointID: "p1", AnswerText: "alpha"}},
	}
	right := Grade{SuggestedScore: 0, MaxScore: 4, Confidence: .9,
		MissingPoints: []grading.PointResult{{Code: "p1"}, {Code: "p2"}},
	}
	decision := ComparePrimaryGrades(left, right, ctx.Rubric, PanelDecisionConfig{ScoreGapThreshold: .75, CriterionGapThreshold: .75})
	if containsString(decision.TriggerCodes, "score_gap") || containsString(decision.TriggerCodes, "criterion_gap") ||
		!containsString(decision.TriggerCodes, "required_point_conflict") {
		t.Fatalf("decision ignored its supplied thresholds: %#v", decision)
	}
}

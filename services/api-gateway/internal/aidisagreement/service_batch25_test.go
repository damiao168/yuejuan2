package aidisagreement

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

func TestBatch25DifferenceClassificationPreservesIndependentGaps(t *testing.T) {
	for _, tc := range []struct {
		name     string
		score    float64
		evidence int
		criteria int
		want     DifferenceType
		material bool
	}{
		{"comparable", 0, 1, 1, "", false},
		{"score", 1, 1, 1, DifferenceScore, true},
		{"evidence", 0, 0, 1, DifferenceEvidence, true},
		{"criterion", 0, 1, 2, DifferenceCriterion, true},
		{"both gaps without score", 0, 0, 2, DifferenceEvidence, true},
		{"score and evidence", 1, 0, 1, DifferenceScoreEvidence, true},
		{"score and criterion", 1, 1, 2, DifferenceScoreCriterion, true},
		{"combined", 1, 0, 2, DifferenceCombined, true},
		{"rounded negligible gap", scoreEpsilon, 1, 1, "", false},
		{"smallest retained gap", .000001, 1, 1, DifferenceScore, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := comparison()
			source.AISuggestedScore, source.HumanScore = tc.score, 0
			source.AIEvidenceCount, source.AIMatchedCriterionCount, source.HumanCriterionCount = tc.evidence, tc.criteria, 1
			at := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
			item, material := calculate(source, at)
			if material != tc.material || item.DifferenceType != tc.want {
				t.Fatalf("classification = %q material=%v", item.DifferenceType, material)
			}
			if material && (item.AICandidateScore != source.AISuggestedScore || item.HumanScore != source.HumanScore || item.CreatedAt != at || item.UpdatedAt != at || !sort.StringsAreSorted(item.TriggerRules)) {
				t.Fatalf("source facts, timestamp or trigger order changed: %#v", item)
			}
		})
	}
}

func TestBatch25DifferenceSeverityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name     string
		score    float64
		maxScore float64
		risk     string
		severity Severity
		rules    []string
	}{
		{"normalized boundary", 2, 5, "R2", SeveritySevere, []string{"score_delta", "severe_score_delta"}},
		{"below normalized boundary", 1.9, 5, "R2", SeverityWarning, []string{"score_delta"}},
		{"risk priority boundary", 1, 10, "R3", SeveritySevere, []string{"r3_priority", "score_delta", "severe_score_delta"}},
		{"zero maximum fallback", .4, 0, "R2", SeveritySevere, []string{"score_delta", "severe_score_delta"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := comparison()
			source.AISuggestedScore, source.HumanScore, source.MaxScore = tc.score, 0, tc.maxScore
			source.AIConfidence, source.AIEvidenceCount, source.RiskTier = 0, 1, tc.risk
			item, material := calculate(source, time.Time{})
			if !material || item.Severity != tc.severity || !reflect.DeepEqual(item.TriggerRules, tc.rules) {
				t.Fatalf("severity=%s rules=%v material=%v", item.Severity, item.TriggerRules, material)
			}
		})
	}
}

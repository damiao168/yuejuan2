package gradingdisagreement

import "testing"

func TestCompareCombinesScoreCriterionEvidenceConfidenceAndRisk(t *testing.T) {
	left := Observation{Score: 8, MaxScore: 10, Confidence: .95, RiskCodes: []string{"ocr_low_confidence"}, Criteria: map[string]CriterionObservation{
		"p1": {Status: "supported", Score: 4, Weight: 4, Required: true, EvidenceFingerprints: []string{"same"}},
		"p2": {Status: "supported", Score: 4, Weight: 4, EvidenceFingerprints: []string{"left"}},
	}}
	right := Observation{Score: 4, MaxScore: 10, Confidence: .55, Criteria: map[string]CriterionObservation{
		"p1": {Status: "unsupported", Score: 0, Weight: 4, Required: true},
		"p2": {Status: "supported", Score: 4, Weight: 4, EvidenceFingerprints: []string{"right"}},
	}}
	result := Compare(left, right)
	if result.NormalizedScoreGap != .4 || result.NormalizedCriterionGap != .4 || !result.RequiredPointConflict || !result.EvidenceConflict || result.ConfidenceGap != .4 {
		t.Fatalf("unexpected disagreement: %#v", result)
	}
	if len(result.RiskCodes) != 1 || result.RiskCodes[0] != "ocr_low_confidence" {
		t.Fatalf("risk union was not stable: %#v", result.RiskCodes)
	}
}

func TestScoreDeltaSupportsAIHumanProjection(t *testing.T) {
	delta, absolute, normalized := ScoreDelta(7, 5, 10)
	if delta != 2 || absolute != 2 || normalized != .2 {
		t.Fatalf("unexpected score delta: %v %v %v", delta, absolute, normalized)
	}
}

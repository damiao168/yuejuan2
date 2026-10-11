package gradingdisagreement

import (
	"reflect"
	"testing"
)

func TestBatch26CompareSeparatesEvidenceFromWeightedDisagreement(t *testing.T) {
	left := Observation{MaxScore: 10, RiskCodes: []string{"z", "", "a", "z"}, Criteria: map[string]CriterionObservation{
		"evidence": {Status: "supported", Score: 2, Weight: 2, Required: true, EvidenceFingerprints: []string{"left"}},
		"missing":  {Status: "supported", Score: 4, Weight: 4, Required: true},
	}}
	right := Observation{MaxScore: 10, RiskCodes: []string{"a", "m"}, Criteria: map[string]CriterionObservation{
		"evidence": {Status: "supported", Score: 2, Weight: 6, EvidenceFingerprints: []string{"right"}},
	}}
	result := Compare(left, right)
	if result.NormalizedCriterionGap != .4 || !result.RequiredPointConflict || !result.EvidenceConflict || len(result.CriterionConflicts) != 2 {
		t.Fatalf("weighted and evidence facts changed: %#v", result)
	}
	if !reflect.DeepEqual(result.RiskCodes, []string{"a", "m", "z"}) || !reflect.DeepEqual(left.RiskCodes, []string{"z", "", "a", "z"}) || !reflect.DeepEqual(right.RiskCodes, []string{"a", "m"}) {
		t.Fatalf("risk ordering or input changed: %#v", result.RiskCodes)
	}
	want := []CriterionConflict{
		{CriterionID: "evidence", Weight: 6, Required: true, EvidenceConflict: true},
		{CriterionID: "missing", Weight: 4, Required: true, StatusConflict: true, ScoreConflict: true},
	}
	if !reflect.DeepEqual(result.CriterionConflicts, want) {
		t.Fatalf("criterion facts or sorting changed: %#v", result.CriterionConflicts)
	}
}

func TestBatch26CompareKeepsEmptyRiskAndMatchingCriteria(t *testing.T) {
	left := Observation{Criteria: map[string]CriterionObservation{"a": {Status: "supported", Score: 1, Weight: 2}}}
	right := left
	result := Compare(left, right)
	if result.RiskCodes != nil || result.CriterionConflicts != nil || result.EvidenceConflict || result.RequiredPointConflict || result.NormalizedCriterionGap != 0 {
		t.Fatalf("empty/matching observation changed: %#v", result)
	}
}

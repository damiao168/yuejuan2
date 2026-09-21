// Package gradingdisagreement computes immutable disagreement facts between
// any two rubric-aligned grading observations. Routing policy stays with the
// caller, allowing the same engine to support AI-human and AI-A/AI-B checks.
package gradingdisagreement

import (
	"math"
	"sort"
)

const epsilon = 1e-9

type CriterionObservation struct {
	Status               string
	Score                float64
	Weight               float64
	Required             bool
	EvidenceFingerprints []string
}

type Observation struct {
	Score      float64
	MaxScore   float64
	Confidence float64
	Criteria   map[string]CriterionObservation
	RiskCodes  []string
}

type CriterionConflict struct {
	CriterionID      string  `json:"criterion_id"`
	Weight           float64 `json:"weight"`
	Required         bool    `json:"required"`
	StatusConflict   bool    `json:"status_conflict"`
	ScoreConflict    bool    `json:"score_conflict"`
	EvidenceConflict bool    `json:"evidence_conflict"`
}

type Result struct {
	Delta                  float64             `json:"delta"`
	AbsoluteDelta          float64             `json:"absolute_delta"`
	NormalizedScoreGap     float64             `json:"normalized_score_gap"`
	NormalizedCriterionGap float64             `json:"normalized_criterion_gap"`
	ConfidenceGap          float64             `json:"confidence_gap"`
	RequiredPointConflict  bool                `json:"required_point_conflict"`
	EvidenceConflict       bool                `json:"evidence_conflict"`
	CriterionConflicts     []CriterionConflict `json:"criterion_conflicts"`
	RiskCodes              []string            `json:"risk_codes"`
}

func ScoreDelta(left, right, maxScore float64) (delta, absolute, normalized float64) {
	delta = left - right
	absolute = math.Abs(delta)
	if maxScore > 0 {
		normalized = absolute / maxScore
	}
	return rounded(delta), rounded(absolute), rounded(normalized)
}

func Compare(left, right Observation) Result {
	maxScore := math.Max(left.MaxScore, right.MaxScore)
	delta, absolute, normalized := ScoreDelta(left.Score, right.Score, maxScore)
	result := Result{Delta: delta, AbsoluteDelta: absolute, NormalizedScoreGap: normalized, ConfidenceGap: rounded(math.Abs(left.Confidence - right.Confidence))}
	ids := map[string]bool{}
	for id := range left.Criteria {
		ids[id] = true
	}
	for id := range right.Criteria {
		ids[id] = true
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	var conflictingWeight float64
	for _, id := range ordered {
		leftCriterion, leftOK := left.Criteria[id]
		rightCriterion, rightOK := right.Criteria[id]
		weight := math.Max(leftCriterion.Weight, rightCriterion.Weight)
		required := leftCriterion.Required || rightCriterion.Required
		statusConflict := !leftOK || !rightOK || leftCriterion.Status != rightCriterion.Status
		scoreConflict := !leftOK || !rightOK || math.Abs(leftCriterion.Score-rightCriterion.Score) > epsilon
		evidenceConflict := leftOK && rightOK && leftCriterion.Status == "supported" && rightCriterion.Status == "supported" &&
			!overlaps(leftCriterion.EvidenceFingerprints, rightCriterion.EvidenceFingerprints)
		if !statusConflict && !scoreConflict && !evidenceConflict {
			continue
		}
		if statusConflict || scoreConflict {
			conflictingWeight += weight
			result.RequiredPointConflict = result.RequiredPointConflict || required
		}
		result.EvidenceConflict = result.EvidenceConflict || evidenceConflict
		result.CriterionConflicts = append(result.CriterionConflicts, CriterionConflict{
			CriterionID: id, Weight: weight, Required: required, StatusConflict: statusConflict,
			ScoreConflict: scoreConflict, EvidenceConflict: evidenceConflict,
		})
	}
	if maxScore > 0 {
		result.NormalizedCriterionGap = rounded(math.Min(1, conflictingWeight/maxScore))
	}
	riskSet := map[string]bool{}
	for _, code := range append(append([]string{}, left.RiskCodes...), right.RiskCodes...) {
		if code != "" {
			riskSet[code] = true
		}
	}
	for code := range riskSet {
		result.RiskCodes = append(result.RiskCodes, code)
	}
	sort.Strings(result.RiskCodes)
	return result
}

func overlaps(left, right []string) bool {
	if len(left) == 0 && len(right) == 0 {
		return true
	}
	seen := map[string]bool{}
	for _, value := range left {
		seen[value] = true
	}
	for _, value := range right {
		if seen[value] {
			return true
		}
	}
	return false
}

func rounded(value float64) float64 { return math.Round(value*1e6) / 1e6 }

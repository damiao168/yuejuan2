// Package gradingdisagreement 计算任意两份按评分细则对齐的评分观察之间的不可变分歧事实。
// 路由策略由调用方负责，使同一引擎支持 AI 与人工及 AI-A/AI-B 比较。
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

// ScoreDelta 的方向为左分数减右分数；归一化差值除以满分，满分非正时返回 0。
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
		conflict := compareCriterion(id, leftCriterion, rightCriterion, leftOK, rightOK)
		if !conflict.StatusConflict && !conflict.ScoreConflict && !conflict.EvidenceConflict {
			continue
		}
		if conflict.StatusConflict || conflict.ScoreConflict {
			conflictingWeight += conflict.Weight
			result.RequiredPointConflict = result.RequiredPointConflict || conflict.Required
		}
		result.EvidenceConflict = result.EvidenceConflict || conflict.EvidenceConflict
		result.CriterionConflicts = append(result.CriterionConflicts, conflict)
	}
	if maxScore > 0 {
		result.NormalizedCriterionGap = rounded(math.Min(1, conflictingWeight/maxScore))
	}
	result.RiskCodes = combinedRiskCodes(left.RiskCodes, right.RiskCodes)
	return result
}

func compareCriterion(id string, left, right CriterionObservation, leftOK, rightOK bool) CriterionConflict {
	// 只有双方都声称支持该评分点时才比较证据，状态分歧另行记录。
	evidenceConflict := leftOK && rightOK && left.Status == "supported" && right.Status == "supported" &&
		!overlaps(left.EvidenceFingerprints, right.EvidenceFingerprints)
	return CriterionConflict{
		CriterionID: id, Weight: math.Max(left.Weight, right.Weight), Required: left.Required || right.Required,
		StatusConflict:   !leftOK || !rightOK || left.Status != right.Status,
		ScoreConflict:    !leftOK || !rightOK || math.Abs(left.Score-right.Score) > epsilon,
		EvidenceConflict: evidenceConflict,
	}
}

func combinedRiskCodes(left, right []string) []string {
	riskSet := map[string]bool{}
	for _, code := range append(append([]string{}, left...), right...) {
		if code != "" {
			riskSet[code] = true
		}
	}
	var codes []string
	for code := range riskSet {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	return codes
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

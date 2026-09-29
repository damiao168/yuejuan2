package subjective

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/gradingdisagreement"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type PanelDisagreementDecision struct {
	Facts               gradingdisagreement.Result
	TriggerCodes        []string
	RequiresArbitration bool
	PrimaryConsensus    bool
}

// 分歧按分数、量规判定、证据、置信度和硬风险分别计算；证据冲突即使分数相同也不能直接达成共识。
func ComparePrimaryGrades(left, right Grade, rubric paper.Rubric, config PanelDecisionConfig) PanelDisagreementDecision {
	config = NormalizePanelDecisionConfig(config)
	facts := gradingdisagreement.Compare(panelObservation(left, rubric), panelObservation(right, rubric))
	triggers := []string{}
	if facts.NormalizedScoreGap >= config.ScoreGapThreshold {
		triggers = append(triggers, "score_gap")
	}
	if facts.NormalizedCriterionGap >= config.CriterionGapThreshold {
		triggers = append(triggers, "criterion_gap")
	}
	if facts.RequiredPointConflict {
		triggers = append(triggers, "required_point_conflict")
	}
	if config.TriggerAnyCriterionConflict && hasScoringCriterionConflict(facts) {
		triggers = append(triggers, "criterion_conflict")
	}
	if config.TriggerEvidenceConflict && facts.EvidenceConflict {
		triggers = append(triggers, "evidence_conflict")
	}
	if facts.ConfidenceGap >= config.ConfidenceGapThreshold {
		triggers = append(triggers, "confidence_conflict")
	}
	hardRisks := stringSet(config.HardRiskCodes)
	for _, code := range facts.RiskCodes {
		if hardRisks[code] {
			triggers = append(triggers, "hard_risk:"+code)
		}
	}
	triggers = uniqueSorted(triggers)
	primaryConsensus := facts.AbsoluteDelta <= 1e-9 && !hasScoringCriterionConflict(facts) && !facts.EvidenceConflict
	// A low-weight scoring conflict below calibrated thresholds is deliberately
	// not averaged away. It still needs a blind arbiter or a human.
	requiresArbitration := len(triggers) > 0 || !primaryConsensus
	if !primaryConsensus && len(triggers) == 0 {
		triggers = []string{"unresolved_criterion_conflict"}
	}
	return PanelDisagreementDecision{Facts: facts, TriggerCodes: triggers, RequiresArbitration: requiresArbitration, PrimaryConsensus: primaryConsensus}
}

func ArbiterAgrees(arbiter, primary Grade, rubric paper.Rubric) float64 {
	left := panelObservation(arbiter, rubric)
	right := panelObservation(primary, rubric)
	var agreed, total float64
	for _, point := range rubric.Points {
		weight := math.Max(point.Score, 0)
		total += weight
		leftPoint, leftOK := left.Criteria[point.ID]
		rightPoint, rightOK := right.Criteria[point.ID]
		if leftOK && rightOK && leftPoint.Status == rightPoint.Status && math.Abs(leftPoint.Score-rightPoint.Score) <= 1e-9 {
			agreed += weight
		}
	}
	if total <= 0 {
		return 0
	}
	return math.Round(agreed/total*1e6) / 1e6
}

func panelObservation(grade Grade, rubric paper.Rubric) gradingdisagreement.Observation {
	criteria := make(map[string]gradingdisagreement.CriterionObservation, len(rubric.Points))
	evidenceByPoint := map[string][]string{}
	for _, evidence := range grade.Evidence {
		if evidence.RubricPointID == "" {
			continue
		}
		normalized := normalizeEvidenceText(evidence.AnswerText)
		if normalized == "" && evidence.Type == "math_artifact" {
			// Mathematical evidence is a verified artifact link rather than a
			// text excerpt. Its source identity must still affect agreement.
			normalized = strings.TrimSpace(evidence.EvidenceID) + ":" + strings.TrimSpace(evidence.Location)
		}
		if normalized == "" {
			continue
		}
		sum := sha256.Sum256([]byte(normalized))
		evidenceByPoint[evidence.RubricPointID] = append(evidenceByPoint[evidence.RubricPointID], hex.EncodeToString(sum[:16]))
	}
	rubricByID := map[string]paper.RubricPoint{}
	for _, point := range rubric.Points {
		rubricByID[point.ID] = point
	}
	for _, point := range grade.MatchedPoints {
		rubricPoint := rubricByID[point.Code]
		criteria[point.Code] = gradingdisagreement.CriterionObservation{
			Status: "supported", Score: point.Score, Weight: rubricPoint.Score, Required: rubricPoint.Required,
			EvidenceFingerprints: uniqueSorted(evidenceByPoint[point.Code]),
		}
	}
	for _, point := range grade.MissingPoints {
		rubricPoint := rubricByID[point.Code]
		criteria[point.Code] = gradingdisagreement.CriterionObservation{Status: "unsupported", Weight: rubricPoint.Score, Required: rubricPoint.Required}
	}
	return gradingdisagreement.Observation{Score: grade.SuggestedScore, MaxScore: grade.MaxScore, Confidence: grade.Confidence, Criteria: criteria, RiskCodes: cloneStrings(grade.RiskFlags)}
}

func hasScoringCriterionConflict(result gradingdisagreement.Result) bool {
	for _, conflict := range result.CriterionConflicts {
		if conflict.StatusConflict || conflict.ScoreConflict {
			return true
		}
	}
	return false
}

func containsHardRisk(risks, hard []string) bool {
	set := stringSet(hard)
	for _, risk := range risks {
		if set[risk] {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func uniqueSorted(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

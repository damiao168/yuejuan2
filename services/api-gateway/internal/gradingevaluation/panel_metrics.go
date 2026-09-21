package gradingevaluation

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

type PanelEvaluationStore interface {
	AddPanelObservation(context.Context, string, string, PanelObservation) (PanelObservation, error)
	ListPanelObservations(context.Context, string, string) ([]PanelObservation, error)
}

var (
	panelResponseKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	panelFingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

var panelSubjectCodes = map[string]bool{
	"chinese": true, "mathematics": true, "english": true, "physics": true,
	"chemistry": true, "biology": true, "history": true, "geography": true,
	"ethics_politics": true,
}

type PanelObservation struct {
	ID                   string        `json:"id,omitempty"`
	RunID                string        `json:"run_id,omitempty"`
	ResponseKey          string        `json:"response_key"`
	ResponseFingerprint  string        `json:"response_fingerprint"`
	EducationStage       string        `json:"education_stage"`
	Subject              string        `json:"subject"`
	Archetype            string        `json:"archetype"`
	ReferenceKind        ReferenceKind `json:"reference_kind"`
	ReferenceScore       float64       `json:"reference_score"`
	MaxScore             float64       `json:"max_score"`
	ScoreA               float64       `json:"score_a"`
	ScoreB               float64       `json:"score_b"`
	ScoreC               *float64      `json:"score_c,omitempty"`
	ResolvedScore        *float64      `json:"resolved_score,omitempty"`
	ArbitrationTriggered bool          `json:"arbitration_triggered"`
	ResolutionSource     string        `json:"resolution_source"`
	HumanEscalated       bool          `json:"human_escalated"`
	ReferenceReviewers   int           `json:"reference_reviewer_count"`
	ReferenceAdjudicated bool          `json:"reference_adjudicated"`
	PrimaryACostMicros   int64         `json:"primary_a_cost_micros"`
	PrimaryBCostMicros   int64         `json:"primary_b_cost_micros"`
	ArbiterCostMicros    int64         `json:"arbiter_cost_micros"`
	ObservedAt           time.Time     `json:"observed_at,omitempty"`
}

type PanelMetrics struct {
	SampleCount              int     `json:"sample_count"`
	PrimaryA                 Metrics `json:"primary_a"`
	PrimaryB                 Metrics `json:"primary_b"`
	ArbitratedPrimaryA       Metrics `json:"arbitrated_primary_a"`
	ArbitratedPrimaryB       Metrics `json:"arbitrated_primary_b"`
	Arbiter                  Metrics `json:"arbiter"`
	Resolved                 Metrics `json:"resolved"`
	ABExactAgreementRate     float64 `json:"ab_exact_agreement_rate"`
	FalseConsensusRate       float64 `json:"false_consensus_rate"`
	FalseConsensusCount      int     `json:"false_consensus_count"`
	ArbitrationTriggerRate   float64 `json:"arbitration_trigger_rate"`
	ArbitrationRescueRate    float64 `json:"arbitration_rescue_rate"`
	ArbitrationErrorRate     float64 `json:"arbitration_error_rate"`
	HumanEscalationRate      float64 `json:"human_escalation_rate"`
	CostPer1000AnswersMicros int64   `json:"cost_per_1000_answers_micros"`
}

type PanelSliceMetrics struct {
	Subject   string       `json:"subject"`
	Archetype string       `json:"archetype"`
	Metrics   PanelMetrics `json:"metrics"`
}

func CalculateStoredPanelMetrics(ctx context.Context, store PanelEvaluationStore, tenantID, runID string) (PanelMetrics, error) {
	if store == nil || strings.TrimSpace(tenantID) == "" || strings.TrimSpace(runID) == "" {
		return PanelMetrics{}, ErrInvalidInput
	}
	items, err := store.ListPanelObservations(ctx, tenantID, runID)
	if err != nil {
		return PanelMetrics{}, err
	}
	return CalculatePanelMetrics(items)
}

func CalculatePanelMetrics(items []PanelObservation) (PanelMetrics, error) {
	result := PanelMetrics{SampleCount: len(items)}
	if len(items) == 0 {
		result.PrimaryA = calculateMetrics(nil)
		result.PrimaryB = calculateMetrics(nil)
		result.ArbitratedPrimaryA = calculateMetrics(nil)
		result.ArbitratedPrimaryB = calculateMetrics(nil)
		result.Arbiter = calculateMetrics(nil)
		result.Resolved = calculateMetrics(nil)
		return result, nil
	}
	aItems, bItems, arbitratedAItems, arbitratedBItems, cItems, resolvedItems := []Observation{}, []Observation{}, []Observation{}, []Observation{}, []Observation{}, []Observation{}
	var abExact, falseConsensus, arbitrations, rescues, arbiterErrors, humanEscalations int
	var totalCost int64
	for _, item := range items {
		if !validPanelObservation(item) {
			return PanelMetrics{}, ErrInvalidInput
		}
		aItems = append(aItems, metricObservation(item.ResponseKey+":a", item.ReferenceScore, item.ScoreA, item.MaxScore))
		bItems = append(bItems, metricObservation(item.ResponseKey+":b", item.ReferenceScore, item.ScoreB, item.MaxScore))
		if math.Abs(item.ScoreA-item.ScoreB) <= scoreEpsilon {
			abExact++
			if math.Abs(item.ScoreA-item.ReferenceScore) > scoreEpsilon {
				falseConsensus++
			}
		}
		if item.ArbitrationTriggered {
			arbitrations++
		}
		if item.ScoreC != nil {
			// Compare C with both primaries on exactly the cases C actually
			// rated; human-only escalations have no C score to compare.
			arbitratedAItems = append(arbitratedAItems, metricObservation(item.ResponseKey+":arbitrated:a", item.ReferenceScore, item.ScoreA, item.MaxScore))
			arbitratedBItems = append(arbitratedBItems, metricObservation(item.ResponseKey+":arbitrated:b", item.ReferenceScore, item.ScoreB, item.MaxScore))
			cItems = append(cItems, metricObservation(item.ResponseKey+":c", item.ReferenceScore, *item.ScoreC, item.MaxScore))
			if math.Abs(*item.ScoreC-item.ReferenceScore) <= scoreEpsilon &&
				(math.Abs(item.ScoreA-item.ReferenceScore) > scoreEpsilon || math.Abs(item.ScoreB-item.ReferenceScore) > scoreEpsilon) {
				rescues++
			}
			if math.Abs(*item.ScoreC-item.ReferenceScore) > scoreEpsilon {
				arbiterErrors++
			}
		}
		if item.ResolvedScore != nil {
			resolvedItems = append(resolvedItems, metricObservation(item.ResponseKey+":resolved", item.ReferenceScore, *item.ResolvedScore, item.MaxScore))
		}
		if item.HumanEscalated {
			humanEscalations++
		}
		totalCost += item.PrimaryACostMicros + item.PrimaryBCostMicros + item.ArbiterCostMicros
	}
	result.PrimaryA, result.PrimaryB = calculateMetrics(aItems), calculateMetrics(bItems)
	result.ArbitratedPrimaryA, result.ArbitratedPrimaryB = calculateMetrics(arbitratedAItems), calculateMetrics(arbitratedBItems)
	result.Arbiter, result.Resolved = calculateMetrics(cItems), calculateMetrics(resolvedItems)
	result.ABExactAgreementRate = rounded(float64(abExact) / float64(len(items)))
	result.FalseConsensusCount = falseConsensus
	if abExact > 0 {
		result.FalseConsensusRate = rounded(float64(falseConsensus) / float64(abExact))
	}
	result.ArbitrationTriggerRate = rounded(float64(arbitrations) / float64(len(items)))
	if len(cItems) > 0 {
		result.ArbitrationRescueRate = rounded(float64(rescues) / float64(len(cItems)))
		result.ArbitrationErrorRate = rounded(float64(arbiterErrors) / float64(len(cItems)))
	}
	result.HumanEscalationRate = rounded(float64(humanEscalations) / float64(len(items)))
	result.CostPer1000AnswersMicros = int64(math.Round(float64(totalCost) * 1000 / float64(len(items))))
	return result, nil
}

func CalculatePanelMetricsBySlice(items []PanelObservation) ([]PanelSliceMetrics, error) {
	groups := map[string][]PanelObservation{}
	for _, item := range items {
		if !validPanelObservation(item) || !panelSubjectCodes[item.Subject] || strings.TrimSpace(item.Archetype) == "" {
			return nil, ErrInvalidInput
		}
		key := item.Subject + "\x00" + item.Archetype
		groups[key] = append(groups[key], item)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]PanelSliceMetrics, 0, len(keys))
	for _, key := range keys {
		metrics, err := CalculatePanelMetrics(groups[key])
		if err != nil {
			return nil, err
		}
		parts := strings.SplitN(key, "\x00", 2)
		result = append(result, PanelSliceMetrics{Subject: parts[0], Archetype: parts[1], Metrics: metrics})
	}
	return result, nil
}

func validPanelObservation(item PanelObservation) bool {
	validScore := func(value float64) bool {
		return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= item.MaxScore
	}
	if item.ResponseKey == "" || item.ReferenceKind != ReferenceHumanAdjudicated || item.ReferenceReviewers < 2 || !item.ReferenceAdjudicated ||
		item.MaxScore <= 0 || !validScore(item.ReferenceScore) || !validScore(item.ScoreA) || !validScore(item.ScoreB) ||
		item.PrimaryACostMicros < 0 || item.PrimaryBCostMicros < 0 || item.ArbiterCostMicros < 0 {
		return false
	}
	if item.ScoreC != nil && !validScore(*item.ScoreC) {
		return false
	}
	if item.ResolvedScore != nil && !validScore(*item.ResolvedScore) {
		return false
	}
	if item.ResolutionSource != "primary_consensus" && item.ResolutionSource != "arbiter" && item.ResolutionSource != "human" {
		return false
	}
	if (item.ResolutionSource == "human") != item.HumanEscalated ||
		(item.ScoreC != nil && !item.ArbitrationTriggered) {
		return false
	}
	switch item.ResolutionSource {
	case "primary_consensus":
		return !item.ArbitrationTriggered && math.Abs(item.ScoreA-item.ScoreB) <= scoreEpsilon &&
			item.ResolvedScore != nil && math.Abs(*item.ResolvedScore-item.ScoreA) <= scoreEpsilon
	case "arbiter":
		return item.ArbitrationTriggered && item.ScoreC != nil && item.ResolvedScore != nil &&
			math.Abs(*item.ResolvedScore-*item.ScoreC) <= scoreEpsilon
	default:
		return item.ResolvedScore == nil
	}
}

func validPersistedPanelObservation(item PanelObservation) bool {
	return validPanelObservation(item) && panelResponseKeyPattern.MatchString(item.ResponseKey) &&
		panelFingerprintPattern.MatchString(item.ResponseFingerprint) && assessment.EducationStage(item.EducationStage).Valid() && panelSubjectCodes[item.Subject] &&
		strings.TrimSpace(item.Archetype) != "" && len(item.Archetype) <= 128
}

func clonePanelObservation(item PanelObservation) PanelObservation {
	if item.ScoreC != nil {
		value := *item.ScoreC
		item.ScoreC = &value
	}
	if item.ResolvedScore != nil {
		value := *item.ResolvedScore
		item.ResolvedScore = &value
	}
	return item
}

func metricObservation(key string, reference, model, maxScore float64) Observation {
	return Observation{ResponseKey: key, ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: reference, ModelScore: model, MaxScore: maxScore, ReferenceReviewers: 2, ReferenceAdjudicated: true}
}

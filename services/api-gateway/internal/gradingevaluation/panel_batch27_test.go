package gradingevaluation

import (
	"reflect"
	"testing"
)

func TestBatch27PanelMetricsKeepsConsensusAndRatedArbiterDenominators(t *testing.T) {
	items := []PanelObservation{}
	for _, id := range []string{"consensus", "rescue", "error", "human"} {
		items = append(items, PanelObservation{ResponseKey: id, ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 1, MaxScore: 3,
			ScoreA: 1, ScoreB: 2, ResolutionSource: "human", HumanEscalated: true,
			ReferenceReviewers: 2, ReferenceAdjudicated: true, PrimaryACostMicros: 10, PrimaryBCostMicros: 20})
	}
	zero, one, two := 0.0, 1.0, 2.0
	items[0].ScoreA, items[0].ScoreB, items[0].ResolvedScore = 0, 0, &zero
	items[0].ResolutionSource, items[0].HumanEscalated = "primary_consensus", false
	items[1].ScoreA, items[1].ScoreB = 0, 2
	items[1].ScoreC, items[1].ResolvedScore = &one, &one
	items[2].ScoreA, items[2].ScoreB = 1, 0
	items[2].ScoreC, items[2].ResolvedScore = &two, &two
	for _, index := range []int{1, 2} {
		items[index].ResolutionSource, items[index].HumanEscalated = "arbiter", false
		items[index].ArbitrationTriggered, items[index].ArbiterCostMicros = true, 10
	}
	items[3].ArbitrationTriggered = true
	got, err := CalculatePanelMetrics(items)
	if err != nil || got.SampleCount != 4 || got.PrimaryA.SampleCount != 4 || got.PrimaryB.SampleCount != 4 ||
		got.ArbitratedPrimaryA.SampleCount != 2 || got.ArbitratedPrimaryB.SampleCount != 2 || got.Arbiter.SampleCount != 2 || got.Resolved.SampleCount != 3 ||
		got.ABExactAgreementRate != .25 || got.FalseConsensusCount != 1 || got.FalseConsensusRate != 1 ||
		got.ArbitrationTriggerRate != .75 || got.ArbitrationRescueRate != .5 || got.ArbitrationErrorRate != .5 || got.HumanEscalationRate != .25 || got.CostPer1000AnswersMicros != 35000 {
		t.Fatalf("panel facts=%#v err=%v", got, err)
	}
	items[1].ReferenceReviewers = 1
	if got, err := CalculatePanelMetrics(items); err != ErrInvalidInput || !reflect.DeepEqual(got, PanelMetrics{}) {
		t.Fatalf("invalid later observation returned partial facts: %#v %v", got, err)
	}
	if got, err := CalculatePanelMetrics(nil); err != nil || got.SampleCount != 0 || got.Arbiter.SampleCount != 0 || got.FalseConsensusRate != 0 {
		t.Fatalf("empty observations changed: %#v %v", got, err)
	}
}

func TestBatch27PanelReadinessKeepsEveryFailureAndExactThresholds(t *testing.T) {
	qwk := .2
	policy := PanelReadinessPolicy{PolicyVersion: "synthetic-27", MinSamplesPerSlice: 5, MinArbitrationsPerSlice: 3,
		MaxFalseConsensusRate: .5, MaxArbitrationErrorRate: .5, MaxResolvedMAE: 1, MinResolvedQWK: .5, MinArbiterMAEImprovement: .2,
		MinResolvedCoverageRate: .5, MaxHumanEscalationRate: .5, MaxArbitrationTriggerRate: .5, MaxCostPer1000AnswersMicros: 2}
	metrics := PanelMetrics{SampleCount: 4, Arbiter: Metrics{SampleCount: 2}, Resolved: Metrics{SampleCount: 1, MAE: 2, QWK: &qwk, QWKAvailable: true},
		FalseConsensusRate: .6, ArbitrationErrorRate: .6, HumanEscalationRate: .6, ArbitrationTriggerRate: .6, CostPer1000AnswersMicros: 3}
	want := []string{"arbitration_error_rate_exceeded", "arbitration_trigger_rate_exceeded", "cost_per_1000_exceeded", "false_consensus_rate_exceeded",
		"human_escalation_rate_exceeded", "insufficient_arbitrations", "insufficient_samples", "resolved_coverage_below_threshold", "resolved_mae_exceeded", "resolved_qwk_below_threshold"}
	if got := panelSliceReadinessReasons(metrics, policy); !reflect.DeepEqual(got, want) {
		t.Fatalf("failure codes or sorting changed: %v", got)
	}
	metrics.Arbiter.SampleCount = 3
	metrics.Resolved.QWK = nil
	want = []string{"arbiter_mae_improvement_below_threshold", "arbitration_error_rate_exceeded", "arbitration_trigger_rate_exceeded", "cost_per_1000_exceeded",
		"false_consensus_rate_exceeded", "human_escalation_rate_exceeded", "insufficient_samples", "resolved_coverage_below_threshold", "resolved_mae_exceeded", "resolved_qwk_unavailable"}
	if got := panelSliceReadinessReasons(metrics, policy); !reflect.DeepEqual(got, want) {
		t.Fatalf("arbitration minimum or missing QWK changed: %v", got)
	}
	qwk = .5
	metrics = PanelMetrics{SampleCount: 5, Arbiter: Metrics{SampleCount: 3}, ArbitratedPrimaryA: Metrics{MAE: .2}, ArbitratedPrimaryB: Metrics{MAE: .3},
		Resolved: Metrics{SampleCount: 3, MAE: 1, QWK: &qwk, QWKAvailable: true}, FalseConsensusRate: .5, ArbitrationErrorRate: .5,
		HumanEscalationRate: .5, ArbitrationTriggerRate: .5, CostPer1000AnswersMicros: 2}
	if got := panelSliceReadinessReasons(metrics, policy); len(got) != 0 {
		t.Fatalf("exact approved thresholds rejected: %v", got)
	}
}

package gradingevaluation

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCalculatePanelMetricsIncludesFalseConsensusRescueEscalationAndCost(t *testing.T) {
	three, four := 3.0, 4.0
	items := []PanelObservation{
		{ResponseKey: "r1", ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 4, MaxScore: 5, ScoreA: 3, ScoreB: 3, ResolvedScore: &three, ResolutionSource: "primary_consensus", ReferenceReviewers: 2, ReferenceAdjudicated: true, PrimaryACostMicros: 10, PrimaryBCostMicros: 10},
		{ResponseKey: "r2", ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 4, MaxScore: 5, ScoreA: 2, ScoreB: 3, ScoreC: &four, ResolvedScore: &four, ArbitrationTriggered: true, ResolutionSource: "arbiter", ReferenceReviewers: 2, ReferenceAdjudicated: true, PrimaryACostMicros: 10, PrimaryBCostMicros: 10, ArbiterCostMicros: 30},
		{ResponseKey: "r3", ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 2, MaxScore: 5, ScoreA: 1, ScoreB: 3, ArbitrationTriggered: true, ResolutionSource: "human", HumanEscalated: true, ReferenceReviewers: 2, ReferenceAdjudicated: true, PrimaryACostMicros: 10, PrimaryBCostMicros: 10, ArbiterCostMicros: 30},
	}
	metrics, err := CalculatePanelMetrics(items)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.FalseConsensusCount != 1 || metrics.FalseConsensusRate != 1 || metrics.ArbitrationTriggerRate != .666667 || metrics.ArbitrationRescueRate != 1 || metrics.HumanEscalationRate != .333333 {
		t.Fatalf("unexpected panel metrics: %#v", metrics)
	}
	if metrics.PrimaryA.MAE != 1.333333 || metrics.PrimaryB.MAE != 1 || metrics.Arbiter.MAE != 0 || metrics.CostPer1000AnswersMicros != 40000 {
		t.Fatalf("unexpected role/cost metrics: %#v", metrics)
	}
	if metrics.ArbitratedPrimaryA.SampleCount != 1 || metrics.ArbitratedPrimaryB.SampleCount != 1 || metrics.Arbiter.SampleCount != 1 {
		t.Fatalf("C was not compared with A/B on the same rated subset: %#v", metrics)
	}
}

func TestPanelEvaluationObservationsAreValidatedPersistedAndImmutableAfterCompletion(t *testing.T) {
	store := NewMemoryStore()
	run, err := store.CreateRun(context.Background(), "tenant-1", "actor-1", CreateRunInput{Key: "panel-run"})
	if err != nil {
		t.Fatal(err)
	}
	resolved := 4.0
	input := PanelObservation{
		ResponseKey: "response-1", ResponseFingerprint: strings.Repeat("a", 64), EducationStage: "senior", Subject: "mathematics", Archetype: "structured_steps",
		ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 4, MaxScore: 5, ScoreA: 3, ScoreB: 4,
		ScoreC: &resolved, ResolvedScore: &resolved, ArbitrationTriggered: true, ResolutionSource: "arbiter",
		ReferenceReviewers: 2, ReferenceAdjudicated: true, PrimaryACostMicros: 10, PrimaryBCostMicros: 11, ArbiterCostMicros: 20,
	}
	stored, err := store.AddPanelObservation(context.Background(), "tenant-1", run.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	*input.ScoreC = 0
	items, err := store.ListPanelObservations(context.Background(), "tenant-1", run.ID)
	if err != nil || len(items) != 1 || items[0].ID == "" || items[0].RunID != run.ID || *items[0].ScoreC != 4 {
		t.Fatalf("unexpected persisted panel observation: %#v err=%v", items, err)
	}
	if _, err = store.AddPanelObservation(context.Background(), "tenant-1", run.ID, stored); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected duplicate response key rejection, got %v", err)
	}
	if _, _, err = NewService(store).CompletePanel(context.Background(), "tenant-1", run.ID); err != nil {
		t.Fatal(err)
	}
	input.ResponseKey = "response-2"
	if _, err = store.AddPanelObservation(context.Background(), "tenant-1", run.ID, input); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expected completed run to reject new panel observations, got %v", err)
	}
}

func TestPanelMetricsRejectNonAdjudicatedReference(t *testing.T) {
	_, err := CalculatePanelMetrics([]PanelObservation{{ResponseKey: "r1", ReferenceKind: ReferenceGold, MaxScore: 5, ScoreA: 1, ScoreB: 1, ResolutionSource: "human", HumanEscalated: true}})
	if err != ErrInvalidInput {
		t.Fatalf("expected adjudicated human reference enforcement, got %v", err)
	}
}

func TestPanelMetricsArbiterRescueUsesTheSameDisputedSubset(t *testing.T) {
	correct := 4.0
	items := []PanelObservation{{
		ResponseKey: "r1", Subject: "physics", Archetype: "calculation",
		ReferenceKind: ReferenceHumanAdjudicated, ReferenceScore: 4, MaxScore: 5,
		ScoreA: 4, ScoreB: 2, ScoreC: &correct, ResolvedScore: &correct,
		ArbitrationTriggered: true, ResolutionSource: "arbiter", ReferenceReviewers: 2, ReferenceAdjudicated: true,
	}}
	metrics, err := CalculatePanelMetrics(items)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.ArbitrationRescueRate != 1 || metrics.ArbitratedPrimaryA.SampleCount != 1 ||
		metrics.ArbitratedPrimaryB.SampleCount != 1 || metrics.Arbiter.SampleCount != 1 {
		t.Fatalf("arbiter was not compared on the disputed subset: %#v", metrics)
	}
	slices, err := CalculatePanelMetricsBySlice(items)
	if err != nil || len(slices) != 1 || slices[0].Subject != "physics" || slices[0].Archetype != "calculation" || slices[0].Metrics.SampleCount != 1 {
		t.Fatalf("panel slice metrics were not deterministic: %#v err=%v", slices, err)
	}
}

func TestPanelEvaluationMigrationIsImmutableAndDeidentified(t *testing.T) {
	raw, err := os.ReadFile("../../migrations/000157_subjective_panel_evaluation.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, required := range []string{"CREATE TABLE grading_panel_evaluation_observation", "reference_reviewer_count >= 2", "reference_adjudicated", "reject_grading_panel_evaluation_mutation", "ENABLE ROW LEVEL SECURITY"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("panel evaluation migration missing %q", required)
		}
	}
	for _, forbidden := range []string{"answer_text", "student_id", "reviewer_id", "image_url"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("panel evaluation persistence leaks %q", forbidden)
		}
	}
}

package modelcalibration

import (
	"context"
	"fmt"
	"math"
	"testing"
)

// .9训练时属于第9箱；预测时应沿用同一半开区间，而不是被第8箱的闭合上边界先命中。
func TestAdversarialConformalBoundaryMapsToOtherTrainingBin(t *testing.T) {
	items := []CalibrationEvidence{}
	for i := 0; i < 100; i++ {
		items = append(items, CalibrationEvidence{RawConfidence: .89, Correct: true}, CalibrationEvidence{RawConfidence: .9, Correct: false, SevereError: true})
	}
	artifact := buildArtifact(MethodConformal, items)
	if len(artifact.Bins) != 2 {
		t.Fatal(artifact.Bins)
	}
	got := mapConfidence(artifact.Bins, .9)
	next := mapConfidence(artifact.Bins, .900001)
	own := artifact.Bins[1].CalibratedConfidence
	if own != 0 || got != own || next != own {
		t.Fatalf("bins=%#v exact=%v next=%v", artifact.Bins, got, next)
	}
	if math.Abs(mapConfidence(artifact.Bins, math.Nextafter(.9, 0))-artifact.Bins[0].CalibratedConfidence) > epsilon {
		t.Fatal("value just below boundary changed bins")
	}
	t.Logf("100/100 raw=.9 observations wrong; their trained bin confidence=%v; prediction at exact .9=%v; prediction at .900001=%v", own, got, next)
}

type boundaryEvaluationReader struct{ items []EvaluationObservation }

func (boundaryEvaluationReader) GetRun(context.Context, string, string) (EvaluationRun, error) {
	return EvaluationRun{ID: "eval-1", ModelReference: "model-v1", PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", Status: "completed"}, nil
}
func (r boundaryEvaluationReader) ListObservations(context.Context, string, string) ([]EvaluationObservation, error) {
	return r.items, nil
}

func TestAdversarialConformalPublicCandidateBoundary(t *testing.T) {
	ctx := context.Background()
	reader := boundaryEvaluationReader{}
	for i := 0; i < 100; i++ {
		for _, good := range []bool{true, false} {
			score := 0.0
			prefix := "bad"
			if good {
				score = 1
				prefix = "good"
			}
			reader.items = append(reader.items, EvaluationObservation{ResponseKey: fmt.Sprintf("%s-%03d", prefix, i), Subject: "mathematics", Archetype: "short_constructed", OCRQuality: "high", ScoreBand: "full", ReferenceScore: 1, ModelScore: score, MaxScore: 1})
		}
	}
	service := NewService(NewMemoryStore(), reader)
	axis := Axis{ModelReference: "model-v1", PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", Subject: "mathematics", Archetype: "short_constructed", SliceKey: "all"}
	calibration, err := service.Create(ctx, "tenant-1", "actor-1", CreateInput{Key: "boundary-calibration", EvaluationRunID: "eval-1", Axis: axis, Method: MethodConformal})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reader.items {
		raw := .9
		if item.ModelScore == 1 {
			raw = .89
		}
		if _, err := service.AddEvidence(ctx, "tenant-1", calibration.ID, AddEvidenceInput{ResponseKey: item.ResponseKey, RawConfidence: raw}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Complete(ctx, "tenant-1", calibration.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, "tenant-1", calibration.ID, "approver-1"); err != nil {
		t.Fatal(err)
	}
	candidate, err := service.RecordCandidate(ctx, "tenant-1", RecordCandidateInput{CandidateKey: "new-exact-boundary", Axis: axis, RawConfidence: .9})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.CalibratedConfidence == nil || *candidate.CalibratedConfidence != 0 || candidate.AbstainReason != "" {
		t.Fatalf("candidate=%#v", candidate)
	}
	t.Logf("public Service candidate raw=.9, calibrated=%v, abstain=%q, source=.9 observations all wrong", *candidate.CalibratedConfidence, candidate.AbstainReason)
}

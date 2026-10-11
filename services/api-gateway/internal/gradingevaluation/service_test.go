package gradingevaluation

import (
	"context"
	"math"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCompleteComputesEvidenceBackedSlicesAndDifficulty(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	service.now = func() time.Time { return time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	run, err := service.CreateRun(ctx, "tenant-a", "manager-a", CreateRunInput{
		Key: "math-shadow-1", DisplayName: "数学影子评测", ModelReference: "qwen-vision-2026-08",
		PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", DatasetReference: "gold-math-202608", DatasetSHA256: testHash,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	inputs := []AddObservationInput{
		{ResponseKey: "response-1", ResponseFingerprint: testHash, ReferenceKind: ReferenceGold, Subject: "math", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "medium", ReferenceScore: 0, ModelScore: 0, MaxScore: 4, PageMatchCorrect: boolPointer(true), CropIoU: floatPointer(.96), TranscriptionCER: floatPointer(0), FormulaExact: boolPointer(true), RubricAgreement: floatPointer(1)},
		{ResponseKey: "response-2", ResponseFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReferenceKind: ReferenceHumanAdjudicated, Subject: "math", Archetype: "short_constructed", OCRQuality: "low", AnswerLength: "long", RubricComplexity: "high", ReferenceScore: 2, ModelScore: 0, MaxScore: 4, PageMatchCorrect: boolPointer(true), CropIoU: floatPointer(.84), TranscriptionCER: floatPointer(.4), FormulaExact: boolPointer(false), RubricAgreement: floatPointer(.5), ErrorSource: ErrorHandwritingOCR, NeedsHumanReview: true, ReferenceReviewers: 2, ReferenceAdjudicated: true},
		{ResponseKey: "response-3", ResponseFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ReferenceKind: ReferenceGold, Subject: "math", Archetype: "short_constructed", OCRQuality: "medium", AnswerLength: "medium", RubricComplexity: "medium", ReferenceScore: 4, ModelScore: 4, MaxScore: 4, PageMatchCorrect: boolPointer(false), CropIoU: floatPointer(.75), TranscriptionCER: floatPointer(.1), FormulaExact: boolPointer(true), RubricAgreement: floatPointer(1), ErrorSource: ErrorPageMatching, NeedsHumanReview: true},
	}
	for _, input := range inputs {
		if _, err = service.AddObservation(ctx, "tenant-a", run.ID, input); err != nil {
			t.Fatalf("add observation: %v", err)
		}
	}
	completed, err := service.Complete(ctx, "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != RunCompleted || completed.ObservationCount != 3 {
		t.Fatalf("unexpected completed run: %+v", completed)
	}
	if _, err = service.AddObservation(ctx, "tenant-a", run.ID, inputs[0]); err != ErrStateConflict {
		t.Fatalf("expected closed run to reject observations, got %v", err)
	}
	assertCompletedPartialScoreSlice(t, ctx, service, run.ID)
	difficulty, err := service.ListResponseDifficulty(ctx, "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("list difficulty: %v", err)
	}
	if len(difficulty) != 3 || difficulty[0].ResponseKey != "response-2" || difficulty[0].DifficultyBand != "high" || !difficulty[0].SevereError {
		t.Fatalf("unexpected difficulty result: %+v", difficulty)
	}
	quality, err := service.QualitySummary(ctx, "tenant-a", run.ID)
	if err != nil {
		t.Fatalf("quality summary: %v", err)
	}
	if quality.SampleCount != 3 || quality.PageMatchAccuracy.Rate == nil || *quality.PageMatchAccuracy.Rate != .666667 ||
		quality.MeanTranscriptionCER.Mean == nil || *quality.MeanTranscriptionCER.Mean != .166667 ||
		quality.RiskyErrorRoutingRecall != 1 || len(quality.ErrorAttribution) != 2 {
		t.Fatalf("unexpected pipeline quality summary: %+v", quality)
	}
}

func boolPointer(value bool) *bool        { return &value }
func floatPointer(value float64) *float64 { return &value }

func TestHumanAdjudicatedReferenceRequiresTwoReviewers(t *testing.T) {
	service := NewService(NewMemoryStore())
	run, err := service.CreateRun(context.Background(), "tenant-a", "manager-a", CreateRunInput{Key: "double-review", DisplayName: "double review", ModelReference: "model", PromptVersion: "p1", RubricVersion: "r1", DatasetReference: "set-1", DatasetSHA256: testHash})
	if err != nil {
		t.Fatal(err)
	}
	input := AddObservationInput{ResponseKey: "response-1", ResponseFingerprint: testHash, ReferenceKind: ReferenceHumanAdjudicated, Subject: "math", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "low", ReferenceScore: 1, ModelScore: 1, MaxScore: 2, ReferenceReviewers: 1}
	if _, err = service.AddObservation(context.Background(), "tenant-a", run.ID, input); err != ErrInvalidInput {
		t.Fatalf("single-review human reference must be rejected, got %v", err)
	}
	input.ReferenceReviewers, input.ReferenceAdjudicated = 2, true
	if _, err = service.AddObservation(context.Background(), "tenant-a", run.ID, input); err != nil {
		t.Fatalf("adjudicated double-review reference should pass: %v", err)
	}
}

func TestConservativeQWKRejectsMixedScalesAndUsesAlignedIntegerScale(t *testing.T) {
	items := []Observation{
		{ReferenceScore: 0, ModelScore: 0, MaxScore: 4}, {ReferenceScore: 2, ModelScore: 2, MaxScore: 4}, {ReferenceScore: 4, ModelScore: 3, MaxScore: 4},
	}
	qwk, ok, reason := conservativeQWK(items)
	if !ok || reason != "" || qwk < .80 || qwk > 1 {
		t.Fatalf("unexpected valid QWK %v %v %q", qwk, ok, reason)
	}
	items[2].MaxScore = 5
	if _, ok, reason = conservativeQWK(items); ok || reason != "requires_single_integer_score_scale" {
		t.Fatalf("mixed scale must be unavailable: %v %q", ok, reason)
	}
	metrics := calculateMetrics(items)
	if metrics.QWKAvailable || metrics.QWK != nil || math.IsNaN(metrics.MAE) {
		t.Fatalf("metrics must not manufacture QWK: %+v", metrics)
	}
}

func TestRunCannotCompleteWithoutActualAlignedObservation(t *testing.T) {
	service := NewService(NewMemoryStore())
	run, err := service.CreateRun(context.Background(), "tenant-a", "manager-a", CreateRunInput{
		Key: "empty-run", DisplayName: "空评测", ModelReference: "local", PromptVersion: "p1", RubricVersion: "r1", DatasetReference: "set-1", DatasetSHA256: testHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Complete(context.Background(), "tenant-a", run.ID); err != ErrInvalidInput {
		t.Fatalf("expected no-observation error, got %v", err)
	}
}

func TestAdmissionEvidenceUsesOnlyTheExactSubjectArchetypeIntersection(t *testing.T) {
	service := NewService(NewMemoryStore())
	ctx := context.Background()
	run, err := service.CreateRun(ctx, "tenant-a", "manager-a", CreateRunInput{
		Key: "admission-math", DisplayName: "admission", ModelReference: "model-v1", PromptVersion: "prompt-v1", RubricVersion: "rubric-v1", DatasetReference: "set-1", DatasetSHA256: testHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []AddObservationInput{
		{ResponseKey: "math-1", ResponseFingerprint: testHash, ReferenceKind: ReferenceGold, Subject: "math", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "low", ReferenceScore: 4, ModelScore: 4, MaxScore: 4},
		{ResponseKey: "english-1", ResponseFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReferenceKind: ReferenceGold, Subject: "english", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "low", ReferenceScore: 4, ModelScore: 0, MaxScore: 4},
	} {
		if _, err = service.AddObservation(ctx, "tenant-a", run.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.Complete(ctx, "tenant-a", run.ID); err != nil {
		t.Fatal(err)
	}
	evidence, err := service.AdmissionEvidenceFor(ctx, "tenant-a", "model-v1", "prompt-v1", "rubric-v1", assessment.SubjectMathematics, "short_constructed")
	if err != nil || !evidence.Approved || evidence.SampleCount != 1 || evidence.SevereErrorRate != 0 || evidence.EvaluationRef != run.ID {
		t.Fatalf("unexpected admission evidence: %#v err=%v", evidence, err)
	}
	missing, err := service.AdmissionEvidenceFor(ctx, "tenant-a", "model-v1", "prompt-v1", "rubric-v1", assessment.SubjectMathematics, "extended_response")
	if err != nil || missing.Approved || missing.SampleCount != 0 {
		t.Fatalf("unmatched axis must fail closed: %#v err=%v", missing, err)
	}
}

// 单样本切片可以报告错误率，但不能声称具备 QWK；保留对切片维度总数的要求。
func assertCompletedPartialScoreSlice(t *testing.T, ctx context.Context, service *Service, runID string) {
	t.Helper()
	slices, err := service.ListSliceMetrics(ctx, "tenant-a", runID)
	if err != nil {
		t.Fatalf("list slices: %v", err)
	}
	if len(slices) != 16 {
		t.Fatalf("slice count=%d, want 16 across the seven required dimensions", len(slices))
	}
	var partial *SliceMetric
	for index := range slices {
		if slices[index].Dimension == SliceScoreBand && slices[index].Value == "partial" {
			partial = &slices[index]
		}
	}
	if partial == nil {
		t.Fatal("missing partial-score slice")
	}
	if partial.Metrics.SampleCount != 1 || partial.Metrics.FalseZeroRate != 1 || partial.Metrics.SevereErrorRate != 1 {
		t.Fatalf("incorrect partial metrics: %+v", partial.Metrics)
	}
	if partial.Metrics.QWKAvailable {
		t.Fatal("single observation must not claim QWK")
	}
}

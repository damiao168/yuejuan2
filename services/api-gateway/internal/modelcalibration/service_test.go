package modelcalibration

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestCalibrationConsumesCompletedAlignedEvaluationAndGatesCandidates(t *testing.T) {
	ctx := context.Background()
	evaluation, run := completedCalibrationEvaluation(t, ctx)

	service := NewService(NewMemoryStore(), NewEvaluationReader(evaluation))
	service.now = func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) }
	axis := Axis{ModelReference: "qwen-vl-7b", PromptVersion: "prompt-v4", RubricVersion: "rubric-v3", Subject: "mathematics", Archetype: "short_constructed", SliceKey: "all"}
	calibration, err := service.Create(ctx, "tenant-a", "manager-a", CreateInput{Key: "math-short-v1", EvaluationRunID: run.ID, Axis: axis, Method: MethodIsotonic})
	if err != nil {
		t.Fatalf("create calibration: %v", err)
	}
	completeAndApproveCalibration(t, ctx, service, calibration, axis)
	target := 0.0
	high, err := service.RecordCandidate(ctx, "tenant-a", RecordCandidateInput{CandidateKey: "candidate-high", Axis: axis, RawConfidence: .90, TargetRisk: &target})
	if err != nil || high.CalibratedConfidence == nil || high.CalibrationID != calibration.ID || high.AbstainReason != "" {
		t.Fatalf("expected accepted confidence candidate, got %+v, %v", high, err)
	}
	low, err := service.RecordCandidate(ctx, "tenant-a", RecordCandidateInput{CandidateKey: "candidate-low", Axis: axis, RawConfidence: .20, TargetRisk: &target})
	if err != nil || low.CalibratedConfidence == nil || low.AbstainReason != "calibrated_confidence_below_target_risk_threshold" {
		t.Fatalf("expected abstained candidate, got %+v, %v", low, err)
	}
	if _, err = service.RecordCandidate(ctx, "tenant-a", RecordCandidateInput{CandidateKey: "candidate-low", Axis: axis, RawConfidence: .99, TargetRisk: &target}); err != nil {
		t.Fatalf("idempotent candidate retry: %v", err)
	}
	if _, err = evaluation.Invalidate(ctx, "tenant-a", run.ID, "source withdrawn"); err != nil {
		t.Fatalf("invalidate source evaluation: %v", err)
	}
	if unavailable, err := service.Approved(ctx, "tenant-a", axis); err != nil || unavailable.Available {
		t.Fatalf("invalidated source must fail closed: %+v, %v", unavailable, err)
	}
	stale, err := service.RecordCandidate(ctx, "tenant-a", RecordCandidateInput{CandidateKey: "candidate-after-invalid", Axis: axis, RawConfidence: .90, TargetRisk: &target})
	if err != nil || stale.AbstainReason != "approved_calibration_source_invalid" || stale.CalibratedConfidence != nil {
		t.Fatalf("invalidated source must abstain candidate: %+v, %v", stale, err)
	}
}

func TestCalibrationRejectsDraftOrMismatchedEvaluationEvidence(t *testing.T) {
	ctx := context.Background()
	evaluation := gradingevaluation.NewService(gradingevaluation.NewMemoryStore())
	run, err := evaluation.CreateRun(ctx, "tenant-a", "manager-a", gradingevaluation.CreateRunInput{
		Key: "draft-eval", DisplayName: "draft", ModelReference: "qwen-vl-7b", PromptVersion: "prompt-v4", RubricVersion: "rubric-v3", DatasetReference: "gold-v1", DatasetSHA256: testHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(NewMemoryStore(), NewEvaluationReader(evaluation))
	axis := Axis{ModelReference: "qwen-vl-7b", PromptVersion: "prompt-v4", RubricVersion: "rubric-v3", Subject: "mathematics", Archetype: "short_constructed", SliceKey: "all"}
	if _, err = service.Create(ctx, "tenant-a", "manager-a", CreateInput{Key: "draft-rejected", EvaluationRunID: run.ID, Axis: axis, Method: MethodAuto}); err != ErrEvaluationRequired {
		t.Fatalf("draft evaluation must be rejected, got %v", err)
	}
}

// 校准来源必须先包含逐条对齐观测，再完成评测；不以标记状态代替来源数据。
func completedCalibrationEvaluation(t *testing.T, ctx context.Context) (*gradingevaluation.Service, gradingevaluation.Run) {
	t.Helper()
	evaluation := gradingevaluation.NewService(gradingevaluation.NewMemoryStore())
	run, err := evaluation.CreateRun(ctx, "tenant-a", "manager-a", gradingevaluation.CreateRunInput{
		Key: "math-eval-v1", DisplayName: "math evidence", ModelReference: "qwen-vl-7b", PromptVersion: "prompt-v4", RubricVersion: "rubric-v3", DatasetReference: "gold-v1", DatasetSHA256: testHash,
	})
	if err != nil {
		t.Fatalf("create evaluation: %v", err)
	}
	observations := []gradingevaluation.AddObservationInput{
		{ResponseKey: "response-a", ResponseFingerprint: testHash, ReferenceKind: gradingevaluation.ReferenceGold, Subject: "mathematics", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "medium", ReferenceScore: 4, ModelScore: 4, MaxScore: 4},
		{ResponseKey: "response-b", ResponseFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", ReferenceKind: gradingevaluation.ReferenceGold, Subject: "mathematics", Archetype: "short_constructed", OCRQuality: "high", AnswerLength: "short", RubricComplexity: "medium", ReferenceScore: 2, ModelScore: 0, MaxScore: 4},
		{ResponseKey: "response-c", ResponseFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ReferenceKind: gradingevaluation.ReferenceGold, Subject: "mathematics", Archetype: "short_constructed", OCRQuality: "medium", AnswerLength: "medium", RubricComplexity: "medium", ReferenceScore: 3, ModelScore: 3, MaxScore: 4},
	}
	for _, input := range observations {
		if _, err = evaluation.AddObservation(ctx, "tenant-a", run.ID, input); err != nil {
			t.Fatalf("add evaluation observation: %v", err)
		}
	}
	if _, err = evaluation.Complete(ctx, "tenant-a", run.ID); err != nil {
		t.Fatalf("complete evaluation: %v", err)
	}

	return evaluation, run
}

// 未审核产物不应进入准入证据；本步骤按完成、拒绝未审批证据、审批、读取证据的原顺序执行。
func completeAndApproveCalibration(t *testing.T, ctx context.Context, service *Service, calibration Calibration, axis Axis) {
	t.Helper()
	var err error
	for key, confidence := range map[string]float64{"response-a": .90, "response-b": .20, "response-c": .70} {
		if _, err = service.AddEvidence(ctx, "tenant-a", calibration.ID, AddEvidenceInput{ResponseKey: key, RawConfidence: confidence}); err != nil {
			t.Fatalf("add calibration evidence %s: %v", key, err)
		}
	}
	completed, err := service.Complete(ctx, "tenant-a", calibration.ID)
	if err != nil {
		t.Fatalf("complete calibration: %v", err)
	}
	if completed.Status != StatusCompleted || completed.CalibrationN != 3 || completed.Artifact.Method != MethodIsotonic || len(completed.Artifact.RiskCoverageCurve) != 21 || completed.ArtifactURI == "" || completed.ArtifactSHA256 == "" {
		t.Fatalf("unexpected completed artifact: %+v", completed)
	}
	if evidence, err := service.Approved(ctx, "tenant-a", axis); err != nil || evidence.Available {
		t.Fatalf("unapproved artifact must not be eligible: %+v, %v", evidence, err)
	}
	approved, err := service.Approve(ctx, "tenant-a", calibration.ID, "reviewer-a")
	if err != nil || approved.Status != StatusApproved {
		t.Fatalf("approve: %+v, %v", approved, err)
	}
	evidence, err := service.Approved(ctx, "tenant-a", axis)
	if err != nil || !evidence.Available || evidence.CalibrationID != calibration.ID || evidence.CalibrationN != 3 {
		t.Fatalf("approved evidence: %+v, %v", evidence, err)
	}
}

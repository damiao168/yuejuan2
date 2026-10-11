package mathunderstanding

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestVerifiedActivationIsAtomicWithLeaseAndCorrectionRevision(t *testing.T) {
	for _, scenario := range []string{"invalid_lease", "corrected_during_verification", "new_crop", "retry_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			assertVerifiedActivationBinding(t, scenario)
		})
	}
}

func TestSymbolicUncertaintyAndContradictionForceHumanReview(t *testing.T) {
	for _, status := range []string{"uncertain", "contradicted"} {
		check := MathVerification{ID: "check-sympy", FormulaID: "formula-1", Kind: "constraint", Status: status, Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1"}
		contract, quality, err := applySymbolicVerifications(validInput(), []MathVerification{check}, 0)
		if err != nil || !contract.SolutionGraph.RequiresHumanReview || quality["critical_confidence"] != float64(0) {
			t.Fatalf("symbolic %s was silently promoted: contract=%#v quality=%#v err=%v", status, contract, quality, err)
		}
	}
}

func TestSymbolicTransitionsMustBindCanonicalDerivesEdges(t *testing.T) {
	contract := validInput()
	contract.Blocks = append(contract.Blocks, MathAnswerBlock{ID: "block-2", Kind: "formula", Status: "active", BoundingBox: BoundingBox{X: .1, Y: .5, Width: .5, Height: .2}, RecognitionEngine: "fixture", RecognitionVersion: "v1", RecognitionConfidence: .9, StructureConfidence: .9, SourceImageHash: contract.InputHash})
	second := contract.Formulas[0]
	second.ID, second.BlockID, second.BoundingBox = "formula-2", "block-2", contract.Blocks[1].BoundingBox
	contract.Formulas = append(contract.Formulas, second)
	contract.SolutionGraph.Steps = append(contract.SolutionGraph.Steps, SolutionStep{ID: "step-2", OrderHint: 2, BlockIDs: []string{"block-2"}, FormulaIDs: []string{"formula-2"}, Confidence: .9})
	contract.SolutionGraph.Edges = []SolutionEdge{{FromStepID: "step-1", ToStepID: "step-2", Kind: "derives"}}
	check := MathVerification{ID: "transition", StepID: "step-2", FormulaID: "formula-2", Kind: "equivalence", Status: "verified", Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1, Details: map[string]any{"from_step_id": "step-1", "from_formula_id": "formula-1"}}
	if _, _, err := applySymbolicVerifications(contract, []MathVerification{check}, 0); err != nil {
		t.Fatal(err)
	}
	contract.SolutionGraph.Edges[0].Kind = "next"
	if _, _, err := applySymbolicVerifications(contract, []MathVerification{check}, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("reading edge accepted as equivalence: %v", err)
	}
}

func TestSymbolicResultDoesNotEraseUncertainRecognitionSyntax(t *testing.T) {
	contract := validInput()
	contract.Verifications[0].Status, contract.Verifications[0].Confidence = "uncertain", 0
	check := MathVerification{ID: "solve", FormulaID: "formula-1", Kind: "constraint", Status: "verified", Domain: "real", Engine: "sympy", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1}
	derived, quality, err := applySymbolicVerifications(contract, []MathVerification{check}, 0)
	if err != nil || !derived.SolutionGraph.RequiresHumanReview || quality["critical_confidence"] != float64(0) {
		t.Fatalf("parser uncertainty silently erased: derived=%#v quality=%#v err=%v", derived, quality, err)
	}
}

func TestVerificationInputRetrievesExactCorrectionNotLatest(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	runtime := workerruntime.NewMemoryStore()
	h := NewHandler(artifacts, corrections, nil, nil, nil).WithRuntime(runtime)
	parent, _ := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	for _, text := range []string{"revision one", "revision two"} {
		corrected := cloneInput(parent.CreateArtifactInput)
		corrected.SolutionGraph.Steps[0].NormalizedText = text
		_, err := corrections.CreateCorrection(ctx, "tenant-a", parent.ID, "teacher", CreateCorrectionInput{ExpectedArtifactVersion: parent.Version, Operations: []CorrectionOperation{{Type: "move_step", TargetID: "step-1"}}, CorrectedContract: corrected})
		if err != nil {
			t.Fatal(err)
		}
	}
	task, _ := h.enqueueVerificationTask(ctx, "worker", parent, 1)
	_, contract, revision, err := h.verificationTaskContract(ctx, "tenant-a", task)
	if err != nil || revision != 1 || contract.SolutionGraph.Steps[0].NormalizedText != "revision one" {
		t.Fatalf("verification task drifted to latest correction: contract=%#v revision=%d err=%v", contract, revision, err)
	}
}

// 租约、修正版本或裁剪输入变化后再提交工作器结果，检查结果和两侧已存状态。
func assertVerifiedActivationBinding(t *testing.T, scenario string) {
	t.Helper()
	ctx := context.Background()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	runtime := workerruntime.NewMemoryStore()
	h := NewHandler(artifacts, corrections, nil, nil, nil).WithRuntime(runtime)
	parent, _ := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	task, _ := h.enqueueVerificationTask(ctx, "worker", parent, 0)
	claimed, _ := runtime.Claim(ctx, "tenant-a", workerruntime.ClaimInput{QueueName: "math-verification", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	lease := claimed[0].LeaseToken
	checks := []MathVerification{{ID: "solve-f1", FormulaID: "formula-1", Kind: "constraint", Status: "verified", ReasonCode: "solution_set_computed", Domain: "real", Engine: "sympy", EngineVersion: "1.14.0", RulesetVersion: "v1", Confidence: 1}}
	contract, quality, err := applySymbolicVerifications(parent.CreateArtifactInput, checks, 0)
	if err != nil {
		t.Fatal(err)
	}
	switch scenario {
	case "invalid_lease":
		lease = "invalid"
	case "corrected_during_verification":
		_, err = corrections.CreateCorrection(ctx, "tenant-a", parent.ID, "teacher", CreateCorrectionInput{ExpectedArtifactVersion: parent.Version, Operations: []CorrectionOperation{{Type: "move_step", TargetID: "step-1"}}, CorrectedContract: parent.CreateArtifactInput})
	case "new_crop":
		newCrop := validInput()
		newCrop.InputHash = "sha256:new-crop"
		_, err = artifacts.CreateArtifact(ctx, "tenant-a", newCrop)
	case "retry_conflict":
		_, _, err = h.completeVerifiedRuntime(ctx, verifiedRuntimeCompletion{
			Task: task, Parent: parent, CorrectionRevision: 0, Contract: contract,
			Quality: quality, LeaseToken: lease, DurationMS: 1,
		})
		contract.Verifications[1].ReasonCode = "different_result"
	}
	if err != nil {
		t.Fatal(err)
	}
	derived, superseded, err := h.completeVerifiedRuntime(ctx, verifiedRuntimeCompletion{
		Task: task, Parent: parent, CorrectionRevision: 0, Contract: contract,
		Quality: quality, LeaseToken: lease, DurationMS: 1,
	})
	current, _ := artifacts.GetLatestArtifact(ctx, "tenant-a", parent.AnswerSegmentID)
	storedTask, _ := runtime.Get(ctx, "tenant-a", task.ID)
	assertVerifiedActivationOutcome(t, scenario, verificationActivationOutcome{
		derived: derived, current: current, parentID: parent.ID, storedTask: storedTask, superseded: superseded, err: err, artifactCount: len(artifacts.artifacts),
	})
}

type verificationActivationOutcome struct {
	derived, current Artifact
	parentID         string
	storedTask       workerruntime.Task
	superseded       bool
	err              error
	artifactCount    int
}

func assertVerifiedActivationOutcome(t *testing.T, scenario string, outcome verificationActivationOutcome) {
	t.Helper()
	switch scenario {
	case "invalid_lease":
		if !errors.Is(outcome.err, workerruntime.ErrLeaseMismatch) || outcome.current.ID != outcome.parentID || outcome.storedTask.Status != workerruntime.StatusLeased || outcome.artifactCount != 1 {
			t.Fatalf("invalid lease activated evidence: derived=%#v current=%#v task=%#v err=%v", outcome.derived, outcome.current, outcome.storedTask, outcome.err)
		}
	case "corrected_during_verification", "new_crop":
		if outcome.err != nil || !outcome.superseded || outcome.current.Stage != "recognition" || outcome.storedTask.Status != workerruntime.StatusSucceeded {
			t.Fatalf("stale task was not safely superseded: current=%#v task=%#v superseded=%t err=%v", outcome.current, outcome.storedTask, outcome.superseded, outcome.err)
		}
	case "retry_conflict":
		if !errors.Is(outcome.err, workerruntime.ErrConflict) || outcome.current.Verifications[1].ReasonCode != "solution_set_computed" || outcome.artifactCount != 2 {
			t.Fatalf("conflicting retry replaced accepted evidence: current=%#v err=%v", outcome.current, outcome.err)
		}
	}
}

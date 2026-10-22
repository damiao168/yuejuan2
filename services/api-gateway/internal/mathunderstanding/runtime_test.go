package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestRuntimeCompletionPersistsBoundArtifactAndCompletesLease(t *testing.T) {
	input := runtimeSpatialInput(false)
	artifact := completeRuntimeArtifact(t, input)
	if len(artifact.Blocks) != 2 {
		t.Fatalf("runtime merged OCR blocks: %#v", artifact.Blocks)
	}
	if len(artifact.Relations) == 0 {
		t.Fatal("runtime did not derive spatial relations")
	}
	if artifact.SolutionGraph.BuilderVersion != runtimeSolutionBuilderVersion {
		t.Fatalf("builder version=%q", artifact.SolutionGraph.BuilderVersion)
	}
	if len(artifact.SolutionGraph.Steps) != 2 || artifact.SolutionGraph.Steps[0].ID == "step-1" {
		t.Fatalf("runtime retained placeholder graph: %#v", artifact.SolutionGraph)
	}
	if len(artifact.SolutionGraph.Edges) == 0 {
		t.Fatalf("runtime graph has no spatial edge: %#v", artifact.SolutionGraph)
	}
}

func TestRuntimeCompletionPreservesWorkerHumanReviewSignal(t *testing.T) {
	artifact := completeRuntimeArtifact(t, runtimeSpatialInput(true))
	if !artifact.SolutionGraph.RequiresHumanReview {
		t.Fatal("runtime discarded worker human-review signal")
	}
}

func TestRuntimeExpiredLeaseDoesNotPersistArtifactOrVerificationTask(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	task, err := runtime.CreateTask(ctx, "tenant-a", "worker-user", workerruntime.CreateTaskInput{
		TaskType: "evidence_verify", QueueName: "math-understanding", SourceType: "answer_segment", SourceID: "segment-1",
		PayloadSchemaVersion: "math-understanding-task-v1", IdempotencyKey: "math:segment-1:expired",
		Payload: map[string]any{"answer_segment_id": "segment-1", "exam_question_snapshot_id": "snapshot-1", "subject_code": "mathematics", "region_kind": "formula", "input_hash": "sha256:crop-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant-a", workerruntime.ClaimInput{QueueName: "math-understanding", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: items=%d err=%v", len(claimed), err)
	}
	runtime.ForceExpireLeaseForTest(task.ID)
	body, _ := json.Marshal(completeRuntimeRequest{LeaseToken: claimed[0].LeaseToken, DurationMS: 12, Artifact: runtimeFormulaInput()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-understanding/tasks/"+task.ID+"/complete", bytes.NewReader(body))
	req.SetPathValue("taskId", task.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "worker-user", TenantID: "tenant-a"}))
	rec := httptest.NewRecorder()
	NewHandler(artifacts, NewMemoryCorrectionStore(artifacts), NewMemoryPilotGateStore(), nil, nil).WithRuntime(runtime).CompleteRuntimeTask(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expired completion code=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := artifacts.GetLatestArtifact(ctx, "tenant-a", "segment-1"); err != ErrNotFound {
		t.Fatalf("expired result persisted artifact: err=%v", err)
	}
	if _, err := runtime.GetBySource(ctx, "tenant-a", "math_understanding_artifact", "math-artifact-1"); err != workerruntime.ErrNotFound {
		t.Fatalf("expired result persisted verification task: err=%v", err)
	}
	claimed, err = runtime.Claim(ctx, "tenant-a", workerruntime.ClaimInput{QueueName: "math-understanding", WorkerService: "ocr-worker", WorkerInstanceID: "worker-2", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("reclaim: items=%d err=%v", len(claimed), err)
	}
	currentInput := runtimeFormulaInput()
	currentInput.Formulas[0].RawLatex = "x=9"
	currentInput.Formulas[0].CanonicalLatex = "x=9"
	currentInput.Formulas[0].AST.Children[1].Value = "9"
	currentInput.Formulas[0].Candidates[0].Latex = "x=9"
	invoke := func(input CreateArtifactInput, lease string) *httptest.ResponseRecorder {
		t.Helper()
		body, _ := json.Marshal(completeRuntimeRequest{LeaseToken: lease, DurationMS: 12, Artifact: input})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-understanding/tasks/"+task.ID+"/complete", bytes.NewReader(body))
		req.SetPathValue("taskId", task.ID)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "worker-user", TenantID: "tenant-a"}))
		rec := httptest.NewRecorder()
		NewHandler(artifacts, NewMemoryCorrectionStore(artifacts), NewMemoryPilotGateStore(), nil, nil).WithRuntime(runtime).CompleteRuntimeTask(rec, req)
		return rec
	}
	for _, replay := range []bool{false, true} {
		if rec := invoke(currentInput, claimed[0].LeaseToken); rec.Code != http.StatusOK {
			t.Fatalf("valid completion replay=%t code=%d body=%s", replay, rec.Code, rec.Body.String())
		}
	}
	if rec := invoke(runtimeFormulaInput(), claimed[0].LeaseToken); rec.Code != http.StatusConflict {
		t.Fatalf("conflicting formula reused successful receipt: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := invoke(currentInput, "old-wrong-token"); rec.Code != http.StatusConflict {
		t.Fatalf("wrong lease replay code=%d", rec.Code)
	}
	current, err := artifacts.GetLatestArtifact(ctx, "tenant-a", "segment-1")
	if err != nil || current.Formulas[0].CanonicalLatex != "x=9" || current.Version != 1 || len(artifacts.artifacts) != 1 {
		t.Fatalf("reclaimed result did not replace rejected facts: %#v err=%v", current, err)
	}
}

func runtimeFormulaInput() CreateArtifactInput {
	input := validInput()
	// 识别回执由服务端重建步骤；旧 fixture 的 step-1 核验属于重建前图，不能附到新步骤。
	input.Verifications = []MathVerification{}
	input.RubricEvidence = []RubricEvidence{}
	return input
}

func TestPrepareRuntimeArtifactPreservesWorkerRelations(t *testing.T) {
	input := runtimeSpatialInput(false)
	input.Relations = []SpatialRelation{{
		ID: "worker-relation", FromID: "block-1", ToID: "block-2", Kind: "continues", Confidence: .9,
	}}
	prepared, err := prepareRuntimeArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Relations) != 1 || prepared.Relations[0].ID != "worker-relation" {
		t.Fatalf("runtime replaced worker relations: %#v", prepared.Relations)
	}
}

func TestVerificationRuntimeCreatesImmutableDerivedArtifact(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	runtime := workerruntime.NewMemoryStore()
	handler := NewHandler(artifacts, corrections, NewMemoryPilotGateStore(), nil, nil).WithRuntime(runtime)
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	task, err := handler.enqueueVerificationTask(ctx, "worker-user", base, 0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant-a", workerruntime.ClaimInput{
		QueueName: "math-verification", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim verification: items=%d err=%v", len(claimed), err)
	}

	inputRequest := httptest.NewRequest(http.MethodGet, "/api/v1/internal/math-verification/tasks/"+task.ID+"/input", nil)
	inputRequest.SetPathValue("taskId", task.ID)
	inputRequest = inputRequest.WithContext(auth.WithUser(inputRequest.Context(), auth.User{ID: "worker-user", TenantID: "tenant-a"}))
	inputResponse := httptest.NewRecorder()
	handler.GetVerificationRuntimeInput(inputResponse, inputRequest)
	if inputResponse.Code != http.StatusOK || !bytes.Contains(inputResponse.Body.Bytes(), []byte(`"correction_revision":0`)) {
		t.Fatalf("verification input code=%d body=%s", inputResponse.Code, inputResponse.Body.String())
	}

	check := MathVerification{
		ID: "sympy-solve-1", StepID: "step-1", FormulaID: "formula-1", Kind: "constraint",
		Status: "verified", ReasonCode: "solution_set_computed", Domain: "real",
		Engine: "sympy", EngineVersion: "1.14.0", RulesetVersion: "yuejuan-math-rules-v1", Confidence: 1,
	}
	body, _ := json.Marshal(completeVerificationRuntimeRequest{
		LeaseToken: claimed[0].LeaseToken, DurationMS: 18, ArtifactID: base.ID,
		ArtifactVersion: base.Version, CorrectionRevision: 0, Verifications: []MathVerification{check},
	})
	completeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-verification/tasks/"+task.ID+"/complete", bytes.NewReader(body))
	completeRequest.SetPathValue("taskId", task.ID)
	completeRequest = completeRequest.WithContext(auth.WithUser(completeRequest.Context(), auth.User{ID: "worker-user", TenantID: "tenant-a"}))
	completeResponse := httptest.NewRecorder()
	handler.CompleteVerificationRuntimeTask(completeResponse, completeRequest)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete verification code=%d body=%s", completeResponse.Code, completeResponse.Body.String())
	}
	derived, err := artifacts.GetLatestArtifact(ctx, "tenant-a", base.AnswerSegmentID)
	if err != nil || derived.Stage != "verified" || derived.ParentArtifactID != base.ID || len(derived.Verifications) != 2 {
		t.Fatalf("derived artifact=%#v err=%v", derived, err)
	}
	storedBase, _ := artifacts.GetArtifact(ctx, "tenant-a", base.ID)
	if storedBase.IsCurrent || storedBase.Stage != "recognition" || len(storedBase.Verifications) != 1 {
		t.Fatalf("recognition artifact was mutated: %#v", storedBase)
	}
	completed, err := runtime.Get(ctx, "tenant-a", task.ID)
	if err != nil || completed.Status != workerruntime.StatusSucceeded || completed.Result["artifact_id"] != derived.ID {
		t.Fatalf("verification runtime=%#v err=%v", completed, err)
	}
}

func runtimeSpatialInput(requiresHumanReview bool) CreateArtifactInput {
	input := validInput()
	input.InputHash = "hash-1"
	input.Blocks = []MathAnswerBlock{block("block-1", .1, .1), block("block-2", .1, .3)}
	input.Formulas = nil
	input.Relations = nil
	input.SolutionGraph = SolutionGraph{
		ID:                  "worker-placeholder",
		AnswerSegmentID:     input.AnswerSegmentID,
		BuilderVersion:      "math-runtime-v1",
		FormulaModelVersion: "text-only",
		OverallConfidence:   .9,
		RequiresHumanReview: requiresHumanReview,
		Steps:               []SolutionStep{{ID: "step-1", OrderHint: 1, BlockIDs: []string{"block-1", "block-2"}, Confidence: .9}},
	}
	input.Verifications = nil
	input.RubricEvidence = nil
	return input
}

func completeRuntimeArtifact(t *testing.T, input CreateArtifactInput) Artifact {
	t.Helper()
	ctx := context.Background()
	artifacts := NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	task, err := runtime.CreateTask(ctx, "tenant-a", "worker-user", workerruntime.CreateTaskInput{
		TaskType: "evidence_verify", QueueName: "math-understanding", SourceType: "answer_segment", SourceID: "segment-1",
		PayloadSchemaVersion: "math-understanding-task-v1", IdempotencyKey: "math:segment-1:hash-1",
		Payload: map[string]any{"answer_segment_id": "segment-1", "exam_question_snapshot_id": "snapshot-1", "subject_code": "mathematics", "region_kind": "formula", "input_hash": "hash-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant-a", workerruntime.ClaimInput{QueueName: "math-understanding", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: items=%d err=%v", len(claimed), err)
	}
	body, _ := json.Marshal(completeRuntimeRequest{LeaseToken: claimed[0].LeaseToken, DurationMS: 12, Artifact: input})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-understanding/tasks/"+task.ID+"/complete", bytes.NewReader(body))
	req.SetPathValue("taskId", task.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "worker-user", TenantID: "tenant-a"}))
	rec := httptest.NewRecorder()
	NewHandler(artifacts, NewMemoryCorrectionStore(artifacts), NewMemoryPilotGateStore(), nil, nil).WithRuntime(runtime).CompleteRuntimeTask(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete code=%d body=%s", rec.Code, rec.Body.String())
	}
	completed, err := runtime.Get(ctx, "tenant-a", task.ID)
	if err != nil || completed.Status != workerruntime.StatusSucceeded {
		t.Fatalf("runtime status=%q err=%v", completed.Status, err)
	}
	artifact, err := artifacts.GetLatestArtifact(ctx, "tenant-a", "segment-1")
	if err != nil || artifact.InputHash != "hash-1" {
		t.Fatalf("artifact=%#v err=%v", artifact, err)
	}
	return artifact
}

func TestArtifactCreationIsIdempotentForSameCropHash(t *testing.T) {
	store := NewMemoryStore()
	input := validInput()
	first, err := store.CreateArtifact(context.Background(), "tenant-a", input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateArtifact(context.Background(), "tenant-a", input)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("same crop must reuse artifact: first=%#v second=%#v", first, second)
	}
}

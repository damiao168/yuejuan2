package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type mathHandlerAdapter struct {
	calls       int
	sawMathCrop bool
	onGrade     func()
}

func (a *mathHandlerAdapter) Name() string { return "math-handler-test" }

func (a *mathHandlerAdapter) Grade(_ context.Context, input AdapterInput) (AdapterOutput, error) {
	a.calls++
	a.sawMathCrop = input.ActiveCrop != nil && input.MathEvidence != nil
	if a.onGrade != nil {
		a.onGrade()
	}
	return AdapterOutput{
		SchemaVersion: gradingAgentV2BuilderSchemaVersion, RequestID: input.RequestID,
		DeliveryMode: "teacher_suggestion", NeedsHumanReview: true,
		SuggestedScore: 999, ModelVersion: input.ModelPolicy.ModelVersion, PromptVersion: input.ModelPolicy.PromptVersion,
		RubricVersion:  input.Rubric.Version,
		MathCandidates: []MathCriterionCandidate{{RubricPointID: "p1", Status: "supported", EvidenceIDs: []string{"step-1"}, Confidence: .99, ReasonCode: "semantic_alignment"}},
	}, nil
}

func newMathHandlerFixture(t *testing.T, unresolved bool) (*Handler, *MemoryStore, *mathHandlerAdapter, Context) {
	t.Helper()
	value, source, _, _ := mathGradingFixture(t)
	value.AnswerText, value.AnswerVersion = "x=2", "answer-v1"
	if unresolved {
		value.Rubric.Points[0].EvidenceRequirements = []paper.EvidenceRequirement{{Type: "concept", Target: "factorization"}}
	}
	store := NewMemoryStore()
	store.AddContext("tenant-a", value.SegmentID, value)
	adapter := &mathHandlerAdapter{}
	crop := newActiveCropResolverFixture(t)
	crop.asset.TenantID = "tenant-a"
	crop.refresh()
	crop.evidenceStore.tenantID, crop.assetStore.tenantID = "tenant-a", "tenant-a"
	handler := NewHandler(store, adapter, nil).WithMathGradingV2(true, adapter, source, crop.resolver)
	return handler, store, adapter, value
}

func mathHandlerRequest(t *testing.T, body any, pathKey, pathValue string) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r.SetPathValue(pathKey, pathValue)
	return r.WithContext(auth.WithUser(r.Context(), auth.User{TenantID: "tenant-a", ID: "teacher-1"}))
}

func TestMathGradeDirectUsesServerScoreAndReplaysExactBinding(t *testing.T) {
	handler, store, adapter, _ := newMathHandlerFixture(t, false)
	input := GradeRequest{IdempotencyKey: "math-direct-1"}
	for i, status := range []int{http.StatusCreated, http.StatusOK} {
		w := httptest.NewRecorder()
		handler.Grade(w, mathHandlerRequest(t, input, "id", "segment-1"))
		var body struct{ Grade Grade }
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("direct call %d: %d %s", i, w.Code, w.Body.String())
		}
		grade := body.Grade
		if grade.SuggestedScore != 2 || !grade.NeedsHumanReview || grade.MissingPoints == nil || grade.MathArtifactID == "" || grade.MathArtifactVersion != 2 || grade.MathCorrectionRevision != 1 || grade.MathScoringVersion != MathScoringVersionV1 {
			t.Fatalf("model score or unbound suggestion persisted: %#v", grade)
		}
		run, err := store.GetRun(context.Background(), "tenant-a", grade.RunID)
		if err != nil || run.Status != RunSucceeded || run.GradeID != grade.ID || run.MathArtifactID != grade.MathArtifactID {
			t.Fatalf("run binding: %#v %v", run, err)
		}
	}
	if adapter.calls != 1 || !adapter.sawMathCrop || len(store.grades[key("tenant-a", "segment-1")]) != 1 {
		t.Fatal("exact evidence replay invoked inference or duplicated grade")
	}
}

func TestMathGradeDirectUnresolvedAndMissingCropNeverCreateGrade(t *testing.T) {
	for _, scenario := range []string{"unresolved", "missing_crop"} {
		t.Run(scenario, func(t *testing.T) {
			assertMathDirectReviewCannotCreateGrade(t, scenario)
		})
	}
}

func TestMathWorkerCompletionRescoresAndTerminatesUnsafeResults(t *testing.T) {
	for _, scenario := range []string{"forged_score", "unresolved", "version_drift", "invalid_candidate", "unsafe_delivery", "mock_signal", "alternative_risk", "crop_drift", "crop_unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			assertMathWorkerResultSettlement(t, scenario)
		})
	}
}

func TestMathCropDriftPreventsCachedReplayAndInferenceSettlement(t *testing.T) {
	for _, scenario := range []string{"before_replay", "during_inference"} {
		t.Run(scenario, func(t *testing.T) {
			assertMathCropDriftRejectsReplayAndSettlement(t, scenario)
		})
	}
}

func TestMathAdapterInputRejectsCropDifferentFromPreparedArtifact(t *testing.T) {
	handler, _, adapter, value := newMathHandlerFixture(t, false)
	if err := handler.prepareMathEvidence(context.Background(), "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	value.MathEvidence.cropInputHash = strings.Repeat("a", 64)
	_, _, used, err := handler.buildAdapterInput(context.Background(), "tenant-a", "request-1", value, NormalizePolicy(ModelPolicy{}), PromptGuard{}, aieligibility.OutputConstraint{})
	if !used || !isMathRevisionConflict(err) || adapter.calls != 0 {
		t.Fatalf("new crop was combined with old prepared artifact: used=%v err=%v", used, err)
	}
}

func TestMathV2SelectionKeepsLegacyRoutesWhenDisabledOrEvidenceMissing(t *testing.T) {
	handler, _, _, value := newMathHandlerFixture(t, false)
	if err := handler.prepareMathEvidence(context.Background(), "tenant-a", &value); err != nil || !handler.useMathV2(value) {
		t.Fatalf("ready math context was not selected: %v", err)
	}
	for _, scenario := range []string{"disabled", "non_math", "missing_evidence"} {
		t.Run(scenario, func(t *testing.T) {
			copyHandler, copyValue := *handler, value
			switch scenario {
			case "disabled":
				copyHandler.mathV2Enabled = false
			case "non_math":
				copyValue.Subject, copyValue.AssessmentSnapshot.SubjectCode = "physics", "physics"
			case "missing_evidence":
				copyValue.MathEvidence = nil
			}
			copyHandler.activeCrops = nil
			_, _, used, err := copyHandler.buildAdapterInput(context.Background(), "tenant-a", "request-1", copyValue, NormalizePolicy(ModelPolicy{}), PromptGuard{}, aieligibility.OutputConstraint{})
			if err != nil || used {
				t.Fatalf("legacy route reached v2 crop/inference boundary: used=%v err=%v", used, err)
			}
		})
	}
}

func assertMathDirectReviewCannotCreateGrade(t *testing.T, scenario string) {
	t.Helper()
	handler, store, adapter, _ := newMathHandlerFixture(t, scenario == "unresolved")
	if scenario == "missing_crop" {
		handler.activeCrops = nil
	}
	w := httptest.NewRecorder()
	handler.Grade(w, mathHandlerRequest(t, GradeRequest{}, "id", "segment-1"))
	var body map[string]json.RawMessage
	if w.Code != http.StatusUnprocessableEntity || json.Unmarshal(w.Body.Bytes(), &body) != nil || string(body["criterion_candidates"]) == "null" {
		t.Fatalf("review response: %d %s", w.Code, w.Body.String())
	}
	if len(store.grades[key("tenant-a", "segment-1")]) != 0 || len(store.runs) != 1 {
		t.Fatal("review path created a suggestion or lost the run")
	}
	for _, run := range store.runs {
		if run.Status != RunFailed || run.GradeID != "" {
			t.Fatalf("unresolved run: %#v", run)
		}
	}
	if scenario == "missing_crop" && adapter.calls != 0 {
		t.Fatal("missing crop reached inference")
	}
}

func assertMathWorkerResultSettlement(t *testing.T, scenario string) {
	t.Helper()
	fixture := prepareMathWorkerCompletionFixture(t, scenario)
	handler, store, adapter, value := fixture.handler, fixture.store, fixture.adapter, fixture.value
	runtime, run, task, claimed, output := fixture.runtime, fixture.run, fixture.task, fixture.claimed, fixture.output
	if scenario == "forged_score" {
		assertMathWorkerExecutionUsesServerScore(t, handler, adapter, task, claimed[0], run)
	}
	output.RawOutput = map[string]any{"injected_score": 999}
	status, runStatus, taskStatus := mutateMathWorkerResultScenario(scenario, handler, store, run, &output)
	w := httptest.NewRecorder()
	handler.CompleteWorker(w, mathHandlerRequest(t, WorkerResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultSchemaVersion: "math-grade-v2", Output: output}, "runId", run.ID))
	if w.Code != status {
		t.Fatalf("result: %d %s", w.Code, w.Body.String())
	}
	updated, err := store.GetRun(context.Background(), "tenant-a", run.ID)
	if err != nil || updated.Status != runStatus {
		t.Fatalf("run: %#v %v", updated, err)
	}
	updatedTask, err := runtime.Get(context.Background(), "tenant-a", task.ID)
	if err != nil || updatedTask.Status != taskStatus {
		t.Fatalf("task: %#v %v", updatedTask, err)
	}
	assertMathWorkerPersistedGradeBoundary(t, scenario, store, value, run, updated)
}

func assertMathCropDriftRejectsReplayAndSettlement(t *testing.T, scenario string) {
	t.Helper()
	handler, store, adapter, _ := newMathHandlerFixture(t, false)
	changeCrop := func() { changeMathHandlerActiveCropHash(handler) }
	input := GradeRequest{IdempotencyKey: "crop-drift-1"}
	if scenario == "before_replay" {
		w := httptest.NewRecorder()
		handler.Grade(w, mathHandlerRequest(t, input, "id", "segment-1"))
		if w.Code != http.StatusCreated {
			t.Fatalf("initial grade: %d %s", w.Code, w.Body.String())
		}
		changeCrop()
	} else {
		adapter.onGrade = changeCrop
	}
	w := httptest.NewRecorder()
	handler.Grade(w, mathHandlerRequest(t, input, "id", "segment-1"))
	if w.Code != http.StatusConflict || adapter.calls != 1 {
		t.Fatalf("stale crop was replayed/settled: %d %s calls=%d", w.Code, w.Body.String(), adapter.calls)
	}
	assertMathCropDriftStoredOutcome(t, scenario, store)
}

type mathWorkerCompletionFixture struct {
	handler *Handler
	store   *MemoryStore
	adapter *mathHandlerAdapter
	value   Context
	runtime *workerruntime.MemoryStore
	run     GradingRun
	task    workerruntime.Task
	claimed []workerruntime.Task
	output  AdapterOutput
}

// 先准备数学证据并领取任务，再取得待提交输出；主动执行场景仍在这之后调用执行入口。
func prepareMathWorkerCompletionFixture(t *testing.T, scenario string) mathWorkerCompletionFixture {
	t.Helper()
	handler, store, adapter, value := newMathHandlerFixture(t, scenario == "unresolved")
	runtime := workerruntime.NewMemoryStore()
	handler.WithWorkerRuntimeStore(runtime)
	if err := handler.prepareMathEvidence(context.Background(), "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	policy := NormalizePolicy(ModelPolicy{})
	run, err := store.GetOrCreateRun(context.Background(), "tenant-a", "teacher-1", runInputFor(value, "", policy, "math-worker-1"))
	if err != nil {
		t.Fatal(err)
	}
	task, err := runtime.CreateTask(context.Background(), "tenant-a", "teacher-1", workerruntime.CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID,
		PayloadSchemaVersion: "subjective-grade-v1", Payload: map[string]any{"run_id": run.ID}, IdempotencyKey: "math-worker-task-1", MaxAttempts: 2, RetryBackoffSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(context.Background(), "tenant-a", workerruntime.ClaimInput{QueueName: "subjective-grading", WorkerService: "subjective-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	output, err := adapter.Grade(context.Background(), AdapterInput{RequestID: run.RequestID, ModelPolicy: policy, Rubric: value.Rubric})
	if err != nil {
		t.Fatal(err)
	}
	return mathWorkerCompletionFixture{handler: handler, store: store, adapter: adapter, value: value, runtime: runtime, run: run, task: task, claimed: claimed, output: output}
}

func assertMathWorkerExecutionUsesServerScore(t *testing.T, handler *Handler, adapter *mathHandlerAdapter, task, claimed workerruntime.Task, run GradingRun) {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ExecuteWorker(w, mathHandlerRequest(t, map[string]string{"task_id": task.ID, "lease_token": claimed.LeaseToken}, "runId", run.ID))
	var body struct {
		ResultSchemaVersion string        `json:"result_schema_version"`
		Output              AdapterOutput `json:"output"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.ResultSchemaVersion != "math-grade-v2" || body.Output.SuggestedScore != 2 || !adapter.sawMathCrop {
		t.Fatalf("worker execution escaped math/crop/scoring boundary: %d %s", w.Code, w.Body.String())
	}
}

// 版本漂移在原互斥锁内修改运行；输出和裁剪故障注入均发生在完成请求之前。
func mutateMathWorkerResultScenario(scenario string, handler *Handler, store *MemoryStore, run GradingRun, output *AdapterOutput) (int, string, string) {
	status, runStatus, taskStatus := http.StatusOK, RunSucceeded, workerruntime.StatusSucceeded
	switch scenario {
	case "unresolved":
		status, runStatus, taskStatus = http.StatusUnprocessableEntity, RunFailed, workerruntime.StatusFailed
	case "version_drift":
		store.mu.Lock()
		changed := store.runs[key("tenant-a", run.RequestID)]
		changed.MathCorrectionRevision++
		store.runs[key("tenant-a", run.RequestID)] = changed
		store.mu.Unlock()
		status, runStatus, taskStatus = http.StatusConflict, RunConflict, workerruntime.StatusFailed
	case "invalid_candidate":
		output.MathCandidates[0].Confidence = 2
		status, runStatus, taskStatus = http.StatusBadRequest, RunFailed, workerruntime.StatusFailed
	case "unsafe_delivery":
		output.DeliveryMode = "auto_final"
		status, runStatus, taskStatus = http.StatusBadRequest, RunFailed, workerruntime.StatusFailed
	case "mock_signal":
		output.Mock = true
		status, runStatus, taskStatus = http.StatusBadRequest, RunFailed, workerruntime.StatusFailed
	case "alternative_risk":
		output.RiskFlags = []string{"alternative_solution_candidate"}
		status, runStatus, taskStatus = http.StatusUnprocessableEntity, RunFailed, workerruntime.StatusFailed
	case "crop_drift":
		crops := handler.mathEvidence.Crops.(*activeCropEvidenceFake)
		for i := range crops.values {
			crops.values[i].CropSHA256 = strings.Repeat("a", 64)
		}
		status, runStatus, taskStatus = http.StatusConflict, RunConflict, workerruntime.StatusFailed
	case "crop_unavailable":
		handler.mathEvidence.Crops.(*activeCropEvidenceFake).err = errors.New("crop no longer available")
		status, runStatus, taskStatus = http.StatusUnprocessableEntity, RunFailed, workerruntime.StatusFailed
	}
	return status, runStatus, taskStatus
}

func assertMathWorkerPersistedGradeBoundary(t *testing.T, scenario string, store *MemoryStore, value Context, run, updated GradingRun) {
	t.Helper()
	grades := store.grades[key("tenant-a", value.SegmentID)]
	if scenario == "forged_score" {
		if len(grades) != 1 || grades[0].SuggestedScore != 2 || !grades[0].NeedsHumanReview || grades[0].MathArtifactID != run.MathArtifactID {
			t.Fatalf("worker-owned score persisted: %#v", grades)
		}
		if _, exists := grades[0].RawOutput["injected_score"]; exists {
			t.Fatal("untrusted worker raw score was persisted")
		}
	} else if len(grades) != 0 || updated.GradeID != "" {
		t.Fatal("unsafe worker result created a grade")
	}
}

func changeMathHandlerActiveCropHash(handler *Handler) {
	crops := handler.mathEvidence.Crops.(*activeCropEvidenceFake)
	for i := range crops.values {
		crops.values[i].CropSHA256 = strings.Repeat("a", 64)
	}
}

func assertMathCropDriftStoredOutcome(t *testing.T, scenario string, store *MemoryStore) {
	t.Helper()
	grades := store.grades[key("tenant-a", "segment-1")]
	if scenario == "during_inference" && len(grades) != 0 || scenario == "before_replay" && len(grades) != 1 {
		t.Fatalf("stale crop generated a new grade: %#v", grades)
	}
	if scenario == "during_inference" {
		for _, run := range store.runs {
			if run.Status != RunConflict {
				t.Fatalf("crop drift did not conflict the run: %#v", run)
			}
		}
	}
}

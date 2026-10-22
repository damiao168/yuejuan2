package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func completionFixtureInput(fixture workerServiceFixture) WorkerResultInput {
	output := validServiceOutput()
	output.RequestID = fixture.run.RequestID
	output.ModelVersion, output.PromptVersion, output.RubricVersion = fixture.run.ModelVersion, fixture.run.PromptVersion, fixture.run.RubricVersion
	return WorkerResultInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken,
		ResultSchemaVersion: "subjective-grade-v1", DurationMS: 15, Output: output}
}

func TestWorkerPrepareLateFailureDoesNotOverwriteAcceptedRun(t *testing.T) {
	for _, stage := range []string{"math_evidence", "eligibility"} {
		t.Run(stage, func(t *testing.T) { assertWorkerPrepareLateFailure(t, stage) })
	}
}

func assertWorkerPrepareLateFailure(t *testing.T, stage string) {
	t.Helper()
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	input := completionFixtureInput(fixture)
	var accepted WorkerCompletionResult
	complete := func() {
		var err error
		accepted, err = NewWorkerCompletionService(fixture.store, fixture.runtime).Complete(context.Background(), workerServiceTenant, "teacher-1", preparedWorkerFixture(fixture), input, input.Output)
		if err != nil {
			t.Fatal(err)
		}
	}
	prepare := prepareMathEvidenceFunc(nil)
	eligibility := decideEligibilityFunc(allowWorkerEligibility)
	if stage == "math_evidence" {
		prepare = func(context.Context, string, *Context) error { complete(); return ErrActiveCropUnavailable }
	} else {
		eligibility = func(context.Context, string, string, Context, ModelPolicy) (aieligibility.Decision, bool, error) {
			complete()
			return aieligibility.Decision{ID: "late-denial"}, false, nil
		}
	}
	// 准备已经开始后完成回调先提交；迟到的准入错误必须保留该成功事实。
	if _, err := NewWorkerExecutionService(fixture.store, fixture.runtime, prepare, eligibility).Prepare(context.Background(), fixture.input); err == nil {
		t.Fatal("expected the original preparation error")
	}
	run, err := fixture.store.GetRun(context.Background(), workerServiceTenant, fixture.run.ID)
	if err != nil || run.Status != RunSucceeded || run.GradeID != accepted.Grade.ID || !run.UpdatedAt.Equal(accepted.Run.UpdatedAt) {
		t.Fatalf("late preparation failure changed the accepted run: %v", err)
	}
}

func TestGradeIdempotencyPreservesNilAndExplicitEmptyResults(t *testing.T) {
	store := NewMemoryStore()
	grade := Grade{AnswerSegmentID: "synthetic-segment", QuestionID: "synthetic-question", AdapterRequestID: "synthetic-request", RawOutput: map[string]any{}}
	first, err := store.CreateGrade(context.Background(), "synthetic-tenant", "synthetic-actor", grade)
	if err != nil {
		t.Fatal(err)
	}
	if first.MatchedPoints != nil || first.MissingPoints != nil || first.Evidence != nil || first.RiskFlags != nil {
		t.Fatal("store clone changed null result fields to empty arrays")
	}
	if replay, err := store.CreateGrade(context.Background(), "synthetic-tenant", "synthetic-actor", grade); err != nil || replay.ID != first.ID {
		t.Fatalf("the same full grade identity no longer replays: %v", err)
	}
	grade.Evidence = []grading.Evidence{}
	if _, err := store.CreateGrade(context.Background(), "synthetic-tenant", "synthetic-actor", grade); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("explicitly changed grade shape was silently accepted: %v", err)
	}
}

func TestWorkerRejectedLeaseNeverPersistsGrade(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	input := completionFixtureInput(fixture)
	input.LeaseToken = "old-worker-lease"
	service := NewWorkerCompletionService(fixture.store, fixture.runtime)
	_, err := service.Complete(context.Background(), workerServiceTenant, "teacher-1", preparedWorkerFixture(fixture), input, input.Output)
	if !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("expected stale lease rejection, got %v", err)
	}
	if _, err := fixture.store.GetGradeByAdapterRequestID(context.Background(), workerServiceTenant, fixture.run.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rejected worker persisted a grade: %v", err)
	}
	input.LeaseToken = fixture.task.LeaseToken
	input.Output.SuggestedScore = 4
	result, err := service.Complete(context.Background(), workerServiceTenant, "teacher-1", preparedWorkerFixture(fixture), input, input.Output)
	if err != nil || result.Grade.SuggestedScore != 4 {
		t.Fatalf("valid worker reused stale grade: %#v %v", result, err)
	}
}

func TestWorkerFailureCannotRewriteTerminalRunAfterTaskRetryStateChanges(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	service := NewWorkerCompletionService(fixture.store, fixture.runtime)
	input := WorkerFailureInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ErrorCode: "ai_output_incomplete", Retryable: false,
		ErrorDetail: executionFailureDetail(truncatedDiagnostics()), DurationMS: 12}
	first, err := service.Fail(context.Background(), workerServiceTenant, fixture.run.ID, input)
	if err != nil || first.Run.Status != RunFailed || first.Task.Status != workerruntime.StatusFailed {
		t.Fatalf("initial atomic failure did not close task and run: %#v %v", first, err)
	}
	if _, err := service.Fail(context.Background(), workerServiceTenant, fixture.run.ID, input); err == nil {
		t.Fatal("a second failure callback rewrote an already terminal run")
	}
	run, err := fixture.store.GetRun(context.Background(), workerServiceTenant, fixture.run.ID)
	if err != nil || run.Status != RunFailed || run.ErrorCode != "ai_output_incomplete" || run.ExecutionDiagnostics == nil {
		t.Fatalf("terminal run changed after duplicate failure: %#v %v", run, err)
	}
}

func TestWorkerCompletionReplayRequiresSameLeaseAndRawResult(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	input := completionFixtureInput(fixture)
	service := NewWorkerCompletionService(fixture.store, fixture.runtime)
	accepted, err := service.Complete(context.Background(), workerServiceTenant, "teacher-1", preparedWorkerFixture(fixture), input, input.Output)
	if err != nil {
		t.Fatal(err)
	}
	replayed, replay, err := service.Replay(context.Background(), workerServiceTenant, "teacher-1", fixture.run.ID, input)
	if err != nil || !replay || replayed.Grade.ID != accepted.Grade.ID || replayed.Run.Status != RunSucceeded {
		t.Fatalf("same result replay: %#v %v", replayed, err)
	}
	for _, scenario := range []string{"lease", "result", "schema", "worker_capability", "run"} {
		t.Run(scenario, func(t *testing.T) {
			changed := input
			ctx := context.Background()
			runID := fixture.run.ID
			switch scenario {
			case "lease":
				changed.LeaseToken = "forged"
			case "result":
				changed.Output.SuggestedScore--
			case "schema":
				changed.ResultSchemaVersion = "other-v1"
			case "worker_capability":
				other := accepted.Task
				other.ID = "another-task"
				ctx = workerruntime.WithTaskCapability(ctx, other)
			case "run":
				runID = "other-run"
			}
			if _, _, err := service.Replay(ctx, workerServiceTenant, "teacher-1", runID, changed); err == nil {
				t.Fatal("changed completion was accepted")
			}
		})
	}
	// 同一完成租约不能通过 Prepare 再获得模型执行权限，也不能被迟到的失败回调改写成功运行。
	if _, err := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, allowWorkerEligibility).Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerLeaseMismatch) {
		t.Fatalf("completed task was executable: %v", err)
	}
	service.Reject(context.Background(), workerServiceTenant, preparedWorkerFixture(fixture), input, RunFailed, "late", errors.New("late rejection"))
	run, _ := fixture.store.GetRun(context.Background(), workerServiceTenant, fixture.run.ID)
	if run.Status != RunSucceeded {
		t.Fatalf("late failure changed accepted run: %#v", run)
	}
}

func TestWorkerReplayCannotReviveRunAfterTerminalFailure(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	input := completionFixtureInput(fixture)
	service := NewWorkerCompletionService(fixture.store, fixture.runtime)
	accepted, err := service.Complete(context.Background(), workerServiceTenant, "teacher-1", preparedWorkerFixture(fixture), input, input.Output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.UpdateRun(context.Background(), workerServiceTenant, fixture.run.ID, UpdateRunInput{Status: RunFailed, ErrorCode: "synthetic_terminal_failure"}); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := service.Replay(context.Background(), workerServiceTenant, "teacher-1", fixture.run.ID, input); !replay || err == nil {
		t.Fatalf("replay revived a failed run: replay=%v err=%v accepted=%s", replay, err, accepted.Run.ID)
	}
}

func TestPanelMissingHumanHandoffRetriesWithoutCallingModels(t *testing.T) {
	for _, stage := range []string{"task", "association"} {
		t.Run(stage, func(t *testing.T) {
			a := &panelTestAdapter{err: errors.New("unavailable")}
			b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
			c := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .99}
			orchestrator := newTestPanelOrchestrator(t, a, b, c)
			store := &failingPanelHumanStore{PanelPersistence: orchestrator.store, failure: errors.New("handoff unavailable"), failTask: stage == "task"}
			if stage == "association" {
				store.failUpdate = 2
			}
			orchestrator.store = store
			if _, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{}); err == nil {
				t.Fatal("expected handoff failure")
			}
			store.failTask, store.failUpdate = false, 0
			result, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
			if err != nil || result.Panel.Status != PanelHumanReview || result.Panel.ReviewTaskID == "" || result.PrimaryB.ID == "" || len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 0 || store.taskCalls != 2 {
				t.Fatalf("handoff replay lost frozen results: %#v err=%v calls=%d", result, err, store.taskCalls)
			}
			again, err := orchestrator.Grade(context.Background(), "tenant-1", "actor-1", panelTestContext(), PanelDecisionConfig{})
			if err != nil || again.Panel.ReviewTaskID != result.Panel.ReviewTaskID || store.taskCalls != 2 {
				t.Fatalf("completed handoff was repeated: %#v %v", again, err)
			}
		})
	}
}

func TestEvidenceNormalizationKeepsMathMeaning(t *testing.T) {
	for _, pair := range [][2]string{{"v=3 m/s", "v=-3 m/s"}, {"0.5", "05"}, {"1/2", "12"}, {"x<3", "x>3"}, {"a-b", "ab"}} {
		if normalizeEvidenceText(pair[0]) == normalizeEvidenceText(pair[1]) {
			t.Fatalf("normalization erased math meaning: %q / %q", pair[0], pair[1])
		}
	}
	if normalizeEvidenceText("Ｖ＝３　Ｍ／Ｓ") != normalizeEvidenceText("v=3 m/s") {
		t.Fatal("NFKC, case and whitespace compatibility was lost")
	}
	for _, excerpt := range []string{"v=-3 m/s", "v=3 ms"} {
		_, err := outputEvidenceIndex([]grading.Evidence{{EvidenceID: "e1", RubricPointID: "p1", Location: "answer_text", Confidence: .9, AnswerText: excerpt}}, "v=3 m/s")
		if !errors.Is(err, ErrInvalidModelOutput) {
			t.Fatalf("fabricated excerpt accepted: %q %v", excerpt, err)
		}
	}
}

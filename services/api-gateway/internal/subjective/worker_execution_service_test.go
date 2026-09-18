package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const workerServiceTenant = "tenant-service-test"

type workerServiceFixture struct {
	store   *MemoryStore
	runtime *workerruntime.MemoryStore
	run     GradingRun
	task    workerruntime.Task
	input   WorkerPrepareInput
}

func newWorkerServiceFixture(t *testing.T, runInput CreateRunInput, sourceID string) workerServiceFixture {
	t.Helper()
	ctx := context.Background()
	store := NewMemoryStore()
	value := serviceTestContext()
	store.AddContext(workerServiceTenant, value.SegmentID, value)
	if runInput.AnswerSegmentID == "" {
		runInput = serviceTestRunInput(value)
	}
	run, err := store.GetOrCreateRun(ctx, workerServiceTenant, "teacher-1", runInput)
	if err != nil {
		t.Fatal(err)
	}
	if sourceID == "" {
		sourceID = run.ID
	}
	runtime := workerruntime.NewMemoryStore()
	_, err = runtime.CreateTask(ctx, workerServiceTenant, "teacher-1", workerruntime.CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: sourceID,
		Payload: map[string]any{"run_id": run.ID}, PayloadSchemaVersion: "subjective-grade-v1",
		IdempotencyKey: "task-" + run.RequestID, MaxAttempts: 2, RetryBackoffSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, workerServiceTenant, workerruntime.ClaimInput{
		QueueName: "subjective-grading", WorkerService: "subjective-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	task := claimed[0]
	return workerServiceFixture{store: store, runtime: runtime, run: run, task: task, input: WorkerPrepareInput{
		TenantID: workerServiceTenant, RunID: run.ID, TaskID: task.ID, LeaseToken: task.LeaseToken,
	}}
}

func TestWorkerExecutionServicePrepare(t *testing.T) {
	t.Run("correct task", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, allowWorkerEligibility)
		prepared, err := service.Prepare(context.Background(), fixture.input)
		if err != nil || prepared.Run.ID != fixture.run.ID || prepared.Task.ID != fixture.task.ID || prepared.Context.SegmentID != "segment-service-test" {
			t.Fatalf("prepared=%#v err=%v", prepared, err)
		}
	})

	t.Run("source mismatch", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "another-run")
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, allowWorkerEligibility)
		if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerTaskMismatch) {
			t.Fatalf("expected task mismatch, got %v", err)
		}
	})

	t.Run("invalid lease", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		fixture.input.LeaseToken = "wrong-token"
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, allowWorkerEligibility)
		if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerLeaseMismatch) {
			t.Fatalf("expected lease mismatch, got %v", err)
		}
	})

	t.Run("inactive status", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		_, err := fixture.runtime.Fail(context.Background(), workerServiceTenant, fixture.task.ID, workerruntime.FailInput{LeaseToken: fixture.task.LeaseToken, ErrorCode: "stopped"})
		if err != nil {
			t.Fatal(err)
		}
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, allowWorkerEligibility)
		if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerLeaseMismatch) {
			t.Fatalf("expected inactive lease mismatch, got %v", err)
		}
	})

	t.Run("math evidence unavailable", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		prepare := func(context.Context, string, *Context) error { return ErrActiveCropUnavailable }
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, prepare, allowWorkerEligibility)
		if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrActiveCropUnavailable) {
			t.Fatalf("expected evidence error, got %v", err)
		}
		assertWorkerAndRunFailure(t, fixture, "math_evidence_unavailable", RunFailed)
	})

	t.Run("math revision conflict", func(t *testing.T) {
		value := serviceTestContext()
		runInput := serviceTestRunInput(value)
		runInput.MathArtifactID, runInput.MathArtifactVersion = "artifact-1", 2
		runInput.MathCorrectionRevision, runInput.MathScoringVersion = 1, MathScoringVersionV1
		fixture := newWorkerServiceFixture(t, runInput, "")
		prepare := func(_ context.Context, _ string, value *Context) error {
			value.MathEvidence = &MathEvidenceContext{ArtifactID: "artifact-changed", ArtifactVersion: 2, CorrectionRevision: 1, ScoringVersion: MathScoringVersionV1}
			return nil
		}
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, prepare, allowWorkerEligibility)
		if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerMathBindingConflict) || !errors.Is(err, mathunderstanding.ErrRevisionConflict) {
			t.Fatalf("expected math binding conflict, got %v", err)
		}
		assertWorkerAndRunFailure(t, fixture, "math_evidence_version_conflict", RunConflict)
	})

	t.Run("eligibility abstain", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		deny := func(context.Context, string, string, Context, ModelPolicy) (aieligibility.Decision, bool, error) {
			return aieligibility.Decision{ID: "decision-denied"}, false, nil
		}
		service := NewWorkerExecutionService(fixture.store, fixture.runtime, nil, deny)
		_, err := service.Prepare(context.Background(), fixture.input)
		var abstained *EligibilityAbstainedError
		if !errors.As(err, &abstained) || abstained.Decision.ID != "decision-denied" {
			t.Fatalf("expected eligibility decision, got %v", err)
		}
		assertWorkerAndRunFailure(t, fixture, "ai_eligibility_abstained", RunFailed)
	})
}

func allowWorkerEligibility(context.Context, string, string, Context, ModelPolicy) (aieligibility.Decision, bool, error) {
	return aieligibility.Decision{ID: "decision-allowed", ExternalAIAllowed: true}, true, nil
}

func serviceTestContext() Context {
	return Context{
		SegmentID: "segment-service-test", AnswerVersion: "answer-v1", AnswerText: "synthetic answer",
		Question: paper.Question{ID: "question-1", QuestionNo: "Q1", QuestionType: "short_answer", Score: 5},
		Rubric: paper.Rubric{ID: "rubric-1", QuestionID: "question-1", Version: "rubric-v1", MaxScore: 5,
			Points: []paper.RubricPoint{{ID: "p1", Description: "point one", Score: 5, Required: true}}},
	}
}

func serviceTestRunInput(value Context) CreateRunInput {
	return CreateRunInput{AnswerSegmentID: value.SegmentID, AnswerVersion: value.AnswerVersion, QuestionID: value.Question.ID,
		RubricVersion: value.Rubric.Version, ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8, RequestID: "request-service-test"}
}

func assertWorkerAndRunFailure(t *testing.T, fixture workerServiceFixture, errorCode, runStatus string) {
	t.Helper()
	task, taskErr := fixture.runtime.Get(context.Background(), workerServiceTenant, fixture.task.ID)
	run, runErr := fixture.store.GetRun(context.Background(), workerServiceTenant, fixture.run.ID)
	if taskErr != nil || runErr != nil || task.Status != workerruntime.StatusFailed || task.ErrorCode != errorCode || run.Status != runStatus || run.ErrorCode != errorCode {
		t.Fatalf("task=%#v taskErr=%v run=%#v runErr=%v", task, taskErr, run, runErr)
	}
}

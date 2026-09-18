package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestWorkerCompletionServiceCompleteAndReuse(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "creates grade"
		if existing {
			name = "reuses existing grade"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
			prepared := preparedWorkerFixture(fixture)
			output := validServiceOutput()
			output.RequestID = fixture.run.RequestID
			var existingGrade Grade
			if existing {
				var err error
				existingGrade, err = fixture.store.CreateGrade(context.Background(), workerServiceTenant, "teacher-1", successfulGrade(prepared.Context, prepared.Policy, prepared.Run.ID, output))
				if err != nil {
					t.Fatal(err)
				}
			}
			input := WorkerResultInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1", DurationMS: 15}
			result, err := NewWorkerCompletionService(fixture.store, fixture.runtime).Complete(context.Background(), workerServiceTenant, "teacher-1", prepared, input, output)
			if err != nil || result.Run.Status != RunSucceeded || result.Task.Status != workerruntime.StatusSucceeded || result.Run.GradeID != result.Grade.ID {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if existing && result.Grade.ID != existingGrade.ID {
				t.Fatalf("existing grade was not reused: %s != %s", result.Grade.ID, existingGrade.ID)
			}
		})
	}
}

func TestWorkerCompletionServiceRejectsRuntimeConflict(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	prepared := preparedWorkerFixture(fixture)
	output := validServiceOutput()
	output.RequestID = fixture.run.RequestID
	input := WorkerResultInput{TaskID: fixture.task.ID, LeaseToken: "wrong-token", ResultSchemaVersion: "subjective-grade-v1"}
	_, err := NewWorkerCompletionService(fixture.store, fixture.runtime).Complete(context.Background(), workerServiceTenant, "teacher-1", prepared, input, output)
	if !errors.Is(err, ErrWorkerCompletionRejected) || !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("expected runtime completion conflict, got %v", err)
	}
}

func TestWorkerCompletionServiceRejectAndFailRunStates(t *testing.T) {
	t.Run("settlement rejection fails run", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		prepared := preparedWorkerFixture(fixture)
		input := WorkerResultInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, DurationMS: 7}
		NewWorkerCompletionService(fixture.store, fixture.runtime).Reject(context.Background(), workerServiceTenant, prepared, input, RunFailed, "invalid_model_output", ErrInvalidModelOutput)
		assertWorkerAndRunFailure(t, fixture, "invalid_model_output", RunFailed)
	})

	t.Run("worker failure updates run", func(t *testing.T) {
		fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
		input := WorkerFailureInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ErrorCode: "worker_failed", DurationMS: 9}
		result, err := NewWorkerCompletionService(fixture.store, fixture.runtime).Fail(context.Background(), workerServiceTenant, fixture.run.ID, input)
		if err != nil || result.Run.Status != RunFailed || result.Run.ErrorCode != "worker_failed" || result.Task.Status != workerruntime.StatusFailed {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}

func preparedWorkerFixture(fixture workerServiceFixture) WorkerExecutionContext {
	value, _ := fixture.store.LoadContext(context.Background(), workerServiceTenant, fixture.run.AnswerSegmentID)
	return WorkerExecutionContext{Run: fixture.run, Task: fixture.task, Context: value,
		Policy: ModelPolicy{ModelVersion: fixture.run.ModelVersion, PromptVersion: fixture.run.PromptVersion, MinConfidence: fixture.run.MinConfidence}}
}

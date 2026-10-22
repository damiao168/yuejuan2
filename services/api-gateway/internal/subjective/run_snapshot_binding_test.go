package subjective

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestRunSnapshotIdentityIsFrozenAcrossRequestReplay(t *testing.T) {
	value := serviceTestContext()
	value.AssessmentSnapshot.ID = "snapshot-one"
	policy := ModelPolicy{ModelVersion: "model", PromptVersion: "prompt", MinConfidence: .8}
	first, err := stableRequestID("tenant", value, policy, "same-key")
	if err != nil {
		t.Fatal(err)
	}
	input := runInputFor(value, "", policy, first)
	store := NewMemoryStore()
	run, err := store.GetOrCreateRun(context.Background(), "tenant", "actor", input)
	if err != nil || run.ExamQuestionSnapshotID != value.AssessmentSnapshot.ID {
		t.Fatalf("snapshot not frozen: %#v %v", run, err)
	}
	value.AssessmentSnapshot.ID = "snapshot-two"
	second, err := stableRequestID("tenant", value, policy, "same-key")
	if err != nil || second == first {
		t.Fatalf("new snapshot reused request identity: %s %v", second, err)
	}
	input.ExamQuestionSnapshotID = value.AssessmentSnapshot.ID
	if _, err := store.GetOrCreateRun(context.Background(), "tenant", "actor", input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("replay rebound run snapshot: %v", err)
	}
}

func TestWorkerPrepareRejectsChangedSnapshotBeforeAdmission(t *testing.T) {
	fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
	value := serviceTestContext()
	value.AssessmentSnapshot.ID = "new-snapshot"
	fixture.store.AddContext(workerServiceTenant, value.SegmentID, value)
	called := false
	service := NewWorkerExecutionService(fixture.store, fixture.runtime, func(context.Context, string, *Context) error { called = true; return nil }, allowWorkerEligibility)
	if _, err := service.Prepare(context.Background(), fixture.input); !errors.Is(err, ErrWorkerResultVersionConflict) || called {
		t.Fatalf("changed snapshot reached model preparation: called=%v err=%v", called, err)
	}
	assertWorkerAndRunFailure(t, fixture, "result_version_conflict", RunConflict)
}

func TestTerminalPanelRejectsChangedSnapshotWithoutNewModelCall(t *testing.T) {
	a := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	b := &panelTestAdapter{supported: map[string]bool{"p1": true}, confidence: .9}
	c := &panelTestAdapter{confidence: .99}
	o := newTestPanelOrchestrator(t, a, b, c)
	value := panelTestContext()
	value.AssessmentSnapshot.ID = "snapshot-old"
	if _, err := o.Grade(context.Background(), "tenant-1", "actor-1", value, PanelDecisionConfig{}); err != nil {
		t.Fatal(err)
	}
	value.AssessmentSnapshot.ID = "snapshot-new"
	if _, err := o.Grade(context.Background(), "tenant-1", "actor-1", value, PanelDecisionConfig{}); !errors.Is(err, ErrIdempotencyConflict) || len(a.inputs) != 1 || len(b.inputs) != 1 || len(c.inputs) != 0 {
		t.Fatalf("old panel reused after snapshot change: calls=%d/%d/%d err=%v", len(a.inputs), len(b.inputs), len(c.inputs), err)
	}
}

func TestWorkerCompletionRejectsSnapshotChangedAfterPrepare(t *testing.T) {
	value := serviceTestContext()
	value.AssessmentSnapshot.ID = "snapshot-one"
	inputRun := serviceTestRunInput(value)
	inputRun.ExamQuestionSnapshotID = value.AssessmentSnapshot.ID
	f := newWorkerServiceFixture(t, inputRun, "")
	f.store.AddContext(workerServiceTenant, value.SegmentID, value)
	prepared := preparedWorkerFixture(f)
	output := validServiceOutput()
	output.RequestID, output.ModelVersion, output.PromptVersion, output.RubricVersion = f.run.RequestID, f.run.ModelVersion, f.run.PromptVersion, f.run.RubricVersion
	input := WorkerResultInput{TaskID: f.task.ID, LeaseToken: f.task.LeaseToken, ResultSchemaVersion: "subjective-grade-v1", DurationMS: 15, Output: output}
	value.AssessmentSnapshot.ID = "snapshot-two"
	f.store.AddContext(workerServiceTenant, value.SegmentID, value)
	if _, err := NewWorkerCompletionService(f.store, f.runtime).Complete(context.Background(), workerServiceTenant, "teacher-1", prepared, input, output); !errors.Is(err, ErrWorkerCompletionRejected) || !errors.Is(err, ErrWorkerResultVersionConflict) {
		t.Fatalf("late result accepted after snapshot change: %v", err)
	}
	if _, err := f.store.GetGradeByAdapterRequestID(context.Background(), workerServiceTenant, f.run.RequestID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late result wrote grade: %v", err)
	}
	task, err := f.runtime.Get(context.Background(), workerServiceTenant, f.task.ID)
	if err != nil || task.Status != workerruntime.StatusLeased {
		t.Fatalf("rejected completion changed worker task: %#v %v", task, err)
	}
}

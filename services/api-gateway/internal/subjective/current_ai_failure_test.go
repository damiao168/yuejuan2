package subjective

import (
	"context"
	"testing"
)

func TestMemoryCurrentAIFailureUsesLatestVersionAndClearsAfterSuccess(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	store.BindReviewTaskRun("tenant-a", "review-1", "")
	value := ContextForTest("short_answer", 8, "answer", nil)
	value.SegmentID = "segment-1"
	value.AssessmentSnapshot.ID = "snapshot-1"
	value.AssessmentSnapshot.QuestionID = value.Question.ID
	store.AddContext("tenant-a", value.SegmentID, value)

	run, err := store.GetOrCreateRun(ctx, "tenant-a", "actor", CreateRunInput{
		AnswerSegmentID: value.SegmentID, AnswerVersion: value.AnswerVersion,
		QuestionID: value.Question.ID, ExamQuestionSnapshotID: value.AssessmentSnapshot.ID, RubricVersion: value.Rubric.Version,
		ModelVersion: "model", PromptVersion: "prompt", RequestID: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := &ExecutionDiagnostics{
		FailureKind: "output_truncated", FinishOutcome: "output_limit",
		Attempts: 1, ProviderRequestCount: 1, UsageStatus: "unavailable",
	}
	if _, err := store.UpdateRun(ctx, "tenant-a", run.ID, UpdateRunInput{
		Status: RunFailed, ErrorCode: "ai_output_incomplete", ExecutionDiagnostics: diagnostics,
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.CurrentAIFailure(ctx, "tenant-a", "review-1", value.SegmentID, value.Question.ID, "snapshot-1", value.Rubric.Version); err != nil || got != "output_truncated" {
		t.Fatalf("expected current failure, got %q, %v", got, err)
	}

	if updated, err := store.UpdateRun(ctx, "tenant-a", run.ID, UpdateRunInput{Status: RunSucceeded}); err != nil || updated.ExecutionDiagnostics != nil {
		t.Fatalf("success retained failure diagnostics: %#v %v", updated.ExecutionDiagnostics, err)
	}
	if persisted, err := store.GetRun(ctx, "tenant-a", run.ID); err != nil || persisted.ExecutionDiagnostics != nil {
		t.Fatalf("success response hid rather than cleared stored diagnostics: %#v %v", persisted.ExecutionDiagnostics, err)
	}
	if got, err := store.CurrentAIFailure(ctx, "tenant-a", "review-1", value.SegmentID, value.Question.ID, "snapshot-1", value.Rubric.Version); err != nil || got != "" {
		t.Fatalf("success must clear old failure, got %q, %v", got, err)
	}
}

func TestMemoryCurrentAIFailureDoesNotCrossTenantOrAnswerVersion(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	store.BindReviewTaskRun("tenant-a", "review-1", "")
	value := ContextForTest("short_answer", 8, "answer", nil)
	value.SegmentID = "segment-1"
	value.AssessmentSnapshot.ID = "snapshot-1"
	store.AddContext("tenant-a", value.SegmentID, value)
	run, err := store.GetOrCreateRun(ctx, "tenant-a", "actor", CreateRunInput{
		AnswerSegmentID: value.SegmentID, AnswerVersion: value.AnswerVersion,
		QuestionID: value.Question.ID, ExamQuestionSnapshotID: value.AssessmentSnapshot.ID, RubricVersion: value.Rubric.Version,
		ModelVersion: "model", PromptVersion: "prompt", RequestID: "request-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRun(ctx, "tenant-a", run.ID, UpdateRunInput{Status: RunFailed, ExecutionDiagnostics: &ExecutionDiagnostics{
		FailureKind: "schema_invalid", FinishOutcome: "completed", Attempts: 1,
		ProviderRequestCount: 1, UsageStatus: "unavailable",
	}}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.CurrentAIFailure(ctx, "tenant-b", "review-1", value.SegmentID, value.Question.ID, "snapshot-1", value.Rubric.Version); err != nil || got != "" {
		t.Fatalf("cross-tenant failure leaked: %q, %v", got, err)
	}
	value.AnswerVersion = "answer-v2"
	store.AddContext("tenant-a", value.SegmentID, value)
	if got, err := store.CurrentAIFailure(ctx, "tenant-a", "review-1", value.SegmentID, value.Question.ID, "snapshot-1", value.Rubric.Version); err != nil || got != "" {
		t.Fatalf("old answer version blocked current answer: %q, %v", got, err)
	}
}

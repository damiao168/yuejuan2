package subjective

import (
	"context"
	"errors"
	"testing"
)

func TestPanelStoreKeepsOneFrozenPanelAndOneRunPerRole(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	input := CreatePanelInput{
		AnswerSegmentID: "segment-1", AnswerVersion: "answer-v1", QuestionID: "question-1",
		RubricVersion: "rubric-v1", MaxScore: 4, DecisionConfig: PanelDecisionConfig{},
	}
	panel, err := store.GetOrCreatePanel(ctx, "tenant-1", "actor-1", input)
	if err != nil {
		t.Fatal(err)
	}
	runInput := CreateRunInput{
		PanelID: panel.ID, AgentRole: AgentRolePrimaryA, RequestID: "panel-a",
		AnswerSegmentID: input.AnswerSegmentID, AnswerVersion: input.AnswerVersion,
		QuestionID: input.QuestionID, RubricVersion: input.RubricVersion,
		ModelVersion: "model-a", PromptVersion: "prompt-v1", MinConfidence: .8,
	}
	run, err := store.GetOrCreateRun(ctx, "tenant-1", "actor-1", runInput)
	if err != nil || run.PanelID != panel.ID {
		t.Fatalf("valid panel run was rejected: %#v err=%v", run, err)
	}
	if run.Status != RunQueued || run.AttemptCount != 0 {
		t.Fatalf("panel role did not wait for an execution claim: %#v", run)
	}
	otherRequest := runInput
	otherRequest.RequestID = "panel-a-again"
	if _, err := store.GetOrCreateRun(ctx, "tenant-1", "actor-1", otherRequest); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second run for the same role was accepted: %v", err)
	}
	claimed, owner, err := store.ClaimPanelRun(ctx, "tenant-1", run.ID)
	if err != nil || !owner || claimed.Status != RunProcessing || claimed.AttemptCount != 1 {
		t.Fatalf("panel role was not claimed exactly once: %#v owner=%v err=%v", claimed, owner, err)
	}
	if _, owner, err := store.ClaimPanelRun(ctx, "tenant-1", run.ID); err != nil || owner {
		t.Fatalf("second executor acquired the same panel role: owner=%v err=%v", owner, err)
	}
	wrongSnapshot := runInput
	wrongSnapshot.AgentRole, wrongSnapshot.RequestID, wrongSnapshot.AnswerVersion = AgentRolePrimaryB, "panel-b", "answer-v2"
	if _, err := store.GetOrCreateRun(ctx, "tenant-1", "actor-1", wrongSnapshot); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("different answer version entered a frozen panel: %v", err)
	}
	if _, err := store.UpdateRun(ctx, "tenant-1", run.ID, UpdateRunInput{Status: RunSucceeded, GradeID: "grade-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePanel(ctx, "tenant-1", panel.ID, UpdatePanelInput{
		Status: PanelComparing, PrimaryARunID: run.ID,
	}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("panel compared before primary B completed: %v", err)
	}
	bInput := runInput
	bInput.AgentRole, bInput.RequestID, bInput.ModelVersion = AgentRolePrimaryB, "panel-b", "model-b"
	bRun, err := store.GetOrCreateRun(ctx, "tenant-1", "actor-1", bInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, owner, err := store.ClaimPanelRun(ctx, "tenant-1", bRun.ID); err != nil || !owner {
		t.Fatalf("primary B could not be claimed: owner=%v err=%v", owner, err)
	}
	if _, err := store.UpdateRun(ctx, "tenant-1", bRun.ID, UpdateRunInput{Status: RunSucceeded, GradeID: "grade-b"}); err != nil {
		t.Fatal(err)
	}
	resolvedScore := 2.0
	if _, err := store.UpdatePanel(ctx, "tenant-1", panel.ID, UpdatePanelInput{
		Status: PanelComparing, PrimaryARunID: run.ID, PrimaryBRunID: bRun.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePanel(ctx, "tenant-1", panel.ID, UpdatePanelInput{
		Status: PanelResolved, ResolvedScore: &resolvedScore, ResolutionSource: ResolutionPrimaryConsensus,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdatePanel(ctx, "tenant-1", panel.ID, UpdatePanelInput{Status: PanelComparing}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("terminal panel regressed to comparing: %v", err)
	}
	replay, err := store.GetOrCreatePanel(ctx, "tenant-1", "actor-1", input)
	if err != nil || replay.ID != panel.ID || replay.Status != PanelResolved {
		t.Fatalf("completed panel was duplicated: %#v err=%v", replay, err)
	}
	changed := input
	changed.QuestionID = "question-2"
	if _, err := store.GetOrCreatePanel(ctx, "tenant-1", "actor-1", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed question reused frozen panel identity: %v", err)
	}
}

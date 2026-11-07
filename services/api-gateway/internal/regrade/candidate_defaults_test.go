package regrade

import (
	"context"
	"testing"
)

type candidateDefaultRecordingStore struct {
	*MemoryStore
	input CandidateInput
}

func (s *candidateDefaultRecordingStore) RecordCandidate(ctx context.Context, tenant, item, actor string, input CandidateInput) (Item, error) {
	s.input = input
	return s.MemoryStore.RecordCandidate(ctx, tenant, item, actor, input)
}

func TestCandidateOmittedSelectionsRemainAnArrayAndDeltaWaitsForReview(t *testing.T) {
	store := &candidateDefaultRecordingStore{MemoryStore: NewMemoryStore()}
	store.SeedPublishedRelease("tenant", "exam", "release", 1, true, map[string][]SourceItem{"question": {{SubmissionID: "paper", OldFinalGradeID: "grade", OldScore: 2, MaxScore: 5}}})
	service := NewService(store)
	ctx := t.Context()
	created, err := service.Create(ctx, "tenant", "exam", "question", "manager", CreateInput{SourceReleaseID: "release", ReasonCode: ReasonOther, ReasonText: "candidate defaults", Strategy: StrategyHumanRecheck, AssigneeID: "reviewer", IdempotencyKey: "candidate-defaults-command"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, "tenant", created.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, "tenant", created.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(ctx, "tenant", created.Items[0].ID, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := service.RecordCandidate(ctx, "tenant", claimed.ID, "reviewer", CandidateInput{Score: 3, ExpectedRevision: claimed.Revision})
	if err != nil || store.input.RubricSelections == nil || len(store.input.RubricSelections) != 0 || candidate.Delta != nil {
		t.Fatalf("candidate defaults violate persistence contract: %+v %+v %v", store.input, candidate, err)
	}
	resolved, err := service.Review(ctx, "tenant", candidate.ID, "manager", ReviewInput{Decision: ReviewAccept, ExpectedRevision: candidate.Revision, Note: "accept independently"})
	if err != nil || resolved.Delta == nil || *resolved.Delta != 1 {
		t.Fatalf("reviewed difference missing: %+v %v", resolved, err)
	}
}

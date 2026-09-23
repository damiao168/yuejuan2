package scorerelease

import (
	"context"
	"errors"
	"testing"
)

func TestRegradePublishRejectsStaleSourceWithoutChangingCurrent(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	base, err := service.Create(ctx, tenant, exam, actor, CreateInput{Reason: "initial", IdempotencyKey: "stale-base-001"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, base.ID, actor); err != nil {
		t.Fatal(err)
	}
	makeDraft := func(key string, score float64) Release {
		t.Helper()
		r, err := service.CreateFromRegrade(ctx, tenant, exam, actor, CreateRegradeInput{
			SourceReleaseID: base.ID, QuestionID: "question-1", Reason: "reviewed correction", IdempotencyKey: key,
			Changes: []RegradeChange{{SubmissionID: "submission-1", QuestionID: "question-1", Score: score, MaxScore: 5, ReviewedGradeID: key}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	a, b := makeDraft("stale-draft-001", 5), makeDraft("stale-draft-002", 3)
	corrected, err := service.Get(ctx, tenant, a.ID)
	if err != nil || len(corrected.Questions) != 1 || corrected.Questions[0].Explanation.Feedback != "" || len(corrected.Questions[0].Explanation.RubricSummary) != 0 {
		t.Fatalf("regrade retained stale explanation: %+v %v", corrected, err)
	}
	if _, err = service.Publish(ctx, tenant, a.ID, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(ctx, tenant, b.ID, actor); !errors.Is(err, ErrStaleSource) {
		t.Fatalf("publish stale: %v", err)
	}
	current, err := service.CurrentPublished(ctx, tenant, exam)
	if err != nil || current.Release.ID != a.ID || current.Items[0].TotalScore != 5 {
		t.Fatalf("current changed: %+v %v", current, err)
	}
	draft, err := service.Get(ctx, tenant, b.ID)
	if err != nil || draft.Release.Status != StatusDraft {
		t.Fatalf("rejected draft changed: %+v %v", draft, err)
	}
	if _, err = service.Publish(ctx, tenant, a.ID, actor); err != nil {
		t.Fatalf("published retry: %v", err)
	}
}

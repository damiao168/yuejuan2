package calibration

import (
	"context"
	"errors"
	"testing"
)

type failCompletionOnce struct {
	Store
	failed bool
}

func (s *failCompletionOnce) CompleteSession(ctx context.Context, tenant string, session Session, qualification Qualification) (Session, Qualification, error) {
	if !s.failed {
		s.failed = true
		return Session{}, Qualification{}, context.DeadlineExceeded
	}
	return s.Store.CompleteSession(ctx, tenant, session, qualification)
}

func TestLastAttemptRetryCompletesPersistedSession(t *testing.T) {
	service, _, _ := testService(t, 1)
	service.store = &failCompletionOnce{Store: service.store}
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "tenant-a", "exam-1", "question-1", "grader-1")
	if err != nil {
		t.Fatal(err)
	}
	input := SubmitAttemptInput{GoldPaperID: session.Samples[0].GoldPaperID, SubmittedScore: 2, RubricSelections: map[string]any{"concept": true, "evidence": "partial"}}
	if _, _, _, err = service.SubmitAttempt(ctx, "tenant-a", session.ID, input); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first: %v", err)
	}
	changed := input
	changed.SubmittedScore = 1
	if _, _, _, err = service.SubmitAttempt(ctx, "tenant-a", session.ID, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry: %v", err)
	}
	_, completed, qualification, err := service.SubmitAttempt(ctx, "tenant-a", session.ID, input)
	if err != nil || completed.Status != SessionPassed || qualification == nil || qualification.Status != QualificationQualified {
		t.Fatalf("recovery: %+v %+v %v", completed, qualification, err)
	}
	_, _, _, attempts, err := service.store.GetSession(ctx, "tenant-a", session.ID)
	if err != nil || len(attempts) != 1 {
		t.Fatalf("attempts duplicated: %d %v", len(attempts), err)
	}
}

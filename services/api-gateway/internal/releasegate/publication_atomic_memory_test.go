package releasegate

import (
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

func TestMemoryPublicationRechecksLateRegradeAndCurrentBaseGate(t *testing.T) {
	for _, kind := range []string{"regrade", "base_gate"} {
		t.Run(kind, func(t *testing.T) {
			store := scorerelease.NewMemoryStore()
			store.SeedFacts("exam", []scorerelease.SubmissionFact{{StudentID: "student", SubmissionID: "submission", TotalScore: 4, MaxScore: 5, Status: "confirmed", Questions: []scorerelease.QuestionFact{{QuestionID: "question", QuestionNo: "1", FinalGradeID: "final", Score: 4, MaxScore: 5, SourceType: "single_review"}}}})
			releases := scorerelease.NewService(store)
			draft, err := releases.Create(t.Context(), "tenant", "exam", "manager", scorerelease.CreateInput{Reason: "synthetic", IdempotencyKey: "synthetic-memory"})
			if err != nil {
				t.Fatal(err)
			}
			reader := &batch54RegradeReader{}
			gates := NewService(NewMemoryStore(), releases).WithRegradeBlockerReader(reader)
			old, err := gates.RecheckForPublish(t.Context(), "tenant", "exam", draft.ID, "manager")
			if err != nil || !old.Evaluation.Passed {
				t.Fatalf("initial control: %#v %v", old, err)
			}
			publisher := publicationWindow{Service: releases, before: func() {
				if kind == "regrade" {
					reader.count = 1
				} else {
					store.SetGateIssues("exam", []scorerelease.GateIssue{{Code: "new_base_blocker", Blocking: true, Count: 1}})
				}
			}}
			_, evidence, err := NewPublicationCoordinator(gates, publisher).Publish(t.Context(), "tenant", "exam", draft.ID, "manager")
			if !errors.Is(err, scorerelease.ErrGateBlocked) || evidence.ID == "" || evidence.Evaluation.Passed {
				t.Fatalf("late change bypassed memory final check: %#v %v", evidence, err)
			}
		})
	}
}

func TestPublicationRejectsPublisherWithoutAtomicGate(t *testing.T) {
	publisher := &batch54Publisher{}
	gates := NewService(NewMemoryStore(), fakeBaseGate{gate: scorerelease.Gate{Passed: true}})
	_, _, err := NewPublicationCoordinator(gates, publisher).Publish(t.Context(), "tenant", "exam", "release", "manager")
	if !errors.Is(err, ErrAtomicPublicationUnavailable) || publisher.calls != 0 {
		t.Fatalf("unguarded publisher was used: calls=%d err=%v", publisher.calls, err)
	}
}

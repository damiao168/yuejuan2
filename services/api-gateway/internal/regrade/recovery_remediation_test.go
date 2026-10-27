package regrade

import (
	"errors"
	"testing"
)

func recoveryMemoryFixture(t *testing.T) (*MemoryStore, *Service, Summary) {
	t.Helper()
	store := NewMemoryStore()
	store.SeedPublishedRelease("tenant", "exam", "release", 1, true, map[string][]SourceItem{"question": {{SubmissionID: "submission", OldFinalGradeID: "grade", OldScore: 1, MaxScore: 5}}})
	service := NewService(store)
	summary, err := service.Create(t.Context(), "tenant", "exam", "question", "manager", CreateInput{SourceReleaseID: "release", ReasonCode: ReasonQualityIssue, ReasonText: "synthetic recovery", Strategy: StrategyHumanRecheck, IdempotencyKey: "recovery-remediation", AssigneeID: "grader"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(t.Context(), "tenant", summary.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(t.Context(), "tenant", summary.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	return store, service, summary
}

func TestExceptionCanOnlyFinishAfterExplicitReviewedResolution(t *testing.T) {
	for _, decision := range []string{ReviewAccept, ReviewReject} {
		t.Run(decision, func(t *testing.T) {
			store, service, summary := recoveryMemoryFixture(t)
			item, err := service.Claim(t.Context(), "tenant", summary.Items[0].ID, "grader")
			if err != nil {
				t.Fatal(err)
			}
			item, err = service.RecordCandidate(t.Context(), "tenant", item.ID, "grader", CandidateInput{Score: 3, ExpectedRevision: item.Revision})
			if err != nil {
				t.Fatal(err)
			}
			item, err = service.Review(t.Context(), "tenant", item.ID, "manager", ReviewInput{Decision: ReviewException, Note: "原异常证据有疑问", ExpectedRevision: item.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Finalize(t.Context(), "tenant", summary.Job.ID, "manager"); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("unresolved exception bypassed: %v", err)
			}
			if _, err := service.Review(t.Context(), "tenant", item.ID, "manager", ReviewInput{Decision: decision, ExpectedRevision: item.Revision}); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("missing resolution explanation accepted: %v", err)
			}
			if _, err := service.Review(t.Context(), "tenant", item.ID, "manager", ReviewInput{Decision: decision, Note: "已补充核对", ExpectedRevision: item.Revision - 1}); !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("stale exception revision accepted: %v", err)
			}
			if _, err := service.Pause(t.Context(), "tenant", summary.Job.ID, "manager"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Review(t.Context(), "tenant", item.ID, "manager", ReviewInput{Decision: decision, Note: "已补充核对", ExpectedRevision: item.Revision}); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("paused exception accepted: %v", err)
			}
			job, err := service.Resume(t.Context(), "tenant", summary.Job.ID, "manager")
			if err != nil || job.Status != StatusDiffReview {
				t.Fatalf("resume=%#v %v", job, err)
			}
			item, err = service.Review(t.Context(), "tenant", item.ID, "manager", ReviewInput{Decision: decision, Note: "已重新核对原图和冻结标准", ExpectedRevision: item.Revision})
			if err != nil || item.Status != ItemResolved {
				t.Fatalf("resolution=%#v %v", item, err)
			}
			job, err = service.Finalize(t.Context(), "tenant", summary.Job.ID, "manager")
			if err != nil || job.Status != StatusReadyForRelease {
				t.Fatalf("finalize=%#v %v", job, err)
			}
			actual, err := store.Get(t.Context(), "tenant", summary.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range actual.Events {
				if event.Type == "item_reviewed" && event.Payload["previous_status"] == ItemException {
					found = event.Payload["previous_review_note"] == "原异常证据有疑问" && event.Payload["note"] == "已重新核对原图和冻结标准"
				}
			}
			if !found {
				t.Fatalf("exception/resolution audit history lost: %#v", actual.Events)
			}
		})
	}
}

func TestResumeRestoresPhaseFromExistingItemFacts(t *testing.T) {
	score := 2.0
	for _, tc := range []struct {
		name        string
		items       []Item
		want        string
		finalizable bool
	}{
		{"no candidate", []Item{{Status: ItemPending}}, StatusRunning, false},
		{"partial candidates", []Item{{Status: ItemAwaitingReview, CandidateScore: &score}, {Status: ItemPending}}, StatusDiffReview, false},
		{"all candidates", []Item{{Status: ItemAwaitingReview, CandidateScore: &score}}, StatusDiffReview, false},
		{"all resolved", []Item{{Status: ItemResolved, CandidateScore: &score}}, StatusDiffReview, true},
		{"resolved without candidate", []Item{{Status: ItemResolved}}, StatusDiffReview, true},
		{"unresolved exception", []Item{{Status: ItemException, CandidateScore: &score}}, StatusDiffReview, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.jobs["job"] = Job{ID: "job", TenantID: "tenant", Status: StatusPaused}
			store.items["job"] = tc.items
			job, err := store.Resume(t.Context(), "tenant", "job", "manager")
			if err != nil || job.Status != tc.want {
				t.Fatalf("resume=%#v %v want=%s", job, err, tc.want)
			}
			if _, err := store.Resume(t.Context(), "tenant", "job", "manager"); err != nil {
				t.Fatalf("resume retry: %v", err)
			}
			_, err = store.Finalize(t.Context(), "tenant", "job", "manager")
			if (err == nil) != tc.finalizable {
				t.Fatalf("finalize=%v want allowed=%t", err, tc.finalizable)
			}
		})
	}
}

func TestExceptionEvidenceRemainsAvailableOnlyUntilResolution(t *testing.T) {
	store, service, claimed := claimIndependentRegradeFixture(t, t.Context())
	item, err := service.RecordCandidate(t.Context(), "tenant-a", claimed.ID, "reviewer-a", CandidateInput{Score: 4, ExpectedRevision: claimed.Revision})
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.Review(t.Context(), "tenant-a", item.ID, "manager-a", ReviewInput{Decision: ReviewException, Note: "原图待核对", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	material, err := store.GetReviewContext(t.Context(), "tenant-a", item.ID)
	if err != nil || material.Context.ExpectedRevision != item.Revision || material.Context.Answer.RawAnswer == "" {
		t.Fatalf("exception evidence unavailable: %#v %v", material, err)
	}
	if id, err := store.GetReviewSegmentID(t.Context(), "tenant-a", item.ID); err != nil || id != "segment-a" {
		t.Fatalf("exception image unavailable: %s %v", id, err)
	}
	if _, err := store.GetReviewContext(t.Context(), "tenant-b", item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant evidence: %v", err)
	}
	if _, err := service.Review(t.Context(), "tenant-a", item.ID, "manager-a", ReviewInput{Decision: ReviewReject, Note: "重新核对后保留原分", ExpectedRevision: item.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetReviewContext(t.Context(), "tenant-a", item.ID); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("resolved evidence remains readable: %v", err)
	}
}

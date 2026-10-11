package regrade

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegradeFreezesPublishedReleaseAndRequiresNewReleasePlan(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	store.SetNow(func() time.Time { return now })
	store.SeedPublishedRelease("tenant-a", "exam-a", "release-v1", 1, true, map[string][]SourceItem{
		"question-a": {
			{SubmissionID: "submission-1", StudentID: "student-1", OldFinalGradeID: "final-1", OldScore: 2, MaxScore: 5},
			{SubmissionID: "submission-2", StudentID: "student-2", OldFinalGradeID: "final-2", OldScore: 5, MaxScore: 5},
		},
	})
	service := NewService(store)

	preview, err := service.Preview(context.Background(), "tenant-a", "exam-a", "question-a", "release-v1", Selector{})
	if err != nil || preview.AffectedCount != 2 || preview.PotentialDelta.Min != -5 || preview.PotentialDelta.Max != 3 {
		t.Fatalf("preview = %#v, err=%v", preview, err)
	}
	severity := 2.0
	summary, err := service.Create(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", CreateInput{
		SourceReleaseID: "release-v1", ReasonCode: ReasonAnswerKeyError, ReasonText: "answer key correction confirmed",
		Strategy: StrategyHumanRecheck, SeverityDelta: &severity, AssigneeID: "reviewer-a", IdempotencyKey: "regrade-a19-test-key",
	})
	if err != nil || summary.Job.Status != StatusAwaitingApproval || len(summary.Items) != 2 {
		t.Fatalf("create = %#v, err=%v", summary, err)
	}
	// 重试保持幂等，不会创建第二条可变成绩流程。
	again, err := service.Create(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", CreateInput{
		SourceReleaseID: "release-v1", ReasonCode: ReasonAnswerKeyError, ReasonText: "answer key correction confirmed",
		Strategy: StrategyHumanRecheck, SeverityDelta: &severity, AssigneeID: "reviewer-a", IdempotencyKey: "regrade-a19-test-key",
	})
	if err != nil || again.Job.ID != summary.Job.ID || len(again.Items) != 2 {
		t.Fatalf("idempotent create = %#v, err=%v", again, err)
	}
	completeRegradeCandidateReview(t, service, summary)
	if _, err = service.Finalize(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), "tenant-a", summary.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Job.Status != StatusReadyForRelease || loaded.ReleasePlan == nil || loaded.ReleasePlan.SourceReleaseID != "release-v1" || len(loaded.ReleasePlan.ResolvedItems) != 2 {
		t.Fatalf("finalized summary = %#v", loaded)
	}
	if len(loaded.DiffHistogram) != 2 || len(loaded.SevereChanges) != 1 || loaded.SevereChanges[0].SubmissionID != "submission-2" {
		t.Fatalf("diff detail = %#v", loaded)
	}
	// 不可变的来源发布仍保留旧分数；A19 只提供 ReleasePlan，由 A18 实例化为后续版本。
	source, err := store.SourceItems(context.Background(), "tenant-a", "exam-a", "question-a", "release-v1", Selector{})
	if err != nil || source[0].OldScore != 2 || source[1].OldScore != 5 {
		t.Fatalf("source release fact was changed: %#v, err=%v", source, err)
	}
}

func TestRegradePauseResumeRevisionAndTenantIsolation(t *testing.T) {
	store := NewMemoryStore()
	store.SeedPublishedRelease("tenant-a", "exam-a", "release-v1", 1, true, map[string][]SourceItem{"question-a": {{SubmissionID: "submission-1", OldFinalGradeID: "final-1", OldScore: 1, MaxScore: 4}}})
	service := NewService(store)
	summary, err := service.Create(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", CreateInput{SourceReleaseID: "release-v1", ReasonCode: ReasonRubricError, ReasonText: "criterion changed", Strategy: StrategyHumanRecheck, IdempotencyKey: "regrade-a19-pause-key"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Start(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Pause(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(context.Background(), "tenant-a", summary.Items[0].ID, "reviewer-a"); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expected paused claim to fail, got %v", err)
	}
	if _, err = service.Resume(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	item, err := service.Claim(context.Background(), "tenant-a", summary.Items[0].ID, "reviewer-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.RecordCandidate(context.Background(), "tenant-a", item.ID, "reviewer-a", CandidateInput{Score: 3, ExpectedRevision: item.Revision - 1}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected revision conflict, got %v", err)
	}
	if _, err = service.Get(context.Background(), "tenant-b", summary.Job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected tenant isolation, got %v", err)
	}
}

func TestRegradeExceptionBlocksFinalize(t *testing.T) {
	store := NewMemoryStore()
	store.SeedPublishedRelease("tenant-a", "exam-a", "release-v1", 1, true, map[string][]SourceItem{"question-a": {{SubmissionID: "submission-1", OldFinalGradeID: "final-1", OldScore: 1, MaxScore: 2}}})
	service := NewService(store)
	summary, err := service.Create(context.Background(), "tenant-a", "exam-a", "question-a", "manager-a", CreateInput{SourceReleaseID: "release-v1", ReasonCode: ReasonQualityIssue, ReasonText: "quality incident", Strategy: StrategyHumanRecheck, IdempotencyKey: "regrade-a19-exception-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = service.Approve(context.Background(), "tenant-a", summary.Job.ID, "manager-a")
	_, _ = service.Start(context.Background(), "tenant-a", summary.Job.ID, "manager-a")
	item, err := service.Claim(context.Background(), "tenant-a", summary.Items[0].ID, "reviewer-a")
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.RecordCandidate(context.Background(), "tenant-a", item.ID, "reviewer-a", CandidateInput{Score: 2, ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Review(context.Background(), "tenant-a", item.ID, "manager-a", ReviewInput{Decision: ReviewException, ExpectedRevision: item.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Finalize(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("expected unresolved exception to block finalization, got %v", err)
	}
}

// 幂等创建确认后才批准和启动；每份答卷先写候选分数，再人工确认原版本。
func completeRegradeCandidateReview(t *testing.T, service *Service, summary Summary) {
	t.Helper()
	if _, err := service.Approve(context.Background(), "tenant-a", summary.Job.ID, "approver-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(context.Background(), "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	for _, original := range summary.Items {
		item, err := service.Claim(context.Background(), "tenant-a", original.ID, "reviewer-a")
		if err != nil {
			t.Fatal(err)
		}
		candidate := 3.0
		if item.SubmissionID == "submission-2" {
			candidate = 2.0
		}
		item, err = service.RecordCandidate(context.Background(), "tenant-a", item.ID, "reviewer-a", CandidateInput{Score: candidate, ExpectedRevision: item.Revision, RequireManualReview: true})
		if err != nil || item.Status != ItemAwaitingReview {
			t.Fatalf("candidate = %#v, err=%v", item, err)
		}
		item, err = service.Review(context.Background(), "tenant-a", item.ID, "manager-a", ReviewInput{Decision: ReviewAccept, ExpectedRevision: item.Revision})
		if err != nil || item.Status != ItemResolved || item.ReviewedScore == nil {
			t.Fatalf("review = %#v, err=%v", item, err)
		}
	}
}

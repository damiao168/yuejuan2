package regraderelease

import (
	"context"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/regrade"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

func TestReadyRegradeCreatesChangedSuccessorFromFrozenRelease(t *testing.T) {
	ctx := context.Background()
	regradeStore := regrade.NewMemoryStore()
	regradeStore.SeedPublishedRelease("tenant", "exam", "release-1", 1, true, map[string][]regrade.SourceItem{
		"question": {{SubmissionID: "submission", OldFinalGradeID: "final", OldScore: 2, MaxScore: 5}},
	})
	regrades := regrade.NewService(regradeStore)
	summary, err := regrades.Create(ctx, "tenant", "exam", "question", "manager", regrade.CreateInput{SourceReleaseID: "release-1", ReasonCode: regrade.ReasonRubricError, ReasonText: "rubric correction", Strategy: regrade.StrategyHumanRecheck, IdempotencyKey: "regrade-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = regrades.Approve(ctx, "tenant", summary.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err = regrades.Start(ctx, "tenant", summary.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	item, err := regrades.Claim(ctx, "tenant", summary.Items[0].ID, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = regrades.RecordCandidate(ctx, "tenant", item.ID, "reviewer", regrade.CandidateInput{Score: 4, ExpectedRevision: item.Revision, RequireManualReview: true}); err != nil {
		t.Fatal(err)
	}
	loaded, _ := regrades.Get(ctx, "tenant", summary.Job.ID)
	if _, err = regrades.Review(ctx, "tenant", loaded.Items[0].ID, "manager", regrade.ReviewInput{Decision: regrade.ReviewAccept, ReviewedScore: float64Ptr(4), ExpectedRevision: loaded.Items[0].Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err = regrades.Finalize(ctx, "tenant", summary.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}

	releases := scorerelease.NewMemoryStore()
	releases.SeedFacts("exam", []scorerelease.SubmissionFact{{SubmissionID: "submission", TotalScore: 2, MaxScore: 5, Status: "confirmed", Questions: []scorerelease.QuestionFact{{QuestionID: "question", QuestionNo: "1", FinalGradeID: "final", Score: 2, MaxScore: 5, SourceType: "single_review"}}}})
	// 使用重评存储所用的相同不可变事实实例化来源发布。交接桥接不读取当前事实。
	source, err := releases.Create(ctx, "tenant", "exam", "manager", scorerelease.CreateInput{Reason: "initial", IdempotencyKey: "source-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = releases.Publish(ctx, "tenant", source.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	svc := NewService(regrades, scorerelease.NewService(releases))
	successor, err := svc.Create(ctx, "tenant", summary.Job.ID, "manager", Input{Reason: "corrected", IdempotencyKey: "successor-key-123"})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := releases.Get(ctx, "tenant", successor.ID)
	if err != nil || detail.Release.Source != scorerelease.SourceRegrade || detail.Release.SourceReleaseID != source.ID || detail.Items[0].TotalScore != 4 || detail.Questions[0].Score != 4 {
		t.Fatalf("successor must be a changed immutable source snapshot: %#v %#v, err=%v", detail.Release, detail, err)
	}
}

func float64Ptr(value float64) *float64 { return &value }

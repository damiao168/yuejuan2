package regrade

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestRegradeGraderContextIsIndependentOfSourceReleaseFacts(t *testing.T) {
	ctx := context.Background()
	store, service, claimed := claimIndependentRegradeFixture(t, ctx)
	loaded, err := service.GetGraderContext(ctx, "tenant-a", claimed.ID, "reviewer-a")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Question.QuestionNo != "Q1" || loaded.FrozenRubric.Version != "2" || loaded.Answer.RawAnswer != "student answer" || loaded.Answer.SegmentImageURL == "" {
		t.Fatalf("context = %#v", loaded)
	}
	raw, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"submission-a", "old-grade-secret", "old_score", "original_reviewer"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("grader context leaked %q: %s", forbidden, raw)
		}
	}
	if _, err := service.GetGraderContext(ctx, "tenant-a", claimed.ID, "other-reviewer"); !errors.Is(err, ErrAssignmentForbidden) {
		t.Fatalf("other reviewer context err=%v", err)
	}
	segmentID, err := service.GetSegmentID(ctx, "tenant-a", claimed.ID, "reviewer-a")
	if err != nil || segmentID != "segment-a" {
		t.Fatalf("segment bridge = %q, %v", segmentID, err)
	}
	if _, err := service.RecordCandidate(ctx, "tenant-a", claimed.ID, "reviewer-a", CandidateInput{Score: 4, ExpectedRevision: claimed.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetGraderContext(ctx, "tenant-a", claimed.ID, "reviewer-a"); !errors.Is(err, ErrAssignmentForbidden) {
		t.Fatalf("completed context should not remain readable, err=%v", err)
	}
	assertRegradeManagementContextLifecycle(t, ctx, store, service, claimed)
}

func claimIndependentRegradeFixture(t *testing.T, ctx context.Context) (*MemoryStore, *Service, Item) {
	t.Helper()
	store := NewMemoryStore()
	store.SeedPublishedRelease("tenant-a", "exam-a", "release-a", 1, true, map[string][]SourceItem{
		"question-a": {{SubmissionID: "submission-a", OldFinalGradeID: "old-grade-secret", OldScore: 1, MaxScore: 5}},
	})
	store.SeedGraderEvidence("submission-a", MemoryGraderEvidence{
		Question:     GraderQuestion{ID: "question-a", QuestionNo: "Q1", QuestionType: "extended_response", Score: 5, Stem: "Explain the result", KnowledgePoints: []string{"reasoning"}},
		FrozenRubric: paper.Rubric{ID: "rubric-v2", Version: "2", MaxScore: 5, Points: []paper.RubricPoint{{ID: "reason", Description: "valid reasoning", Score: 5}}},
		RawAnswer:    "student answer", SegmentStatus: "completed", AnswerSegmentID: "segment-a",
	})
	service := NewService(store).WithContextSource(store)
	summary, err := service.Create(ctx, "tenant-a", "exam-a", "question-a", "manager-a", CreateInput{
		SourceReleaseID: "release-a", ReasonCode: ReasonRubricError, ReasonText: "new rubric", Strategy: StrategyHumanRecheck,
		AssigneeID: "reviewer-a", IdempotencyKey: "regrade-grader-context",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Approve(ctx, "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, "tenant-a", summary.Job.ID, "manager-a"); err != nil {
		t.Fatal(err)
	}
	claimed, err := service.Claim(ctx, "tenant-a", summary.Items[0].ID, "reviewer-a")
	if err != nil {
		t.Fatal(err)
	}
	return store, service, claimed
}

// 独立评分完成后管理复核按当前修订读取；接受后上下文和图片入口一同关闭。
func assertRegradeManagementContextLifecycle(t *testing.T, ctx context.Context, store *MemoryStore, service *Service, claimed Item) {
	t.Helper()
	// 独立评分提交后，管理复核读取真实材料和候选；跨租户仍不得读取。
	managed, err := store.GetReviewContext(ctx, "tenant-a", claimed.ID)
	if err != nil || managed.Item.OldScore != 1 || managed.Item.CandidateScore == nil || *managed.Item.CandidateScore != 4 || managed.Context.Answer.RawAnswer != "student answer" || managed.Context.ExpectedRevision != managed.Item.Revision {
		t.Fatalf("management context=%#v, %v", managed, err)
	}
	managed.Context.FrozenRubric.Points[0].Description = "reader mutation"
	managedAgain, err := store.GetReviewContext(ctx, "tenant-a", claimed.ID)
	if err != nil || managedAgain.Context.FrozenRubric.Points[0].Description != "valid reasoning" {
		t.Fatalf("frozen material mutated: %#v %v", managedAgain, err)
	}
	if _, err := store.GetReviewContext(ctx, "tenant-b", claimed.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant context err=%v", err)
	}
	if _, err := store.GetReviewSegmentID(ctx, "tenant-a", claimed.ID); err != nil {
		t.Fatalf("management image scope: %v", err)
	}
	if _, err := service.Review(ctx, "tenant-a", claimed.ID, "manager-a", ReviewInput{Decision: ReviewAccept, ExpectedRevision: managed.Item.Revision - 1}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale material revision accepted: %v", err)
	}
	if _, err := service.Review(ctx, "tenant-a", claimed.ID, "manager-a", ReviewInput{Decision: ReviewAccept, ExpectedRevision: managed.Item.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetReviewContext(ctx, "tenant-a", claimed.ID); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("reviewed candidate remains readable: %v", err)
	}
	if _, err := store.GetReviewSegmentID(ctx, "tenant-a", claimed.ID); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("reviewed candidate image remains readable: %v", err)
	}
}

package calibration

import (
	"context"
	"testing"
)

func TestBatch25SessionGoldReferenceKeepsFirstMatch(t *testing.T) {
	references := []goldReference{
		{GoldSample: GoldSample{GoldPaperID: "other", MaxScore: 2}},
		{GoldSample: GoldSample{GoldPaperID: "target", MaxScore: 5}, ReferenceScore: 2},
		{GoldSample: GoldSample{GoldPaperID: "target", MaxScore: 9}, ReferenceScore: 7},
	}
	if got := sessionGoldReference(references, "target"); got != &references[1] {
		t.Fatalf("lookup must keep the first frozen reference: %#v", got)
	}
	if got := sessionGoldReference(references, "missing"); got != nil {
		t.Fatalf("missing reference = %#v", got)
	}
	if got := sessionGoldReference(nil, "target"); got != nil {
		t.Fatalf("empty reference set = %#v", got)
	}
}

func TestBatch25MissingGoldPrecedesScoreBoundsAndLeavesNoAttempt(t *testing.T) {
	service, _, store := testService(t, 1)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "tenant-a", "exam-1", "question-1", "grader-1")
	if err != nil {
		t.Fatal(err)
	}
	// 样本不属于会话且分数也越界时，仍先报告样本边界，不能新增作答事实。
	_, _, _, err = service.SubmitAttempt(ctx, "tenant-a", session.ID, SubmitAttemptInput{GoldPaperID: "missing", SubmittedScore: -1})
	if err != ErrSampleNotInSession {
		t.Fatalf("missing sample error = %v", err)
	}
	stored, _, _, attempts, err := store.GetSession(ctx, "tenant-a", session.ID)
	if err != nil || len(attempts) != 0 || stored.SubmittedCount != 0 || stored.Status != SessionInProgress {
		t.Fatalf("rejected input mutated session: %#v attempts=%d err=%v", stored, len(attempts), err)
	}
}

package regrade

import (
	"context"
	"errors"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// 未分配任务由两名阅卷人竞争领取；成功者同时得到分配、队列和独立材料，失败者不能读取。
func TestUnassignedClaimAtomicallyAssignsWinnerAndMaterials(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	store.SeedPublishedRelease("tenant", "exam", "release", 1, true, map[string][]SourceItem{"question": {{SubmissionID: "submission", OldFinalGradeID: "old", OldScore: 1, MaxScore: 5}}})
	store.SeedGraderEvidence("submission", MemoryGraderEvidence{Question: GraderQuestion{ID: "question", Score: 5}, FrozenRubric: paper.Rubric{ID: "rubric", MaxScore: 5}, AnswerSegmentID: "segment"})
	service := NewService(store).WithContextSource(store)
	created, err := service.Create(ctx, "tenant", "exam", "question", "manager", CreateInput{SourceReleaseID: "release", ReasonCode: ReasonOther, ReasonText: "confirmed", Strategy: StrategyHumanRecheck, IdempotencyKey: "claim-assignment"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Approve(ctx, "tenant", created.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Start(ctx, "tenant", created.Job.ID, "manager"); err != nil {
		t.Fatal(err)
	}
	type claimResult struct {
		actor string
		item  Item
		err   error
	}
	results := make(chan claimResult, 2)
	var wg sync.WaitGroup
	for _, actor := range []string{"first", "second"} {
		wg.Add(1)
		go func(actor string) {
			defer wg.Done()
			item, e := service.Claim(ctx, "tenant", created.Items[0].ID, actor)
			results <- claimResult{actor, item, e}
		}(actor)
	}
	wg.Wait()
	close(results)
	winner, loser := "", ""
	for result := range results {
		if result.err == nil {
			if winner != "" || result.item.AssignedTo != result.actor || result.item.ClaimedBy != result.actor {
				t.Fatalf("inconsistent claim: %+v", result)
			}
			winner = result.actor
		} else {
			if !errors.Is(result.err, ErrAssignmentForbidden) {
				t.Fatal(result.err)
			}
			loser = result.actor
		}
	}
	if winner == "" || loser == "" {
		t.Fatal("claim must have one winner")
	}
	items, err := service.ListAssigned(ctx, "tenant", winner)
	if err != nil || len(items) != 1 {
		t.Fatalf("winner queue: %+v %v", items, err)
	}
	if _, err = service.GetGraderContext(ctx, "tenant", created.Items[0].ID, winner); err != nil {
		t.Fatal(err)
	}
	if segment, e := service.GetSegmentID(ctx, "tenant", created.Items[0].ID, winner); e != nil || segment != "segment" {
		t.Fatalf("winner image: %q %v", segment, e)
	}
	if _, err = service.GetGraderContext(ctx, "tenant", created.Items[0].ID, loser); !errors.Is(err, ErrAssignmentForbidden) {
		t.Fatalf("loser context: %v", err)
	}
	// 修复前已保存的本人领取可重领补齐分配，随后普通重放不能递增修订。
	store.mu.Lock()
	store.items[created.Job.ID][0].AssignedTo = ""
	store.mu.Unlock()
	recovered, err := service.Claim(ctx, "tenant", created.Items[0].ID, winner)
	if err != nil || recovered.AssignedTo != winner {
		t.Fatalf("legacy claim recovery: %+v %v", recovered, err)
	}
	replay, err := service.Claim(ctx, "tenant", created.Items[0].ID, winner)
	if err != nil || replay.Revision != recovered.Revision {
		t.Fatalf("claim replay changed revision: %+v %v", replay, err)
	}
}

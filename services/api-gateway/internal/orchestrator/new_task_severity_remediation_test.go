package orchestrator

import (
	"context"
	"testing"
)

// 只用合成任务核对新增待执行任务是否改变未处理的失败/人工复核事实。
// 第二个任务正常完成后使用生产聚合重新计算，作为同一事实集合的状态对照。
func TestBatch15CreatingTaskPreservesExistingRunSeverity(t *testing.T) {
	for _, prior := range []string{"failed", "requires_human_review"} {
		t.Run(prior, func(t *testing.T) {
			ctx := context.Background()
			store := NewMemoryStore()
			run, err := store.CreateRun(ctx, "tenant", "actor", CreateRunInput{WorkflowType: "custom", TargetType: "exam", TargetID: "exam"})
			if err != nil {
				t.Fatal(err)
			}
			first, err := store.CreateTask(ctx, "tenant", run.ID, "actor", CreateTaskInput{AgentType: "audit_agent"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.StartTask(ctx, "tenant", first.ID); err != nil {
				t.Fatal(err)
			}
			if prior == "failed" {
				_, err = store.FailTask(ctx, "tenant", first.ID, "synthetic failure")
			} else {
				low := .5
				_, err = store.CompleteTask(ctx, "tenant", first.ID, CompleteTaskInput{Confidence: &low})
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.GetRun(ctx, "tenant", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			second, err := store.CreateTask(ctx, "tenant", run.ID, "actor", CreateTaskInput{AgentType: "analytics_agent"})
			if err != nil {
				t.Fatal(err)
			}
			after, err := store.GetRun(ctx, "tenant", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.StartTask(ctx, "tenant", second.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.CompleteTask(ctx, "tenant", second.ID, CompleteTaskInput{}); err != nil {
				t.Fatal(err)
			}
			recomputed, err := store.GetRun(ctx, "tenant", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("first_task_status=%s before_new_task=%s after_new_task=%s after_second_completion=%s", prior, before.Status, after.Status, recomputed.Status)
			if after.Status != prior {
				t.Fatalf("new queued task hid unresolved %s run status", prior)
			}
		})
	}
}

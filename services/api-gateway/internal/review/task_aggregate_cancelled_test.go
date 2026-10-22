package review

import (
	"context"
	"database/sql/driver"
	"testing"
)

func TestTaskAggregatesKeepCancelledHistoryOutsideRemainingWork(t *testing.T) {
	statuses := []string{"pending", "assigned", "in_progress", "returned", "submitted", "completed", "cancelled"}
	memory := NewMemoryStore()
	rows := make([][]driver.Value, 0, len(statuses))
	for _, status := range statuses {
		memory.tasks[key("tenant", status)] = ReviewTask{ID: status, TenantID: "tenant", AssignedTo: "reviewer", Status: status}
		rows = append(rows, []driver.Value{status, "reviewer", int64(1)})
	}
	postgres, script := openReviewBatch34Store(t, []reviewBatch34Step{{
		name: "all status aggregate", fragment: "GROUP BY status, assigned_to", columns: 3,
		args: []any{"tenant", "", "", "", "tenant", "", "", "", "", "", false, "", ""}, rows: rows,
	}})
	for _, store := range []Store{memory, postgres} {
		result, err := store.AggregateTasks(context.Background(), "tenant", ListFilter{})
		if err != nil || result.TotalCount != 7 || result.CompletedCount != 2 || result.RemainingCount != 4 || result.StatusCounts["cancelled"] != 1 {
			t.Fatalf("aggregate=%+v err=%v", result, err)
		}
		if len(result.Reviewers) != 1 || result.Reviewers[0].TotalCount != 7 || result.Reviewers[0].CompletedCount != 2 || result.Reviewers[0].RemainingCount != 4 {
			t.Fatalf("reviewer aggregate=%+v", result.Reviewers)
		}
	}
	if script.next != len(script.steps) || script.commits != 0 || script.rolls != 0 {
		t.Fatalf("aggregate query boundary changed: queries=%d commits=%d rollbacks=%d", script.next, script.commits, script.rolls)
	}
}

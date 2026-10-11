package review

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestTaskAggregateAndCursorCover120TasksAcrossAssignments(t *testing.T) {
	store := NewMemoryStore()
	seedReviewCursorTasks(store)

	handler := NewHandler(store, nil, nil, nil)

	query := url.Values{"limit": {"50"}, "exam_id": {"exam-a"}}
	assertReviewCursorChain(t, handler, query)

	// 转移会立即改变聚合结果；新游标链必须省略已转移任务，同时保持原任务顺序。
	moved := store.tasks[key("tenant-a", "task-075")]
	moved.AssignedTo = "reviewer-b"
	moved.Revision++
	store.tasks[key("tenant-a", moved.ID)] = moved
	query.Del("cursor")
	page := readReviewerCursorPage(t, handler, query)
	if page.Aggregate.TotalCount != 119 || page.Aggregate.RemainingCount != 112 {
		t.Fatalf("transfer aggregate=%+v", page.Aggregate)
	}
	query.Set("status", "submitted")
	submitted := readReviewerCursorPage(t, handler, query)
	if submitted.Aggregate.TotalCount != 7 || submitted.Aggregate.CompletedCount != 7 || submitted.Aggregate.RemainingCount != 0 || len(submitted.Tasks) != 7 {
		t.Fatalf("status-filtered aggregate=%+v tasks=%d", submitted.Aggregate, len(submitted.Tasks))
	}
	query.Set("exam_id", "exam-b")
	otherExam := readReviewerCursorPage(t, handler, query)
	if otherExam.Aggregate.TotalCount != 0 || len(otherExam.Tasks) != 0 {
		t.Fatalf("exam filter leaked tasks: %+v", otherExam)
	}
	_, err := store.AggregateTasks(context.Background(), "tenant-a", ListFilter{AssignedTo: "reviewer-b", ExamID: "exam-a"})
	if err != nil {
		t.Fatal(err)
	}
}

type reviewCursorPage struct {
	Tasks     []ReviewTask  `json:"tasks"`
	Next      string        `json:"next_cursor"`
	HasMore   bool          `json:"has_more"`
	Aggregate TaskAggregate `json:"aggregate"`
}

// 聚合与游标使用同一租户和人员过滤；外租户、其他考试任务只用于检查隔离。
func seedReviewCursorTasks(store *MemoryStore) {
	createdAt := time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC)
	for index := 0; index < 120; index++ {
		id := fmt.Sprintf("task-%03d", index)
		status := "assigned"
		if index < 7 {
			status = "submitted"
		}
		store.tasks[key("tenant-a", id)] = ReviewTask{
			ID: id, TenantID: "tenant-a", ExamID: "exam-a", AssignedTo: "reviewer-a",
			Status: status, Priority: 10, CreatedAt: createdAt.Add(time.Duration(index) * time.Second), Revision: 1,
		}
	}
	store.tasks[key("tenant-b", "foreign-task")] = ReviewTask{ID: "foreign-task", TenantID: "tenant-b", ExamID: "exam-a", AssignedTo: "reviewer-a", Status: "assigned"}
	store.tasks[key("tenant-a", "different-exam")] = ReviewTask{ID: "different-exam", TenantID: "tenant-a", ExamID: "exam-b", AssignedTo: "reviewer-a", Status: "assigned"}

}

func readReviewerCursorPage(t *testing.T, handler *Handler, query url.Values) reviewCursorPage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?"+query.Encode(), nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "reviewer-a", TenantID: "tenant-a", Roles: []string{"grader"}}))
	response := httptest.NewRecorder()
	handler.ListTasks(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	var page reviewCursorPage
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

// 先穷尽原游标链并检查去重，随后主测试才转移任务；同一个查询对象保留末页游标。
func assertReviewCursorChain(t *testing.T, handler *Handler, query url.Values) {
	t.Helper()
	seen := map[string]bool{}
	for pageNo := 0; pageNo < 3; pageNo++ {
		page := readReviewerCursorPage(t, handler, query)
		if page.Aggregate.TotalCount != 120 || page.Aggregate.CompletedCount != 7 || page.Aggregate.RemainingCount != 113 ||
			len(page.Aggregate.Reviewers) != 1 || page.Aggregate.Reviewers[0].ReviewerID != "reviewer-a" {
			t.Fatalf("page %d aggregate=%+v", pageNo+1, page.Aggregate)
		}
		recordUniqueReviewTaskIDs(t, seen, page.Tasks)
		if pageNo < 2 && !page.HasMore {
			t.Fatalf("page %d ended early", pageNo+1)
		}
		if pageNo == 2 && page.HasMore {
			t.Fatal("third page should be terminal")
		}
		query.Set("cursor", page.Next)
	}
	if len(seen) != 120 {
		t.Fatalf("seen %d of 120 tasks", len(seen))
	}

}

func recordUniqueReviewTaskIDs(t *testing.T, seen map[string]bool, tasks []ReviewTask) {
	t.Helper()
	for _, task := range tasks {
		if seen[task.ID] {
			t.Fatalf("duplicate %s", task.ID)
		}
		seen[task.ID] = true
	}
}

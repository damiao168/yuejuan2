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

	handler := NewHandler(store, nil, nil, nil)
	list := func(query url.Values) struct {
		Tasks     []ReviewTask  `json:"tasks"`
		Next      string        `json:"next_cursor"`
		HasMore   bool          `json:"has_more"`
		Aggregate TaskAggregate `json:"aggregate"`
	} {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?"+query.Encode(), nil)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "reviewer-a", TenantID: "tenant-a", Roles: []string{"grader"}}))
		response := httptest.NewRecorder()
		handler.ListTasks(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		var page struct {
			Tasks     []ReviewTask  `json:"tasks"`
			Next      string        `json:"next_cursor"`
			HasMore   bool          `json:"has_more"`
			Aggregate TaskAggregate `json:"aggregate"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		return page
	}

	query := url.Values{"limit": {"50"}, "exam_id": {"exam-a"}}
	seen := map[string]bool{}
	for pageNo := 0; pageNo < 3; pageNo++ {
		page := list(query)
		if page.Aggregate.TotalCount != 120 || page.Aggregate.CompletedCount != 7 || page.Aggregate.RemainingCount != 113 ||
			len(page.Aggregate.Reviewers) != 1 || page.Aggregate.Reviewers[0].ReviewerID != "reviewer-a" {
			t.Fatalf("page %d aggregate=%+v", pageNo+1, page.Aggregate)
		}
		for _, task := range page.Tasks {
			if seen[task.ID] {
				t.Fatalf("duplicate %s", task.ID)
			}
			seen[task.ID] = true
		}
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

	// A transfer changes the aggregate immediately; a fresh cursor chain must
	// omit the moved task without corrupting the original task order.
	moved := store.tasks[key("tenant-a", "task-075")]
	moved.AssignedTo = "reviewer-b"
	moved.Revision++
	store.tasks[key("tenant-a", moved.ID)] = moved
	query.Del("cursor")
	page := list(query)
	if page.Aggregate.TotalCount != 119 || page.Aggregate.RemainingCount != 112 {
		t.Fatalf("transfer aggregate=%+v", page.Aggregate)
	}
	query.Set("status", "submitted")
	submitted := list(query)
	if submitted.Aggregate.TotalCount != 7 || submitted.Aggregate.CompletedCount != 7 || submitted.Aggregate.RemainingCount != 0 || len(submitted.Tasks) != 7 {
		t.Fatalf("status-filtered aggregate=%+v tasks=%d", submitted.Aggregate, len(submitted.Tasks))
	}
	query.Set("exam_id", "exam-b")
	otherExam := list(query)
	if otherExam.Aggregate.TotalCount != 0 || len(otherExam.Tasks) != 0 {
		t.Fatalf("exam filter leaked tasks: %+v", otherExam)
	}
	_, err := store.AggregateTasks(context.Background(), "tenant-a", ListFilter{AssignedTo: "reviewer-b", ExamID: "exam-a"})
	if err != nil {
		t.Fatal(err)
	}
}

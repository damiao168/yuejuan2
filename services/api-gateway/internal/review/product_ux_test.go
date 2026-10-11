package review

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProductTaskFiltersKeepPagesAndAggregateInSameScope(t *testing.T) {
	store := NewMemoryStore()
	for index, task := range []ReviewTask{
		{ID: "a", ExamID: "exam-a", ExamName: "期中考试", Subject: "数学", QuestionID: "q1", AnonymousCode: "密%号", Status: "pending"},
		{ID: "b", ExamID: "exam-a", ExamName: "期中考试", Subject: "数学", QuestionID: "q1", AnonymousCode: "密%号", Status: "returned"},
		{ID: "c", ExamID: "exam-a", ExamName: "期中考试", Subject: "数学", QuestionID: "q2", AnonymousCode: "密%号", Status: "pending"},
		{ID: "d", ExamID: "exam-a", ExamName: "期中考试", Subject: "数学", QuestionID: "q1", AnonymousCode: "密%号", Status: "assigned", AssignedTo: "grader"},
		{ID: "e", ExamID: "exam-a", ExamName: "期中考试", Subject: "数学", QuestionID: "q1", AnonymousCode: "密%号", Status: "submitted"},
		{ID: "f", ExamID: "exam-b", ExamName: "期中考试", Subject: "英语", QuestionID: "q1", AnonymousCode: "密%号", Status: "pending"},
	} {
		task.TenantID = "tenant"
		task.CreatedAt = time.Unix(int64(index), 0)
		store.tasks[key("tenant", task.ID)] = task
	}
	store.tasks[key("other", "foreign")] = ReviewTask{ID: "foreign", TenantID: "other", ExamID: "exam-a", Subject: "数学", QuestionID: "q1", AnonymousCode: "密%号", Status: "pending"}
	filter := ListFilter{ExamID: "exam-a", Subject: "数学", QuestionID: "q1", Q: "%", Unassigned: true, Status: "active", Limit: 1}
	page, err := store.ListTasks(context.Background(), "tenant", filter)
	if err != nil || len(page) != 1 || page[0].ID != "a" {
		t.Fatalf("first page=%+v err=%v", page, err)
	}
	aggregate, err := store.AggregateTasks(context.Background(), "tenant", filter)
	if err != nil || aggregate.TotalCount != 2 || aggregate.RemainingCount != 2 {
		t.Fatalf("aggregate=%+v err=%v", aggregate, err)
	}
	filter.CursorID = page[0].ID
	filter.CursorCreatedAt = page[0].CreatedAt
	filter.CursorPriority = page[0].Priority
	page, err = store.ListTasks(context.Background(), "tenant", filter)
	if err != nil || len(page) != 1 || page[0].ID != "b" {
		t.Fatalf("second page=%+v err=%v", page, err)
	}
	aggregate, _ = store.AggregateTasks(context.Background(), "tenant", filter)
	if aggregate.TotalCount != 2 {
		t.Fatalf("cursor changed full aggregate: %+v", aggregate)
	}
	filter.Subject = "英语"
	page, _ = store.ListTasks(context.Background(), "tenant", filter)
	if len(page) != 0 {
		t.Fatal("subject filter leaked another exam")
	}
}

func TestProductTaskUnassignedFilterCannotBypassWorkerAssignment(t *testing.T) {
	store := NewMemoryStore()
	store.tasks[key("tenant", "pending")] = ReviewTask{ID: "pending", TenantID: "tenant", Status: "pending"}
	handler := NewHandler(store, nil, nil, nil)
	for _, query := range []string{"unassigned=true", "unassigned=invalid"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?"+query, nil)
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "grader", TenantID: "tenant", Roles: []string{"grader"}}))
		response := httptest.NewRecorder()
		handler.ListTasks(response, request)
		if query == "unassigned=invalid" {
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid bool status=%d", response.Code)
			}
			continue
		}
		var result struct {
			Tasks     []ReviewTask  `json:"tasks"`
			Aggregate TaskAggregate `json:"aggregate"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Tasks) != 0 || result.Aggregate.TotalCount != 0 {
			t.Fatalf("worker scope response=%s", response.Body.String())
		}
	}
}

func TestTaskFrozenRubricReportsMissingInsteadOfInventingVersion(t *testing.T) {
	for _, snapshot := range []map[string]any{nil, {}, {"points": "corrupted"}} {
		rubric := taskFrozenRubric(snapshot, "question", 10)
		if rubric.Status != "missing" || rubric.Version != "" || len(rubric.Points) != 0 || rubric.MaxScore != 10 {
			t.Fatalf("missing snapshot result=%+v", rubric)
		}
	}
	rubric := taskFrozenRubric(map[string]any{"version": "frozen-v1", "points": []any{map[string]any{"id": "point", "score": 2, "description": "依据"}}, "deductions": []any{"错单位扣1分"}, "examples": []any{"样例"}}, "question", 10)
	if rubric.Version != "frozen-v1" || len(rubric.Points) != 1 || len(rubric.Deductions) != 1 || len(rubric.Examples) != 1 {
		t.Fatalf("frozen reference lost: %+v", rubric)
	}
}

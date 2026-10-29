package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestReviewedHistoryIncludesSubmittedAndCompletedButKeepsOwnScopeAndPagination(t *testing.T) {
	store := NewMemoryStore()
	for index, item := range []ReviewTask{
		{ID: "submitted", Status: "submitted", AssignedTo: "teacher"},
		{ID: "completed", Status: "completed", AssignedTo: "teacher"},
		{ID: "draft-only", Status: "in_progress", AssignedTo: "teacher"},
		{ID: "someone-else", Status: "submitted", AssignedTo: "other"},
		{ID: "cancelled", Status: "cancelled", AssignedTo: "teacher"},
	} {
		item.TenantID, item.ExamID, item.CreatedAt = "tenant", "exam", time.Unix(int64(index), 0)
		store.tasks[key("tenant", item.ID)] = item
	}
	handler := NewHandler(store, nil, nil, nil)
	query := url.Values{"status": {"reviewed"}, "assigned_to": {"other"}, "exam_id": {"exam"}, "limit": {"1"}}
	seen := []string{}
	for page := 0; page < 2; page++ {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?"+query.Encode(), nil)
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "teacher", TenantID: "tenant", Roles: []string{"grader"}}))
		response := httptest.NewRecorder()
		handler.ListTasks(response, request)
		var result reviewCursorPage
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("history response: %d %s", response.Code, response.Body.String())
		}
		if len(result.Tasks) != 1 || result.Aggregate.TotalCount != 2 || result.Aggregate.CompletedCount != 2 || result.Aggregate.RemainingCount != 0 {
			t.Fatalf("history aggregate/page mismatch: %+v", result)
		}
		seen = append(seen, result.Tasks[0].ID)
		if result.HasMore != (page == 0) {
			t.Fatalf("history pagination ended at page %d", page)
		}
		query.Set("cursor", result.Next)
	}
	if seen[0] != "submitted" || seen[1] != "completed" {
		t.Fatalf("history leaked another person's grade or a draft: %v", seen)
	}
}

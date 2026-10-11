package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/pagination"
)

func TestReviewListRejectsInvalidPriorityAndTimestampCursors(t *testing.T) {
	handler := NewHandler(NewMemoryStore(), nil, nil, nil)
	// 三段数量合法但优先级或时间损坏时，仍要拒绝，不能当作没有游标继续查询。
	for _, test := range []struct {
		name     string
		priority string
		time     string
	}{
		{name: "priority", priority: "bad", time: "2026-10-06T00:00:00Z"},
		{name: "timestamp", priority: "10", time: "bad"},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{"cursor": {pagination.EncodeParts(test.priority, test.time, "task")}}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks?"+query.Encode(), nil)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "grader", TenantID: "tenant", Roles: []string{"grader"}}))
			response := httptest.NewRecorder()
			handler.ListTasks(response, request)
			var body httpx.ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusBadRequest || body.Error.Code != "invalid_pagination" || body.Error.Message != invalidCursorMessage {
				t.Fatalf("invalid %s cursor response=%d %s", test.name, response.Code, response.Body.String())
			}
		})
	}
}

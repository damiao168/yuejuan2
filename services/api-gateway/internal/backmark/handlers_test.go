package backmark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestBackmarkHandlersReturnBoundedCursorPages(t *testing.T) {
	store := NewMemoryStore()
	now := time.Date(2026, 9, 15, 1, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	for index := 0; index < 3; index++ {
		id := fmt.Sprintf("task-%d", index)
		store.SeedSource(SourceTask{ReviewTaskID: id, OriginalGradeID: "grade-" + id, OriginalReviewer: "grader-a", OriginalScore: 1, MaxScore: 2, GradedAt: now})
	}
	handler := NewHandler(NewService(store), nil)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/exams/exam-1/questions/question-1/backmark-batches", strings.NewReader(`{"source_incident_id":"incident-1","reassigned_to":"grader-b","policy":{"disposition":"confirm"}}`))
	request.SetPathValue("examId", "exam-1")
	request.SetPathValue("questionId", "question-1")
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "manager-1", TenantID: "tenant-1"}))
	response := httptest.NewRecorder()
	handler.Create(response, request)
	var created struct {
		Summary Summary `json:"backmark"`
		Next    *string `json:"next_cursor"`
		More    *bool   `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || created.Next == nil || *created.Next != "" || created.More == nil || *created.More {
		t.Fatalf("create response does not match pagination contract: %s", response.Body.String())
	}
	for _, test := range []backmarkCursorRouteCase{
		{"batch items", "/api/v1/backmark-batches/" + created.Summary.Batch.ID, "manager-1", handler.Get},
		{"assigned items", "/api/v1/backmark-items/mine", "grader-b", handler.ListMine},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertBackmarkCursorRoute(t, test, created.Summary.Batch.ID)
		})
	}
}

func TestBackmarkHandlersRejectInvalidPagination(t *testing.T) {
	handler := NewHandler(NewService(NewMemoryStore()), nil)
	for _, query := range []string{"limit=0", "limit=201", "cursor=invalid"} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/backmark-batches?"+query, nil)
		request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "manager-1", TenantID: "tenant-1"}))
		response := httptest.NewRecorder()
		handler.List(response, request)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_pagination") {
			t.Fatalf("query %s: status=%d body=%s", query, response.Code, response.Body.String())
		}
	}
}

func TestGetContextReturnsBlindBackmarkSurface(t *testing.T) {
	store := NewMemoryStore()
	store.SeedSource(SourceTask{ReviewTaskID: "source-task-secret", OriginalGradeID: "old-grade-secret", OriginalReviewer: "grader-a", OriginalScore: 4, MaxScore: 5, GradedAt: time.Now().UTC()})
	service := NewService(store).WithContextSource(backmarkContextSource{context: review.TaskContext{
		Question:       paper.Question{ID: "question-1", QuestionNo: "Q1", QuestionType: "extended_response", Score: 5},
		FrozenRubric:   paper.Rubric{ID: "rubric-1", MaxScore: 5},
		AnswerArtifact: review.AnswerArtifact{AnswerSegmentID: "segment-secret", RawAnswer: "answer", Status: "completed"},
	}})
	summary, err := service.Create(context.Background(), "tenant-1", "exam-1", "question-1", "manager-1", CreateInput{
		SourceIncidentID: "incident-1", ReassignedTo: "grader-b", Policy: Policy{Disposition: DispositionConfirm},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/backmark-items/"+summary.Items[0].ID+"/context", nil)
	request.SetPathValue("itemId", summary.Items[0].ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "grader-b", TenantID: "tenant-1", Roles: []string{"grader"}}))
	response := httptest.NewRecorder()

	NewHandler(service, nil).GetContext(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{"source-task-secret", "old-grade-secret", "original_score", "original_reviewer", "segment-secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("blind context leaked %q: %s", forbidden, body)
		}
	}
	var decoded struct {
		Context GraderContext `json:"backmark_context"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Context.Question.QuestionNo != "Q1" || decoded.Context.Answer.SegmentImageURL == "" {
		t.Fatalf("missing grading context: %#v", decoded.Context)
	}
}

type backmarkCursorRouteCase struct {
	name   string
	path   string
	userID string
	handle http.HandlerFunc
}

func assertBackmarkCursorRoute(t *testing.T, test backmarkCursorRouteCase, batchID string) {
	t.Helper()
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 2; page++ {
		cursor = assertBackmarkCursorPage(t, test, batchID, page, cursor, seen)
	}
}

// 连续页共享已见ID，只在当前页内容与分页边界都正确时推进游标。
func assertBackmarkCursorPage(t *testing.T, test backmarkCursorRouteCase, batchID string, page int, cursor string, seen map[string]bool) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, test.path+"?limit=2&cursor="+cursor, nil)
	request.SetPathValue("batchId", batchID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: test.userID, TenantID: "tenant-1"}))
	response := httptest.NewRecorder()
	test.handle(response, request)
	var decoded struct {
		Summary Summary      `json:"backmark"`
		Items   []GraderItem `json:"backmark_items"`
		Next    string       `json:"next_cursor"`
		More    bool         `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, item := range decoded.Summary.Items {
		ids = append(ids, item.ID)
	}
	for _, item := range decoded.Items {
		ids = append(ids, item.ID)
	}
	if response.Code != http.StatusOK || len(ids) != 2-page || decoded.More != (page == 0) || (decoded.Next != "") != decoded.More {
		t.Fatalf("invalid page %d: %s", page, response.Body.String())
	}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("cursor repeated item %s", id)
		}
		seen[id] = true
	}
	return decoded.Next
}

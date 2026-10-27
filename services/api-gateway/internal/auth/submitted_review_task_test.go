package auth

import (
	"context"
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type submittedTaskBoundaryStore struct {
	*MemoryStore
	allowed bool
	err     error
	calls   int
}

func (s *submittedTaskBoundaryStore) CanReadSubmittedReviewTask(_ context.Context, tenantID, taskID, reviewerID string) (bool, error) {
	s.calls++
	return s.allowed && tenantID == "tenant-1" && taskID == "history-1" && reviewerID == "grader-1", s.err
}

func TestSubmittedReviewTaskReadBoundaryDoesNotExpandWriteOrSiblingAccess(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, owner, tenant string
		proof, permitted                  bool
		lookupError                       error
		want, calls                       int
	}{
		{"own context", "GET", "/review-tasks/history-1/context", "grader-1", "tenant-1", true, true, nil, 200, 1},
		{"own workspace", "GET", "/review-tasks/history-1/workspace", "grader-1", "tenant-1", true, true, nil, 200, 1},
		{"own image", "GET", "/review-tasks/history-1/segment-image", "grader-1", "tenant-1", true, true, nil, 200, 1},
		{"head image", "HEAD", "/review-tasks/history-1/segment-image", "grader-1", "tenant-1", true, true, nil, 200, 1},
		{"renew remains denied", "POST", "/review-tasks/history-1/renew", "grader-1", "tenant-1", true, true, nil, 403, 0},
		{"draft remains denied", "PUT", "/review-tasks/history-1/draft", "grader-1", "tenant-1", true, true, nil, 403, 0},
		{"submit remains denied", "POST", "/review-tasks/history-1/submit", "grader-1", "tenant-1", true, true, nil, 403, 0},
		{"annotation creation remains denied", "POST", "/review-tasks/history-1/annotations", "grader-1", "tenant-1", true, true, nil, 403, 0},
		{"another owner", "GET", "/review-tasks/history-1/context", "grader-2", "tenant-1", true, true, nil, 403, 0},
		{"another tenant", "GET", "/review-tasks/history-1/context", "grader-1", "tenant-2", true, true, nil, 403, 0},
		{"missing formal grade", "GET", "/review-tasks/history-1/context", "grader-1", "tenant-1", false, true, nil, 403, 1},
		{"role permission revoked", "GET", "/review-tasks/history-1/context", "grader-1", "tenant-1", true, false, nil, 403, 1},
		{"proof unavailable", "GET", "/review-tasks/history-1/context", "grader-1", "tenant-1", false, true, errors.New("unavailable"), 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &submittedTaskBoundaryStore{MemoryStore: NewMemoryStore(), allowed: tc.proof, err: tc.lookupError}
			store.AddResourceBoundary("review_task", "history-1", ResourceBoundary{TenantID: tc.tenant, AssignedTo: tc.owner})
			mux := http.NewServeMux()
			mux.Handle(tc.method+" /review-tasks/{id}/{action}", RequireRequestResourceBoundary(store)(RequirePermission("review:work")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))))
			user := User{ID: "grader-1", TenantID: "tenant-1"}
			if tc.permitted {
				user.Permissions = []string{"review:work"}
			}
			// 提交后活动任务集合为空；只读核验不能把整场考试或提交扩进普通作用域。
			scope := AccessScope{TenantID: user.TenantID, ActorID: user.ID, AssignedOnly: true}
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request = request.WithContext(WithAccessScope(WithUser(request.Context(), user), scope))
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != tc.want || store.calls != tc.calls {
				t.Fatalf("status=%d calls=%d, want %d/%d: %s", response.Code, store.calls, tc.want, tc.calls, response.Body.String())
			}
		})
	}
}

func TestSubmittedReviewTaskProofIsBoundToCurrentOwnerAndFormalGrade(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		store, script := openAuthBatch49Store(t, []authBatch49Step{{
			kind: "query", fragment: "AND t.status IN ('submitted','completed') AND t.deleted_at IS NULL\n    AND g.reviewer_id=$3::uuid AND g.deleted_at IS NULL",
			args: []any{"tenant-1", "task-1", "reviewer-1"}, columns: 1, rows: [][]driver.Value{{allowed}},
		}})
		got, err := store.CanReadSubmittedReviewTask(t.Context(), "tenant-1", "task-1", "reviewer-1")
		if err != nil || got != allowed {
			t.Fatalf("proof=%v err=%v want=%v", got, err, allowed)
		}
		assertAuthBatch49Script(t, script, "")
	}
}

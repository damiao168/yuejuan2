package review

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type auditBatchAssignmentStore struct {
	Store
	calls int
}

func (s *auditBatchAssignmentStore) BatchAssignTasks(context.Context, string, string, BatchAssignInput) ([]ReviewTask, error) {
	s.calls++
	return []ReviewTask{}, nil
}

func TestAuditBatchAssignmentChecksEveryBodyTaskBeforeMutation(t *testing.T) {
	actor := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	scope, err := auth.ResolveDeclaredAccessScope(actor)
	if err != nil {
		t.Fatal(err)
	}
	identities := auth.NewMemoryStore()
	identities.AddUser(auth.UserWithPassword{User: auth.User{ID: "grader", TenantID: "tenant", TenantCode: "demo", Username: "grader", Roles: []string{"grader"}, Status: "active"}, SchoolID: "school-a"})
	for _, school := range []string{"a", "b"} {
		identities.AddResourceBoundary("review_task", "task-"+school, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-" + school})
	}
	for _, tc := range []struct {
		name, ids string
		want      int
		calls     int
	}{
		{"managed", `["task-a"]`, http.StatusOK, 1},
		{"foreign", `["task-b"]`, http.StatusForbidden, 0},
		{"mixed", `["task-a","task-b"]`, http.StatusForbidden, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &auditBatchAssignmentStore{}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks/batch-assign", strings.NewReader(`{"task_ids":`+tc.ids+`,"assigned_to":"grader"}`))
			req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), actor), scope))
			rec := httptest.NewRecorder()
			NewHandler(store, identities, nil, nil).BatchAssignTasks(rec, req)
			if rec.Code != tc.want || store.calls != tc.calls {
				t.Fatalf("got status=%d writes=%d: %s", rec.Code, store.calls, rec.Body.String())
			}
		})
	}
}

func TestAuditCreateTaskChecksBodySegmentManagementBeforeMutation(t *testing.T) {
	identities := auth.NewMemoryStore()
	actor := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin", "teacher"}, Permissions: []string{"review:manage"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}, "teacher": map[string]any{"scope": "class", "class_ids": []string{"class-b"}}}}
	scope, err := auth.ResolveDeclaredAccessScope(actor)
	if err != nil {
		t.Fatal(err)
	}
	scope.SchoolIDs = append(scope.SchoolIDs, "school-b")
	scope.ExamIDs = []string{"exam-b"}
	store := NewMemoryStore()
	for _, school := range []string{"school-a", "school-b"} {
		segment := "segment-" + school
		identities.AddResourceBoundary("answer_segment", segment, auth.ResourceBoundary{TenantID: "tenant", SchoolID: school, ExamID: "exam-b"})
		store.AddContext("tenant", segment, Context{ExamID: "exam-b", Question: paper.Question{ID: "q", QuestionNo: "1", Score: 10}, SubmissionID: "submission"})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/review-tasks", strings.NewReader(`{"answer_segment_id":"`+segment+`","source":"manual_sample"}`))
		req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), actor), scope))
		rec := httptest.NewRecorder()
		NewHandler(store, identities, nil, nil).CreateTask(rec, req)
		want := http.StatusCreated
		if school == "school-b" {
			want = http.StatusForbidden
		}
		if rec.Code != want {
			t.Fatalf("%s returned %d: %s", school, rec.Code, rec.Body.String())
		}
	}
	tasks, err := store.ListTasks(t.Context(), "tenant", ListFilter{})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("unauthorized create mutated tasks: %d %v", len(tasks), err)
	}
}

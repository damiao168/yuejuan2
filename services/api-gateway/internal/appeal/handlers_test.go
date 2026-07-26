package appeal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// tenantOnlyScope reproduces the data_scope that CreateManagedUser writes for
// every account created through POST /api/v1/users: a role-keyed object with no
// student_id at any nesting level, so ScopedStudentID cannot resolve a student.
func tenantOnlyScope() map[string]any {
	return map[string]any{"student": map[string]any{"scope": "tenant"}}
}

func scopedUser(id string, scope map[string]any, permissions ...string) auth.User {
	if permissions == nil {
		permissions = []string{}
	}
	return auth.User{
		ID:          id,
		TenantID:    tenantID,
		Roles:       []string{"student"},
		Permissions: permissions,
		DataScope:   scope,
	}
}

func newScopeHandler(t *testing.T) (*Handler, map[string]Appeal) {
	t.Helper()
	store := seededAppealStore()
	// seededAppealStore leaves student-2 unpublished; publish it so both
	// students can hold an appeal and cross-student leaks are observable.
	store.AddSubmissionGrade(SubmissionGradeSeed{
		ID:            "submission-grade-2",
		ExamID:        "exam-1",
		SubmissionID:  "submission-2",
		StudentID:     "student-2",
		AnonymousCode: "ANON-002",
		TotalScore:    7,
		MaxScore:      10,
		Status:        "published",
		Locked:        true,
	})
	owned := map[string]Appeal{}
	for _, seed := range []struct{ studentID, actorID string }{
		{"student-1", "student-user-1"},
		{"student-2", "student-user-2"},
	} {
		item, err := store.CreateAppeal(context.Background(), tenantID, seed.actorID, CreateAppealInput{
			ExamID:     "exam-1",
			StudentID:  seed.studentID,
			TargetType: "exam",
			Reason:     "total score looks wrong",
		})
		if err != nil {
			t.Fatalf("create appeal for %s: %v", seed.studentID, err)
		}
		owned[seed.studentID] = item
	}
	return NewHandler(store, auth.NewMemoryStore()), owned
}

func listAppeals(t *testing.T, handler *Handler, user auth.User, query string) (int, []Appeal) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/appeals"+query, nil)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	recorder := httptest.NewRecorder()
	handler.ListAppeals(recorder, request)
	var body struct {
		Appeals []Appeal `json:"appeals"`
	}
	if recorder.Code == http.StatusOK {
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode appeals response: %v", err)
		}
	}
	return recorder.Code, body.Appeals
}

func TestListAppealsDeniesUnresolvableStudentScope(t *testing.T) {
	handler, _ := newScopeHandler(t)

	cases := []struct {
		name         string
		user         auth.User
		query        string
		wantStatus   int
		wantStudents []string
	}{
		{
			name:       "tenant only scope cannot list any appeal",
			user:       scopedUser("student-user-1", tenantOnlyScope()),
			query:      "",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "tenant only scope cannot borrow client student_id",
			user:       scopedUser("student-user-1", tenantOnlyScope()),
			query:      "?student_id=student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "empty data scope cannot list any appeal",
			user:       scopedUser("student-user-1", nil),
			query:      "?student_id=student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nested scope without student_id cannot list any appeal",
			user:       scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "class", "class_id": "class-a"}}),
			query:      "",
			wantStatus: http.StatusForbidden,
		},
		{
			name:         "flat scope is confined to own appeals",
			user:         scopedUser("student-user-1", map[string]any{"student_id": "student-1"}),
			query:        "",
			wantStatus:   http.StatusOK,
			wantStudents: []string{"student-1"},
		},
		{
			name:         "role keyed scope ignores client supplied student_id",
			user:         scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "self", "student_id": "student-1"}}),
			query:        "?student_id=student-2",
			wantStatus:   http.StatusOK,
			wantStudents: []string{"student-1"},
		},
		{
			name:         "manage permission still sees the tenant",
			user:         scopedUser("admin-user-1", tenantOnlyScope(), "appeal:manage"),
			query:        "",
			wantStatus:   http.StatusOK,
			wantStudents: []string{"student-1", "student-2"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			status, items := listAppeals(t, handler, testCase.user, testCase.query)
			if status != testCase.wantStatus {
				t.Fatalf("status = %d, want %d", status, testCase.wantStatus)
			}
			if len(items) != len(testCase.wantStudents) {
				t.Fatalf("returned %d appeals %v, want %d", len(items), studentIDsOf(items), len(testCase.wantStudents))
			}
			for _, wanted := range testCase.wantStudents {
				if !containsString(studentIDsOf(items), wanted) {
					t.Fatalf("appeals %v missing student %s", studentIDsOf(items), wanted)
				}
			}
			for _, item := range items {
				if len(testCase.wantStudents) == 1 && item.StudentID != testCase.wantStudents[0] {
					t.Fatalf("leaked appeal for student %s", item.StudentID)
				}
			}
		})
	}
}

func TestGetAppealDeniesUnresolvableStudentScope(t *testing.T) {
	handler, owned := newScopeHandler(t)

	cases := []struct {
		name       string
		user       auth.User
		appealID   string
		wantStatus int
	}{
		{
			name:       "tenant only scope cannot read another student appeal",
			user:       scopedUser("student-user-1", tenantOnlyScope()),
			appealID:   owned["student-2"].ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "empty data scope cannot read any appeal",
			user:       scopedUser("student-user-1", nil),
			appealID:   owned["student-1"].ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope cannot read another student appeal",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}),
			appealID:   owned["student-2"].ID,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope reads own appeal",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}),
			appealID:   owned["student-1"].ID,
			wantStatus: http.StatusOK,
		},
		{
			name:       "manage permission reads any appeal",
			user:       scopedUser("admin-user-1", tenantOnlyScope(), "appeal:manage"),
			appealID:   owned["student-2"].ID,
			wantStatus: http.StatusOK,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/appeals/"+testCase.appealID, nil)
			request.SetPathValue("id", testCase.appealID)
			request = request.WithContext(auth.WithUser(request.Context(), testCase.user))
			recorder := httptest.NewRecorder()
			handler.GetAppeal(recorder, request)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
		})
	}
}

func studentIDsOf(items []Appeal) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.StudentID)
	}
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

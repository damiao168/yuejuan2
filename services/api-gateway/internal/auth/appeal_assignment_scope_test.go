package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppealAssignmentBoundaryDoesNotExpandTeacherExamOrSubmissionAccess(t *testing.T) {
	scope := AccessScope{TenantID: "tenant", ActorID: "teacher", AssignedOnly: true, ExamIDs: []string{"exam"}, SubmissionIDs: []string{"submission"}}
	base := ResourceBoundary{ResourceType: "appeal", ResourceID: "appeal", TenantID: "tenant", SchoolID: "school", ExamID: "exam", SubmissionID: "submission", StudentID: "student", AssignedTo: "teacher"}
	for _, tc := range []struct {
		name    string
		change  func(*ResourceBoundary)
		allowed bool
	}{
		{"concrete assigned appeal", func(*ResourceBoundary) {}, true},
		{"another reviewer in same exam and submission", func(b *ResourceBoundary) { b.AssignedTo = "other" }, false},
		{"unassigned in same exam and submission", func(b *ResourceBoundary) { b.AssignedTo = "" }, false},
		{"another tenant", func(b *ResourceBoundary) { b.TenantID = "other-tenant" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			boundary := base
			tc.change(&boundary)
			if got := AllowsResourceBoundary(scope, boundary); got != tc.allowed {
				t.Fatalf("allowed=%v want %v", got, tc.allowed)
			}
		})
	}
	// 只有申诉分派也足以处理这条申诉；范围不需要增加考试、答卷、班级或学校。
	minimal := AccessScope{TenantID: "tenant", ActorID: "teacher", AssignedOnly: true}
	if !AllowsResourceBoundary(minimal, base) || len(minimal.ExamIDs) != 0 || len(minimal.SubmissionIDs) != 0 {
		t.Fatal("concrete appeal assignment must work without expanding the teacher scope")
	}
	if AllowsResourceBoundary(minimal, ResourceBoundary{ResourceType: "exam", ResourceID: "exam", TenantID: "tenant", SchoolID: "school", ExamID: "exam"}) ||
		AllowsResourceBoundary(minimal, ResourceBoundary{ResourceType: "submission", ResourceID: "submission", TenantID: "tenant", SchoolID: "school", ExamID: "exam", SubmissionID: "submission"}) {
		t.Fatal("appeal assignment exposed unrelated exam or submission routes")
	}
}

func TestAppealAssignmentBoundaryPreservesStudentAndManagerAccess(t *testing.T) {
	boundary := ResourceBoundary{ResourceType: "appeal", ResourceID: "appeal", TenantID: "tenant", SchoolID: "school", StudentID: "student", AssignedTo: "teacher"}
	student := AccessScope{TenantID: "tenant", ActorID: "student-user", StudentID: "student"}
	if !AllowsResourceBoundary(student, boundary) {
		t.Fatal("student lost access to own assigned appeal")
	}
	student.StudentID = "another-student"
	if AllowsResourceBoundary(student, boundary) {
		t.Fatal("student can read another student's appeal")
	}
	manager := AccessScope{TenantID: "tenant", ActorID: "admin", SchoolIDs: []string{"school"}, schoolWide: true, schoolManagementIDs: []string{"school"}}
	if !AllowsResourceBoundary(manager, boundary) || !AllowsManagedResourceBoundary(manager, boundary) {
		t.Fatal("school administrator lost existing management access")
	}
	teacher := AccessScope{TenantID: "tenant", ActorID: "teacher", AssignedOnly: true}
	if AllowsManagedResourceBoundary(teacher, boundary) {
		t.Fatal("appeal assignment became management authority")
	}
}

func TestAppealRecommendationResourceGuardRechecksCurrentAssignment(t *testing.T) {
	store := NewMemoryStore()
	scope := AccessScope{TenantID: "tenant", ActorID: "teacher", AssignedOnly: true}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/appeals/{id}/recommendation", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	for _, tc := range []struct {
		assignee string
		want     int
	}{{"teacher", http.StatusNoContent}, {"another-teacher", http.StatusForbidden}, {"", http.StatusForbidden}} {
		store.AddResourceBoundary("appeal", "appeal", ResourceBoundary{TenantID: "tenant", SchoolID: "school", AssignedTo: tc.assignee})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/appeals/appeal/recommendation", nil)
		request = request.WithContext(WithAccessScope(request.Context(), scope))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("assignee=%q status=%d want=%d", tc.assignee, response.Code, tc.want)
		}
	}
}

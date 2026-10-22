package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsPlatformWorkerRequiresPlatformTenantAndSupportedServiceRole(t *testing.T) {
	for _, tc := range []struct {
		name, tenant, role string
		allowed            bool
	}{
		{"platform page worker", PlatformTenantID, "page_processing_worker", true},
		{"platform subjective worker", PlatformTenantID, "subjective_grading_worker", true},
		{"school page worker", "school-tenant", "page_processing_worker", false},
		{"school subjective worker", "school-tenant", "subjective_grading_worker", false},
		{"platform administrator", PlatformTenantID, "platform_admin", false},
		{"school administrator", "school-tenant", "school_admin", false},
		{"unknown platform worker", PlatformTenantID, "other_worker", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPlatformWorker(User{TenantID: tc.tenant, Roles: []string{tc.role}}); got != tc.allowed {
				t.Fatalf("IsPlatformWorker=%t, want %t", got, tc.allowed)
			}
		})
	}
}

func TestScopedStudentIDSupportsFlatAndRoleKeyedScopes(t *testing.T) {
	flat := User{DataScope: map[string]any{"scope": "self", "student_id": "student-flat"}}
	if got, ok := ScopedStudentID(flat); !ok || got != "student-flat" {
		t.Fatalf("flat student scope expected student-flat, got %q ok=%t", got, ok)
	}

	roleKeyed := User{DataScope: map[string]any{
		"student": map[string]any{"scope": "self", "student_id": "student-role"},
	}}
	if got, ok := ScopedStudentID(roleKeyed); !ok || got != "student-role" {
		t.Fatalf("role-keyed student scope expected student-role, got %q ok=%t", got, ok)
	}
}

func TestResolveDeclaredAccessScopeCombinesRoleKeyedScopes(t *testing.T) {
	user := User{
		ID: "teacher-1", TenantID: "tenant-1", Roles: []string{"teacher", "grader"},
		DataScope: map[string]any{
			"teacher": map[string]any{"scope": "class", "school_id": "school-1", "class_ids": []any{"class-2", "class-1"}},
			"grader":  map[string]any{"scope": "exam_task", "review_task_id": "task-1"},
		},
	}
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	if scope.TenantWide || !scope.AssignedOnly || !scope.AllowsSchool("school-1") ||
		!scope.AllowsClass("class-1") || !scope.AllowsReviewTask("task-1") {
		t.Fatalf("unexpected combined access scope: %#v", scope)
	}
}

func TestResolveDeclaredAccessScopeCombinesSchoolAdminAndGraderWithoutTenantUpgrade(t *testing.T) {
	user := User{
		ID: "admin-grader-1", TenantID: "tenant-1", Roles: []string{"school_admin", "grader"},
		DataScope: map[string]any{
			"school_admin": map[string]any{"scope": "school", "school_id": "school-1"},
			"grader":       map[string]any{"scope": "exam_task", "review_task_id": "task-1"},
		},
	}
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	if scope.TenantWide || !scope.schoolWide || !scope.AssignedOnly || !scope.AllowsSchool("school-1") || !scope.AllowsReviewTask("task-1") {
		t.Fatalf("unexpected school administrator + grader union: %#v", scope)
	}
}

func TestResolveDeclaredAccessScopeDefaultsToDeny(t *testing.T) {
	tests := []User{
		{ID: "u-1", TenantID: "t-1", Roles: []string{"teacher"}, DataScope: map[string]any{}},
		{ID: "u-1", TenantID: "t-1", Roles: []string{"teacher"}, DataScope: map[string]any{"teacher": map[string]any{"scope": "school", "unexpected": true}}},
		{ID: "u-1", TenantID: "t-1", Roles: []string{"teacher"}, DataScope: map[string]any{"other_role": map[string]any{"scope": "tenant"}}},
		{ID: "u-1", TenantID: "t-1", Roles: []string{"student"}, DataScope: map[string]any{"scope": "self"}},
	}
	for _, user := range tests {
		if _, err := ResolveDeclaredAccessScope(user); !errors.Is(err, ErrAccessScopeMissing) && !errors.Is(err, ErrAccessScopeInvalid) {
			t.Fatalf("expected missing/invalid scope for %#v, got %v", user.DataScope, err)
		}
	}
}

func TestResolveDeclaredAccessScopeRejectsForgedPlatformScope(t *testing.T) {
	_, err := ResolveDeclaredAccessScope(User{
		ID: "tenant-admin", TenantID: "tenant-1", Roles: []string{"tenant_admin"},
		DataScope: map[string]any{"scope": "platform"},
	})
	if !errors.Is(err, ErrAccessScopeInvalid) {
		t.Fatalf("expected invalid platform scope, got %v", err)
	}
}

func TestOrganizationScopeProjectsResolvedBoundary(t *testing.T) {
	resolved := AccessScope{
		TenantID: "tenant-1", ActorID: "user-1",
		SchoolIDs: []string{"school-2", "school-1", "school-1"},
		GradeIDs:  []string{"grade-2"}, ClassIDs: []string{"class-3"},
	}.OrganizationScope()
	if resolved.TenantWide || len(resolved.SchoolIDs) != 2 || resolved.SchoolIDs[0] != "school-1" ||
		len(resolved.GradeIDs) != 1 || resolved.GradeIDs[0] != "grade-2" {
		t.Fatalf("unexpected organization scope: %#v", resolved)
	}
}

func TestAssignedOnlyIsNotATaskWildcard(t *testing.T) {
	for _, resource := range []string{"review_task", "arbitration_task"} {
		handler := RequireScopedResource(resource, "id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
		req := httptest.NewRequest(http.MethodGet, "/tasks/unassigned", nil)
		req.SetPathValue("id", "unassigned")
		req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: "t-1", ActorID: "u-1", AssignedOnly: true}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s AssignedOnly wildcard expected 403, got %d", resource, rec.Code)
		}
	}
}

func TestStudentIdentityIsNotAnExamWildcard(t *testing.T) {
	handler := RequireScopedResource("exam", "id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	req := httptest.NewRequest(http.MethodGet, "/exams/other", nil)
	req.SetPathValue("id", "other")
	req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: "t-1", ActorID: "u-1", StudentID: "student-1"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("student exam wildcard expected 403, got %d", rec.Code)
	}
}

func TestRoleKeyedScopeRejectsRoleScopeDrift(t *testing.T) {
	_, err := ResolveDeclaredAccessScope(User{ID: "grader-1", TenantID: "t-1", Roles: []string{"grader"}, DataScope: map[string]any{
		"grader": map[string]any{"scope": "school", "school_id": "school-a"},
	}})
	if !errors.Is(err, ErrAccessScopeInvalid) {
		t.Fatalf("grader school scope must fail closed, got %v", err)
	}
}

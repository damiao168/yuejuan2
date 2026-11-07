package auth

import "testing"

func TestManagedRoleAssignmentPolicy(t *testing.T) {
	tenantAdmin := User{Roles: []string{"tenant_admin"}}
	schoolAdmin := User{Roles: []string{"school_admin"}}
	for _, role := range []string{"teacher", "grader", "arbitrator"} {
		if !CanAssignManagedRole(schoolAdmin, role) {
			t.Fatalf("school administrator should assign %s", role)
		}
	}
	for _, role := range []string{"platform_admin", "tenant_admin", "school_admin", "auditor", "student", "page_processing_worker", "subjective_grading_worker"} {
		if CanAssignManagedRole(schoolAdmin, role) {
			t.Fatalf("school administrator must not assign %s", role)
		}
	}
	if !CanAssignManagedRole(tenantAdmin, "school_admin") || !CanAssignManagedRole(tenantAdmin, "auditor") {
		t.Fatal("tenant administrator should use the explicit higher-level assignment policy")
	}
}

func TestCanonicalHumanAndServiceScopes(t *testing.T) {
	want := map[string]string{
		"platform_admin": "platform", "tenant_admin": "tenant", "school_admin": "school",
		"teacher": "exam_task", "grader": "exam_task", "arbitrator": "exam_task",
		"student": "self", "auditor": "tenant", "page_processing_worker": "service",
	}
	for role, scope := range want {
		if got := RolePolicy(role).CanonicalScope; got != scope {
			t.Fatalf("%s scope=%s want %s", role, got, scope)
		}
	}
	if !RolePolicy("custom_worker").Service || RolePolicy("custom_worker").ManagedAssignable {
		t.Fatal("worker suffix must always be treated as a non-assignable service identity")
	}
}

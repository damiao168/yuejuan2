package auth

import (
	"context"
	"errors"
	"testing"
)

func TestManagedUserCreationUsesCanonicalScopes(t *testing.T) {
	store := NewMemoryStore()
	actor := User{ID: "tenant-admin", TenantID: "tenant-1", TenantCode: "tenant", Roles: []string{"tenant_admin"}}
	store.SetTenantStatus(actor.TenantID, "active")
	scope := AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: true}
	for _, role := range []AssignableRole{
		{Code: "teacher", ScopeType: "exam_task"}, {Code: "grader", ScopeType: "exam_task"}, {Code: "arbitrator", ScopeType: "exam_task"},
	} {
		store.AddRole(actor.TenantID, role)
	}
	tests := []struct {
		role, wantScope string
	}{
		{role: "teacher", wantScope: "exam_task"},
		{role: "grader", wantScope: "exam_task"},
		{role: "arbitrator", wantScope: "exam_task"},
	}
	for _, test := range tests {
		username := test.role + "-1"
		_, err := store.CreateManagedUser(context.Background(), actor, scope, CreateManagedUserInput{TeachingSubjects: []string{"mathematics"},
			Username: username, DisplayName: username, RoleCode: test.role, SchoolID: "school-1",
		}, "hash")
		if err != nil {
			t.Fatalf("create %s: %v", test.role, err)
		}
		created, err := store.FindUserByLogin(context.Background(), actor.TenantCode, username)
		if err != nil {
			t.Fatalf("find %s: %v", test.role, err)
		}
		roleScope, _ := created.DataScope[test.role].(map[string]any)
		if got := roleScope["scope"]; got != test.wantScope || got == "tenant" {
			t.Fatalf("%s scope=%v want %s", test.role, got, test.wantScope)
		}
	}
}

func TestManagedUserCreationRejectsPrivilegedStudentAndServiceRoles(t *testing.T) {
	store := NewMemoryStore()
	actor := User{ID: "school-admin", TenantID: "tenant-1", TenantCode: "tenant", Roles: []string{"school_admin"}}
	scope := AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, SchoolIDs: []string{"school-1"}}
	for _, role := range []AssignableRole{
		{Code: "platform_admin", ScopeType: "platform"}, {Code: "tenant_admin", ScopeType: "tenant"},
		{Code: "school_admin", ScopeType: "school"}, {Code: "student", ScopeType: "self"},
		{Code: "page_processing_worker", ScopeType: "service"},
	} {
		store.AddRole(actor.TenantID, role)
		_, err := store.CreateManagedUser(context.Background(), actor, scope, CreateManagedUserInput{TeachingSubjects: []string{"mathematics"},
			Username: role.Code, DisplayName: role.Code, RoleCode: role.Code, SchoolID: "school-1",
		}, "hash")
		if !errors.Is(err, ErrRoleAssignment) {
			t.Fatalf("%s expected role assignment rejection, got %v", role.Code, err)
		}
	}
}

func TestManagedUserCreationRequiresReviewTeachingSubjects(t *testing.T) {
	store := NewMemoryStore()
	actor := User{ID: "owner", TenantID: "tenant", Roles: []string{"tenant_admin"}}
	store.AddRole(actor.TenantID, AssignableRole{Code: "grader", ScopeType: "exam_task"})
	_, err := store.CreateManagedUser(context.Background(), actor, AccessScope{TenantWide: true}, CreateManagedUserInput{Username: "grader", DisplayName: "Grader", RoleCode: "grader", SchoolID: "school"}, "hash")
	if !errors.Is(err, ErrInvalidTeachingSubjects) {
		t.Fatalf("missing subjects error=%v", err)
	}
}

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func managedAccessFixture(t *testing.T) (*MemoryStore, User, AccessScope) {
	t.Helper()
	store := NewMemoryStore()
	actor := User{ID: "owner", TenantID: "tenant", TenantCode: "demo", Username: "owner", Status: "active", Roles: []string{"tenant_admin"}}
	store.AddUser(UserWithPassword{User: actor})
	for _, role := range []string{"teacher", "grader", "arbitrator", "school_admin"} {
		store.AddRole(actor.TenantID, AssignableRole{Code: role, ScopeType: RolePolicy(role).CanonicalScope})
	}
	return store, actor, AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: true}
}

func TestManagedAccessMultiRoleKeepsCanonicalTaskScopesAndTeachingSubjects(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	user, err := store.CreateManagedUser(context.Background(), actor, scope, CreateManagedUserInput{TeachingSubjects: []string{"mathematics"}, Username: "mixed", DisplayName: "Mixed", RoleCodes: []string{"teacher", "grader"}, SchoolID: "school-1"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.FindUserByLogin(context.Background(), actor.TenantCode, "mixed")
	if err != nil {
		t.Fatal(err)
	}
	for role, expected := range map[string]string{"teacher": "exam_task", "grader": "exam_task"} {
		if got := created.DataScope[role].(map[string]any)["scope"]; got != expected {
			t.Fatalf("%s scope=%v want %s", role, got, expected)
		}
	}
	encoded, err := json.Marshal(user)
	if err != nil || !strings.Contains(string(encoded), `"teaching_subjects":["mathematics"]`) || strings.Contains(string(encoded), `"class_ids"`) {
		t.Fatalf("teacher profile must expose subjects without class bindings: %s err=%v", encoded, err)
	}
}

func TestManagedAccessConflictAndCrossSchoolTargetDoNotMutateAuthorization(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	store.AddUser(UserWithPassword{User: User{ID: "teacher", TenantID: actor.TenantID, TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}}, SchoolID: "school-1", TeachingSubjects: []string{"mathematics"}})
	input := UpdateManagedUserAccessInput{RoleCodes: []string{"teacher", "grader"}, ExpectedRoles: []string{"teacher"}, Reason: "兼任阅卷"}
	bad := input
	bad.ExpectedRoles = []string{"grader"}
	if _, err := store.UpdateManagedUserAccess(context.Background(), actor, scope, "teacher", bad); !errors.Is(err, ErrManagedUserAccessConflict) {
		t.Fatalf("stale authorization should conflict: %v", err)
	}

	user, _ := store.FindUserByLogin(context.Background(), "demo", "teacher")
	if len(user.Roles) != 1 || user.Roles[0] != "teacher" || user.TeachingSubjects[0] != "mathematics" {
		t.Fatalf("failure changed authorization: %#v", user)
	}
	schoolActor := User{ID: "manager", TenantID: actor.TenantID, Roles: []string{"school_admin"}}
	if _, err := store.UpdateManagedUserAccess(context.Background(), schoolActor, AccessScope{TenantID: actor.TenantID, ActorID: schoolActor.ID, SchoolIDs: []string{"school-2"}}, "teacher", input); !errors.Is(err, ErrOrganizationScope) {
		t.Fatalf("cross-school target must fail: %v", err)
	}
}

func TestManagedAccessChangeRevokesCredentialsAndStaleLogin(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	ctx, now := context.Background(), time.Now()
	store.AddUser(UserWithPassword{User: User{ID: "teacher", TenantID: actor.TenantID, TenantCode: "demo", Username: "teacher", Status: "active", Roles: []string{"teacher"}}, SchoolID: "school-1", TeachingSubjects: []string{"mathematics"}})
	user, _ := store.FindUserByLogin(ctx, "demo", "teacher")
	if _, err := store.CreateSession(ctx, CreateSessionInput{TenantID: actor.TenantID, UserID: user.ID, TokenHash: "session", ExpiresAt: now.Add(time.Hour), SecurityEpoch: user.SecurityEpoch}); err != nil {
		t.Fatal(err)
	}
	store.devices["device"] = memoryDeviceBinding{TenantID: actor.TenantID, UserID: user.ID, ExpiresAt: now.Add(time.Hour)}
	if _, err := store.UpdateManagedUserAccess(ctx, actor, scope, user.ID, UpdateManagedUserAccessInput{RoleCodes: []string{"teacher", "grader"}, ExpectedRoles: []string{"teacher"}, Reason: "兼任阅卷"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindUserBySession(ctx, "session", now); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old session remains: %v", err)
	}
	if len(store.devices) != 0 {
		t.Fatal("old trusted device remains")
	}
	if _, err := store.CreateSession(ctx, CreateSessionInput{TenantID: actor.TenantID, UserID: user.ID, TokenHash: "late-session", ExpiresAt: now.Add(time.Hour), SecurityEpoch: user.SecurityEpoch}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale in-flight login should fail: %v", err)
	}
}

func TestConcurrentManagedAccessDemotionsKeepLastSchoolAdministrator(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	for _, id := range []string{"admin-a", "admin-b"} {
		store.AddUser(UserWithPassword{User: User{ID: id, TenantID: actor.TenantID, TenantCode: "demo", Username: id, Status: "active", Roles: []string{"school_admin"}}, SchoolID: "school-1", TeachingSubjects: []string{"mathematics"}})
	}
	// 两个管理员同时降级，单独看旧人数都为二；写锁内计数必须留下一个启用管理员。
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, id := range []string{"admin-a", "admin-b"} {
		group.Add(1)
		go func(id string) {
			defer group.Done()
			_, err := store.UpdateManagedUserAccess(context.Background(), actor, scope, id, UpdateManagedUserAccessInput{RoleCodes: []string{"teacher"}, ExpectedRoles: []string{"school_admin"}, Reason: "工作交接"})
			results <- err
		}(id)
	}
	group.Wait()
	close(results)
	succeeded, rejected := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrLastSchoolAdmin) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("success=%d rejected=%d", succeeded, rejected)
	}
}

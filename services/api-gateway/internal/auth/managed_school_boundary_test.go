package auth

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func mixedSchoolManagerFixture(t *testing.T) (*MemoryStore, User, AccessScope) {
	t.Helper()
	store := NewMemoryStore()
	actor := User{ID: "manager", TenantID: "tenant", TenantCode: "demo", Username: "manager", Status: "active", Roles: []string{"school_admin", "teacher"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school"},
		"teacher":      map[string]any{"scope": "class", "school_id": "school-b", "class_id": "class-b"},
	}}
	store.AddUser(UserWithPassword{User: actor, SchoolID: "school-a"})
	for _, code := range []string{"teacher", "grader"} {
		store.AddRole(actor.TenantID, AssignableRole{Code: code, ScopeType: RolePolicy(code).CanonicalScope})
	}
	scope, err := store.ResolveAccessScope(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	if scope.AllowsSchool("school-b") || scope.managesSchool("school-b") || !scope.managesSchool("school-a") {
		t.Fatalf("mixed school fixture lost its permission boundary: %#v", scope)
	}
	return store, actor, scope
}

func TestManagedUserMutationsRequireManagedSchoolForMixedRoleActor(t *testing.T) {
	for _, operation := range []string{"create", "activation", "recovery", "status", "profile", "access"} {
		for _, school := range []string{"school-a", "school-b"} {
			t.Run(operation+"/"+school, func(t *testing.T) {
				store, actor, scope := mixedSchoolManagerFixture(t)
				status := "active"
				if operation == "activation" {
					status = "invited"
				}
				store.AddUser(UserWithPassword{User: User{ID: "target", TenantID: actor.TenantID, TenantCode: actor.TenantCode, Username: "target", DisplayName: "Original", Status: status, Roles: []string{"teacher"}}, SchoolID: school, TeachingSubjects: []string{"mathematics"}, PasswordHash: "old-hash"})
				now := time.Now().UTC()
				store.activations["old-activation"] = memoryActivation{TenantID: actor.TenantID, UserID: "target", ExpiresAt: now.Add(time.Hour)}
				store.recoveries["old-recovery"] = memoryRecovery{TenantID: actor.TenantID, UserID: "target", ExpiresAt: now.Add(time.Hour), SecurityEpoch: 1}
				users, activations, recoveries := maps.Clone(store.users), maps.Clone(store.activations), maps.Clone(store.recoveries)
				err := runManagedSchoolMutation(t.Context(), store, actor, scope, operation, "target", school, "new-token")
				if school == "school-a" {
					if err != nil {
						t.Fatalf("managed school %s failed: %v", operation, err)
					}
					return
				}
				if !errors.Is(err, ErrOrganizationScope) {
					t.Fatalf("foreign teaching school %s error=%v", operation, err)
				}
				// 拒绝必须发生在写入或替换旧令牌之前，不能让越权请求使原邀请和恢复链接失效。
				if !reflect.DeepEqual(users, store.users) || !reflect.DeepEqual(activations, store.activations) || !reflect.DeepEqual(recoveries, store.recoveries) {
					t.Fatal("rejected management changed target or existing tokens")
				}
			})
		}
	}
}

func runManagedSchoolMutation(ctx context.Context, store Store, actor User, scope AccessScope, operation, target, school, token string) error {
	switch operation {
	case "create":
		_, err := store.CreateManagedUser(ctx, actor, scope, CreateManagedUserInput{TeachingSubjects: []string{"mathematics"}, Username: "new-user", DisplayName: "New user", RoleCode: "teacher", SchoolID: school}, "hash")
		return err
	case "activation":
		_, err := store.CreateActivation(ctx, actor, scope, target, token, time.Now().Add(time.Hour))
		return err
	case "recovery":
		_, err := store.CreateRecovery(ctx, actor, scope, target, token, time.Now().Add(time.Hour))
		return err
	case "status":
		_, _, err := store.UpdateManagedUserStatus(ctx, actor, scope, target, "disabled")
		return err
	case "profile":
		_, _, err := store.UpdateManagedUserProfile(ctx, actor, scope, target, UpdateManagedUserProfileInput{DisplayName: "Updated", ExpectedDisplayName: "Original"})
		return err
	case "access":
		_, err := store.UpdateManagedUserAccess(ctx, actor, scope, target, UpdateManagedUserAccessInput{RoleCodes: []string{"teacher", "grader"}, ExpectedRoles: []string{"teacher"}, Reason: "兼任阅卷"})
		return err
	default:
		return errors.New("unknown test mutation")
	}
}

func TestManagedUserCreationDefaultsToManagedSchoolWithForeignTeachingScope(t *testing.T) {
	store, actor, scope := mixedSchoolManagerFixture(t)
	created, err := store.CreateManagedUser(t.Context(), actor, scope, CreateManagedUserInput{TeachingSubjects: []string{"mathematics"}, Username: "teacher", DisplayName: "Teacher", RoleCode: "teacher"}, "hash")
	if err != nil || created.SchoolID != "school-a" {
		t.Fatalf("default school=%q error=%v", created.SchoolID, err)
	}
}

func TestManagedUserListUsesManagedSchoolsForMixedRoleActor(t *testing.T) {
	for _, tenantWide := range []bool{false, true} {
		t.Run(map[bool]string{false: "school", true: "tenant"}[tenantWide], func(t *testing.T) {
			store, actor, scope := mixedSchoolManagerFixture(t)
			for _, school := range []string{"school-a", "school-b"} {
				store.AddUser(UserWithPassword{User: User{ID: "target-" + school, TenantID: actor.TenantID, TenantCode: actor.TenantCode, Username: "target-" + school, Status: "active", Roles: []string{"teacher"}}, SchoolID: school})
			}
			scope.TenantWide = tenantWide
			req := httptest.NewRequest(http.MethodGet, "/api/v1/users?q=target", nil)
			req = req.WithContext(WithAccessScope(WithUser(req.Context(), actor), scope))
			rec := httptest.NewRecorder()
			NewHandler(store, time.Hour).ListManagedUsers(rec, req)
			var response struct {
				Users []ManagedUser `json:"users"`
			}
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
				t.Fatalf("list failed: status=%d body=%s", rec.Code, rec.Body.String())
			}
			wantCount := 1
			if tenantWide {
				wantCount = 2
			}
			if len(response.Users) != wantCount {
				t.Fatalf("listed %d users, want %d: %s", len(response.Users), wantCount, rec.Body.String())
			}
			if !tenantWide && response.Users[0].SchoolID != "school-a" {
				t.Fatalf("foreign teaching school leaked into account list: %+v", response.Users)
			}
		})
	}
}

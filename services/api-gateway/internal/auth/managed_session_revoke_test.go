package auth

import (
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestManagedSessionRevokeChecksSchoolRoleAndScopeBeforeChangingCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, targetSchool string
		targetRoles        []string
		tenantWide         bool
		want               int
	}{
		{"same school", "school-a", []string{"teacher", "grader"}, false, http.StatusOK},
		{"foreign school", "school-b", []string{"teacher"}, false, http.StatusForbidden},
		{"higher role", "school-a", []string{"teacher", "tenant_admin"}, false, http.StatusForbidden},
		{"peer admin", "school-a", []string{"school_admin"}, false, http.StatusForbidden},
		{"missing school", "", []string{"teacher"}, false, http.StatusForbidden},
		{"tenant admin foreign school", "school-b", []string{"teacher"}, true, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			role, rawScope := "school_admin", map[string]any{"scope": "school", "school_id": "school-a"}
			if tc.tenantWide {
				role, rawScope = "tenant_admin", map[string]any{"scope": "tenant"}
			}
			actor := User{ID: "manager", TenantID: "tenant", Roles: []string{role}, DataScope: map[string]any{role: rawScope}}
			// school-b 是导航投影，即使出现在普通 SchoolIDs 里也不应成为管理目标。
			scope, err := ResolveDeclaredAccessScope(actor)
			if err != nil {
				t.Fatal(err)
			}
			scope.SchoolIDs = append(scope.SchoolIDs, "school-b")
			target := UserWithPassword{User: User{ID: "target", TenantID: "tenant", Username: "target", Status: "active", Roles: tc.targetRoles}, SchoolID: tc.targetSchool}
			store.AddUser(target)
			_, err = store.CreateSession(t.Context(), CreateSessionInput{TenantID: "tenant", UserID: "target", TokenHash: "target-session", SessionType: SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			store.devices["target-device"] = memoryDeviceBinding{TenantID: "tenant", UserID: "target", ExpiresAt: time.Now().Add(time.Hour)}
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/target/sessions", nil)
			req.SetPathValue("id", "target")
			req = req.WithContext(WithAccessScope(WithUser(req.Context(), actor), scope))
			rec := httptest.NewRecorder()
			NewHandler(store, time.Hour).AdminRevokeUserSessions(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body)
			}
			sessions, err := store.ListSessions(t.Context(), "tenant", "target", "", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			_, deviceExists := store.devices["target-device"]
			wantPreserved := tc.want != http.StatusOK
			if (len(sessions) == 1) != wantPreserved || deviceExists != wantPreserved {
				t.Fatalf("credentials after request: sessions=%d device=%v", len(sessions), deviceExists)
			}
		})
	}
}

func TestManagedSessionRevokeMissingScopeAndSelfLeaveCredentialsIntact(t *testing.T) {
	store := NewMemoryStore()
	actor := User{ID: "actor", TenantID: "tenant", Username: "actor", Status: "active", Roles: []string{"tenant_admin"}}
	store.AddUser(UserWithPassword{User: actor})
	_, err := store.CreateSession(t.Context(), CreateSessionInput{TenantID: "tenant", UserID: "actor", TokenHash: "self", SessionType: SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []AccessScope{{}, {TenantID: "other", ActorID: actor.ID, TenantWide: true}, {TenantID: actor.TenantID, ActorID: "other", TenantWide: true}, {TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: true}} {
		if _, err := store.RevokeManagedUserSessions(t.Context(), actor, scope, actor.ID, "test"); err == nil {
			t.Fatal("invalid scope or self-revocation should fail")
		}
	}
	if revoked, err := store.RevokeAllSessions(t.Context(), actor.TenantID, actor.ID, "user_logout_all"); err != nil || revoked != 1 {
		t.Fatalf("self logout control: revoked=%d err=%v", revoked, err)
	}
}

func managedRevokeAccountStep(school string) authBatch49Step {
	return authBatch49Step{kind: "query", fragment: "FOR UPDATE", args: []any{"tenant", "target"}, columns: 2, rows: [][]driver.Value{{"target", school}}}
}

func TestPostgresManagedSessionRevocationChecksLockedAccountAndRollsBackDeviceFailure(t *testing.T) {
	actor := User{ID: "actor", TenantID: "tenant", Roles: []string{"school_admin"}}
	scope := AccessScope{TenantID: "tenant", ActorID: "actor", schoolWide: true, schoolManagementIDs: []string{"school-a"}, SchoolIDs: []string{"school-a", "school-b"}}
	failure := errors.New("device write failed")
	for _, tc := range []struct {
		name, school, roles string
		deviceFailure       bool
		want                error
	}{
		{"local commit", "school-a", "{teacher}", false, nil},
		{"foreign rejected", "school-b", "{teacher}", false, ErrOrganizationScope},
		{"higher role rejected", "school-a", "{teacher,tenant_admin}", false, ErrUserStatusForbidden},
		{"device failure rollback", "school-a", "{teacher}", true, failure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []authBatch49Step{managedRevokeAccountStep(tc.school)}
			roles := [][]driver.Value{{"teacher"}}
			if tc.roles == "{teacher,tenant_admin}" {
				roles = append(roles, []driver.Value{"tenant_admin"})
			}
			steps = append(steps, authBatch49RoleStep(roles))
			if tc.want == nil || tc.deviceFailure {
				var deviceErr error
				if tc.deviceFailure {
					deviceErr = failure
				}
				steps = append(steps,
					authBatch49Step{kind: "exec", fragment: "UPDATE auth_session", args: []any{"tenant", "target", "test"}},
					authBatch49Step{kind: "exec", fragment: "UPDATE auth_trusted_device", args: []any{"tenant", "target"}, err: deviceErr})
			}
			store, script := openAuthBatch49Store(t, steps)
			count, err := store.RevokeManagedUserSessions(t.Context(), actor, scope, "target", "test")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want=%v", err, tc.want)
			}
			end := "rollback"
			if tc.want == nil {
				end = "commit"
				if count != 1 {
					t.Fatalf("revoked=%d", count)
				}
			} else if count != 0 {
				t.Fatalf("failure returned success count=%d", count)
			}
			assertAuthBatch49Script(t, script, end)
		})
	}
}

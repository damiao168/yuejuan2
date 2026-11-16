package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestManagedProfilePhoneChangePreservesAuditPrivacyAndRevokesSession(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	now := time.Now().UTC()
	store.AddUser(UserWithPassword{
		User: User{ID: "teacher", TenantID: actor.TenantID, TenantCode: actor.TenantCode,
			Username: "teacher", DisplayName: "Original", Status: "active", Roles: []string{"teacher"}},
		SchoolID: "school-1", PhoneNormalized: "13800138000", TeachingSubjects: []string{"mathematics"},
	})
	if _, err := store.CreateSession(t.Context(), CreateSessionInput{
		TenantID: actor.TenantID, UserID: "teacher", TokenHash: "teacher-session",
		ExpiresAt: now.Add(time.Hour), SecurityEpoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, 0)
	const body = `{"display_name":"Updated","employee_no":"T-123","phone":"13900139000","expected_display_name":"Original","expected_employee_no":"","expected_phone_masked":"138****8000"}`
	path := "/api/v1/users/teacher/profile"
	response := managedAccountPatch(t, handler.UpdateManagedUserProfile, actor, &scope, path, "teacher", body)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("profile change returned %d, Cache-Control=%q", response.Code, response.Header().Get("Cache-Control"))
	}
	var result struct {
		User                 ManagedUser `json:"user"`
		LoginIdentityChanged bool        `json:"login_identity_changed"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.DisplayName != "Updated" || result.User.EmployeeNo != "T-123" || result.User.PhoneMasked != MaskPhone("13900139000") ||
		!slices.Equal(result.User.TeachingSubjects, []string{"mathematics"}) || !result.LoginIdentityChanged {
		t.Fatalf("profile changes were not reflected in the response: %+v", result)
	}
	if strings.Contains(response.Body.String(), "13900139000") {
		t.Fatal("profile response contains the unmasked phone")
	}
	if _, err := store.FindUserBySession(t.Context(), "teacher-session", now); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("previous session remains valid after changing login identifiers: %v", err)
	}

	// 原请求的资料版本已经过期，重试不能再更新资料或写出第二条成功审计。
	retry := managedAccountPatch(t, handler.UpdateManagedUserProfile, actor, &scope, path, "teacher", body)
	assertManagedAccountError(t, retry, http.StatusConflict, "managed_user_profile_conflict")
	events, err := store.ListAudits(t.Context(), actor.TenantID, AuditFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Action != "auth.user_profile_updated" || events[0].TargetID != "teacher" ||
		events[0].BeforeValue["phone_masked"] != MaskPhone("13800138000") || events[0].AfterValue["phone_masked"] != MaskPhone("13900139000") {
		t.Fatalf("expected one masked profile change audit, got %v", events)
	}
	audit, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit), "13800138000") || strings.Contains(string(audit), "13900139000") {
		t.Fatal("profile audit contains the unmasked phone")
	}
}

func TestManagedProfileRejectsInvalidOrConflictingUpdates(t *testing.T) {
	const goodBody = `{"display_name":"Updated","employee_no":"","expected_display_name":"Original","expected_employee_no":"","expected_phone_masked":""}`
	for _, tc := range []struct {
		name, body, code string
		status           int
		missingScope     bool
	}{
		{"unknown field", `{"unexpected":"field"}`, "invalid_request", http.StatusBadRequest, false},
		{"missing expected phone", `{"display_name":"Updated","employee_no":"","expected_display_name":"Original","expected_employee_no":""}`, "invalid_request", http.StatusBadRequest, false},
		{"empty display name", strings.Replace(goodBody, "Updated", " ", 1), "invalid_request", http.StatusBadRequest, false},
		{"empty phone", strings.Replace(goodBody, `{"display_name"`, `{"phone":"","display_name"`, 1), "invalid_phone", http.StatusBadRequest, false},
		{"invalid phone", strings.Replace(goodBody, `{"display_name"`, `{"phone":"abc","display_name"`, 1), "invalid_phone", http.StatusBadRequest, false},
		{"duplicate employee number", strings.Replace(goodBody, `"employee_no":""`, `"employee_no":"T-123"`, 1), "identity_exists", http.StatusConflict, false},
		{"missing access scope", goodBody, "access_scope_missing", http.StatusForbidden, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, actor, scope := managedAccessFixture(t)
			for _, user := range []struct{ id, employeeNo string }{{"teacher", ""}, {"other", "T-123"}} {
				store.AddUser(UserWithPassword{
					User: User{ID: user.id, TenantID: actor.TenantID, TenantCode: actor.TenantCode,
						Username: user.id, DisplayName: "Original", Status: "active", Roles: []string{"teacher"}},
					SchoolID: "school-1", EmployeeNo: user.employeeNo, TeachingSubjects: []string{"mathematics"},
				})
			}
			accessScope := &scope
			if tc.missingScope {
				accessScope = nil
			}
			handler := NewHandler(store, 0)
			response := managedAccountPatch(t, handler.UpdateManagedUserProfile, actor, accessScope, "/api/v1/users/teacher/profile", "teacher", tc.body)
			assertManagedAccountError(t, response, tc.status, tc.code)
			user, err := store.FindUserByLogin(t.Context(), actor.TenantCode, "teacher")
			if err != nil || user.DisplayName != "Original" || user.EmployeeNo != "" || len(store.Audits()) != 0 {
				t.Fatalf("rejected update changed the original profile or audit: user=%+v err=%v", user.User, err)
			}
		})
	}
}

func TestManagedAccessRequiresCurrentRolesAndAuditsSuccessfulChange(t *testing.T) {
	store, actor, scope := managedAccessFixture(t)
	store.AddUser(UserWithPassword{
		User: User{ID: "teacher", TenantID: actor.TenantID, TenantCode: actor.TenantCode,
			Username: "teacher", DisplayName: "Teacher", Status: "active", Roles: []string{"teacher"}},
		SchoolID: "school-1", TeachingSubjects: []string{"mathematics"},
	})
	handler := NewHandler(store, 0)
	const path = "/api/v1/users/teacher/access"
	const body = `{"role_codes":["teacher","grader","teacher"],"expected_roles":["teacher"],"reason":"  阅卷工作交接  "}`
	response := managedAccountPatch(t, handler.UpdateManagedUserAccess, actor, &scope, path, "teacher", body)
	if response.Code != http.StatusOK {
		t.Fatalf("access update returned %d: %s", response.Code, response.Body)
	}
	var result struct {
		User ManagedUser `json:"user"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.User.ID != "teacher" || !slices.Equal(result.User.Roles, []string{"grader", "teacher"}) {
		t.Fatalf("access update returned unexpected roles: %+v", result.User)
	}
	retry := managedAccountPatch(t, handler.UpdateManagedUserAccess, actor, &scope, path, "teacher", body)
	assertManagedAccountError(t, retry, http.StatusConflict, "managed_user_access_conflict")
	events := store.Audits()
	if len(events) != 1 || events[0].Action != "auth.user_access_updated" ||
		events[0].ActorID != actor.ID || events[0].TargetID != "teacher" || events[0].Reason != "阅卷工作交接" {
		t.Fatalf("expected one access change audit with actor, target and reason, got %v", events)
	}
}

func managedAccountPatch(t *testing.T, handler http.HandlerFunc, actor User, scope *AccessScope, path, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := WithUser(t.Context(), actor)
	if scope != nil {
		ctx = WithAccessScope(ctx, *scope)
	}
	request := httptest.NewRequestWithContext(ctx, http.MethodPatch, path, strings.NewReader(body))
	request.SetPathValue("id", target)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertManagedAccountError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != status || payload.Error.Code != code {
		t.Fatalf("expected %d/%s, received %d %s: %v", status, code, response.Code, response.Body, err)
	}
}

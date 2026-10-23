package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestUsernameOnlyInvitationRequiresActivationAndKeepsRoleScopes(t *testing.T) {
	for _, roles := range [][]string{{"arbitrator", "grader"}, {"school_admin"}} {
		t.Run(strings.Join(roles, "+"), func(t *testing.T) {
			store := newOrganizationAdminStore(t)
			router := newTestRouter(store)
			adminToken := login(t, router, "demo", "admin", "AdminStart123!")
			body, err := json.Marshal(map[string]any{
				"username": " username_teacher ", "display_name": "张老师", "school_id": "school-1", "role_codes": roles,
			})
			if err != nil {
				t.Fatal(err)
			}
			create := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(string(body)))
				req.Header.Set("Authorization", "Bearer "+adminToken)
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			createdResponse := create()
			if createdResponse.Code != http.StatusCreated {
				t.Fatalf("username invitation expected 201, got %d: %s", createdResponse.Code, createdResponse.Body.String())
			}
			var created struct {
				User       auth.ManagedUser `json:"user"`
				Activation struct {
					Token string `json:"token"`
				} `json:"activation"`
			}
			if err := json.NewDecoder(createdResponse.Body).Decode(&created); err != nil {
				t.Fatal(err)
			}
			if created.User.Username != "username_teacher" || created.User.Status != "invited" || created.Activation.Token == "" ||
				created.User.PhoneMasked != "" || created.User.EmployeeNo != "" || created.User.SchoolID != "school-1" ||
				len(created.User.ClassIDs) != 0 || !slices.Equal(created.User.Roles, roles) {
				t.Fatal("username-only invitation must retain its identity, school and roles without adding phone, employee number or classes")
			}
			if duplicate := create(); duplicate.Code != http.StatusConflict || !strings.Contains(duplicate.Body.String(), `"code":"username_exists"`) {
				t.Fatalf("duplicate username expected 409, got %d: %s", duplicate.Code, duplicate.Body.String())
			}
			password := "Username invitation passphrase 2026!"
			if pending := loginResponseRaw(router, "demo", created.User.Username, password); pending.Code != http.StatusUnauthorized {
				t.Fatalf("invited username must not log in before activation, got %d", pending.Code)
			}
			completeBody := `{"token":"` + created.Activation.Token + `","password":"` + password + `"}`
			for attempt, want := range []int{http.StatusOK, http.StatusBadRequest} {
				req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/activation/complete", strings.NewReader(completeBody))
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				if rec.Code != want {
					t.Fatalf("activation attempt %d expected %d, got %d", attempt+1, want, rec.Code)
				}
			}
			_ = login(t, router, "demo", created.User.Username, password)
			stored, err := store.FindUserByLogin(context.Background(), "demo", created.User.Username)
			if err != nil || stored.Status != "active" || stored.ActivatedAt.IsZero() {
				t.Fatal("completed invitation must activate the same username account")
			}
			for _, role := range roles {
				if scope, ok := stored.DataScope[role].(map[string]any); !ok || scope["scope"] != auth.RolePolicy(role).CanonicalScope {
					t.Fatalf("username invitation changed canonical scope for %s", role)
				}
			}
			if slices.Contains(roles, "grader") {
				scope, err := store.ResolveAccessScope(context.Background(), stored.User)
				if err != nil || !scope.AssignedOnly || scope.TenantWide || scope.IsPlatform || scope.IsSchoolWide() ||
					len(scope.SchoolIDs)+len(scope.ClassIDs)+len(scope.ExamIDs)+len(scope.ReviewTaskIDs)+len(scope.ArbitrationTaskIDs) != 0 {
					t.Fatalf("unassigned grading account must retain an empty task boundary: scope=%#v err=%v", scope, err)
				}
			}
		})
	}
}

func TestInvitationIdentityValidationKeepsLegacyGeneratedUsernames(t *testing.T) {
	store := newOrganizationAdminStore(t)
	router := newTestRouter(store)
	adminToken := login(t, router, "demo", "admin", "AdminStart123!")
	for _, test := range []struct {
		name, identity string
		want           int
	}{
		{"missing identity", `"username":"   "`, http.StatusBadRequest},
		{"oversized username", `"username":"` + strings.Repeat("u", 257) + `"`, http.StatusBadRequest},
		{"phone only", `"phone":"13800138000"`, http.StatusCreated},
		{"employee number only", `"employee_no":"T-USERNAME-1"`, http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{` + test.identity + `,"display_name":"张老师","school_id":"school-1","role_codes":["grader","arbitrator"]}`
			req := httptest.NewRequest(http.MethodPost, "/api/v1/users", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+adminToken)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("identity validation expected %d, got %d: %s", test.want, rec.Code, rec.Body.String())
			}
			if test.want == http.StatusCreated {
				var created struct {
					User auth.ManagedUser `json:"user"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(created.User.Username, "member_") || created.User.Status != "invited" {
					t.Fatal("legacy phone and employee invitations must keep generated usernames and require activation")
				}
			}
		})
	}
}

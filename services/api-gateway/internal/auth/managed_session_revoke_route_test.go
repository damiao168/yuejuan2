package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAuthenticatedSchoolAdminCannotRevokeForeignOrHigherUserSessions(t *testing.T) {
	for _, tc := range []struct {
		name, school string
		roles        []string
		want         int
	}{
		{"own teacher", "school-a", []string{"teacher"}, http.StatusOK},
		{"foreign teacher", "school-b", []string{"teacher"}, http.StatusForbidden},
		{"tenant administrator", "school-a", []string{"tenant_admin"}, http.StatusForbidden},
		{"teacher with privileged second role", "school-a", []string{"teacher", "tenant_admin"}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := auth.NewMemoryStore()
			actor := auth.User{ID: "admin-a", TenantID: "tenant", TenantCode: "demo", Username: "admin-a", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"session:revoke"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
			target := auth.User{ID: "target", TenantID: "tenant", TenantCode: "demo", Username: "target", Status: "active", Roles: tc.roles, DataScope: map[string]any{"scope": "tenant"}}
			store.AddUser(auth.UserWithPassword{User: actor, SchoolID: "school-a"})
			store.AddUser(auth.UserWithPassword{User: target, SchoolID: tc.school})
			for token, id := range map[string]string{"actor-token": actor.ID, "target-token": target.ID} {
				if _, err := store.CreateSession(context.Background(), auth.CreateSessionInput{TenantID: "tenant", UserID: id, TokenHash: auth.HashToken(token), SessionType: auth.SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour), SecurityEpoch: 1}); err != nil {
					t.Fatal(err)
				}
			}
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/target/sessions", nil)
			req.Header.Set("Authorization", "Bearer actor-token")
			rec := httptest.NewRecorder()
			newTestRouter(store).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body)
			}
			sessions, err := store.ListSessions(t.Context(), "tenant", target.ID, "", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			wantRemaining := 1
			if tc.want == http.StatusOK {
				wantRemaining = 0
			}
			if len(sessions) != wantRemaining {
				t.Fatalf("remaining sessions=%d want=%d", len(sessions), wantRemaining)
			}
		})
	}
}

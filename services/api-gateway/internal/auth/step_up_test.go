package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequireRecentAuthAllowsFreshAndRejectsStaleSessions(t *testing.T) {
	tests := []struct {
		name       string
		user       User
		statusCode int
	}{
		{name: "fresh", user: userWithReauthentication(time.Now().UTC().Add(-time.Minute), 1), statusCode: http.StatusNoContent},
		{name: "stale", user: userWithReauthentication(time.Now().UTC().Add(-61*time.Minute), 1), statusCode: http.StatusPreconditionRequired},
		{name: "missing", user: User{ID: "user-1", CurrentAuthLevel: 1}, statusCode: http.StatusPreconditionRequired},
		{name: "future", user: userWithReauthentication(time.Now().UTC().Add(3*time.Minute), 1), statusCode: http.StatusPreconditionRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			handler := RequireRecentAuth(10 * time.Minute)(next)
			req := httptest.NewRequest(http.MethodPost, "/sensitive", nil)
			req = req.WithContext(WithUser(req.Context(), test.user))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != test.statusCode {
				t.Fatalf("status=%d want %d: %s", rec.Code, test.statusCode, rec.Body.String())
			}
			if test.statusCode == http.StatusPreconditionRequired && !strings.Contains(rec.Body.String(), `"code":"recent_auth_required"`) {
				t.Fatalf("stale session must return the stable step-up code: %s", rec.Body.String())
			}
		})
	}
}

func TestRequireAuthLevelRejectsPasswordOnlySession(t *testing.T) {
	handler := RequireAuthLevel(2)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/administrator-action", nil)
	req = req.WithContext(WithUser(req.Context(), userWithReauthentication(time.Now().UTC(), 1)))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `"code":"auth_level_required"`) {
		t.Fatalf("password-only session must not satisfy level 2: %d %s", rec.Code, rec.Body.String())
	}
}

func userWithReauthentication(at time.Time, level int) User {
	return User{ID: "user-1", CurrentAuthLevel: level, ReauthenticatedAt: &at}
}

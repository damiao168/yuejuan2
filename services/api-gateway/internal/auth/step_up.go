package auth

import (
	"net/http"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

const DefaultRecentAuthTTL = time.Hour

// RequireRecentAuth protects a sensitive handler after the ordinary session,
// tenant, resource and permission checks have succeeded. A password login and
// an explicit reauthentication both refresh the trusted session timestamp.
func RequireRecentAuth(maxAge time.Duration) func(http.Handler) http.Handler {
	if maxAge <= 0 {
		maxAge = DefaultRecentAuthTTL
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			user, ok := UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			now := time.Now().UTC()
			if user.CurrentAuthLevel < 1 || user.ReauthenticatedAt == nil || user.ReauthenticatedAt.After(now.Add(2*time.Minute)) || now.Sub(*user.ReauthenticatedAt) > maxAge {
				httpx.Error(w, r, http.StatusPreconditionRequired, "recent_auth_required", "recent authentication is required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAuthLevel is the server-side boundary for future MFA/WebAuthn and
// enterprise IdP sessions. Password-only sessions are level 1 today.
func RequireAuthLevel(level int) func(http.Handler) http.Handler {
	if level < 1 {
		level = 1
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			if user.CurrentAuthLevel < level {
				w.Header().Set("Cache-Control", "no-store")
				httpx.Error(w, r, http.StatusForbidden, "auth_level_required", "a stronger authentication method is required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

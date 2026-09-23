package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func AuthMiddleware(store Store, options ...HandlerOptions) func(http.Handler) http.Handler {
	return authMiddleware(store, false, false, options...)
}

// AccountAuthMiddleware is only for self-account handlers, which always bind
// queries to the authenticated tenant and user. An unassigned teacher must be
// able to change a password or revoke sessions without gaining business access.
func AccountAuthMiddleware(store Store, options ...HandlerOptions) func(http.Handler) http.Handler {
	return authMiddleware(store, true, false, options...)
}

// ReauthenticationAuthMiddleware admits a locked session only to the small
// recovery surface (reauthenticate/logout). Business handlers always use the
// normal middleware and therefore reject the locked session server-side.
func ReauthenticationAuthMiddleware(store Store, options ...HandlerOptions) func(http.Handler) http.Handler {
	return authMiddleware(store, true, true, options...)
}

func authMiddleware(store Store, allowMissingDataScope bool, allowLocked bool, options ...HandlerOptions) func(http.Handler) http.Handler {
	cookieName := DefaultSessionCookieName
	if len(options) > 0 && strings.TrimSpace(options[0].CookieName) != "" {
		cookieName = strings.TrimSpace(options[0].CookieName)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := sessionToken(r, cookieName)
			if token == "" {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			var user User
			var err error
			if allowLocked {
				user, err = store.FindUserBySessionForReauthentication(r.Context(), HashToken(token), time.Now().UTC())
			} else {
				user, err = store.FindUserBySession(r.Context(), HashToken(token), time.Now().UTC())
			}
			if err != nil {
				if errors.Is(err, ErrUnauthenticated) {
					httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
					return
				}
				httpx.Error(w, r, http.StatusInternalServerError, "auth_lookup_failed", "failed to authenticate request")
				return
			}
			scope, err := store.ResolveAccessScope(r.Context(), user)
			if err != nil {
				if errors.Is(err, ErrAccessScopeMissing) || errors.Is(err, ErrAccessScopeInvalid) {
					if !allowMissingDataScope {
						httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
						return
					}
					// Discard any partially resolved scope. This is an explicit
					// empty boundary, never a tenant-wide fallback.
					scope = AccessScope{TenantID: user.TenantID, ActorID: user.ID}
				} else {
					httpx.Error(w, r, http.StatusInternalServerError, "access_scope_lookup_failed", "failed to resolve data access scope")
					return
				}
			}
			// Business routes fail closed for humans without any resolved data
			// boundary. Self-account routes alone accept an empty boundary;
			// service identities retain their explicit worker-only scope.
			if !allowMissingDataScope && !scope.HasDataAccess() && !scope.syntheticUnbounded && !IsServiceUser(user) {
				httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
				return
			}
			organizationScope := scope.OrganizationScope()
			user.OrganizationScope = &organizationScope
			ctx := WithAccessScope(WithUser(r.Context(), user), scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func IsServiceUser(user User) bool {
	for _, role := range user.Roles {
		if role == "page_processing_worker" || strings.HasSuffix(role, "_worker") {
			return true
		}
	}
	return false
}

func sessionToken(r *http.Request, cookieName string) string {
	if token := bearerToken(r); token != "" {
		return token
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			if HasPermission(user, permission) {
				next.ServeHTTP(w, r)
				return
			}
			httpx.Error(w, r, http.StatusForbidden, "forbidden", "missing permission: "+permission)
		})
	}
}

func RequireAnyPermission(permissions ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			for _, permission := range permissions {
				if HasPermission(user, permission) {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.Error(w, r, http.StatusForbidden, "forbidden", "missing required permission")
		})
	}
}

func RequireAnyRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFromContext(r.Context())
			if !ok {
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
				return
			}
			for _, role := range roles {
				if HasRole(user, role) {
					next.ServeHTTP(w, r)
					return
				}
			}
			httpx.Error(w, r, http.StatusForbidden, "forbidden", "missing required role")
		})
	}
}

// RequirePlatformAdmin rejects role strings that are not anchored to the
// reserved platform tenant. This keeps cross-tenant administration from
// depending solely on assignment-time validation.
func RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
			return
		}
		if user.TenantID != PlatformTenantID || !HasRole(user, "platform_admin") {
			httpx.Error(w, r, http.StatusForbidden, "forbidden", "platform administrator required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func HasPermission(user User, permission string) bool {
	for _, current := range user.Permissions {
		if current == permission {
			return true
		}
	}
	return false
}

func HasRole(user User, role string) bool {
	for _, current := range user.Roles {
		if current == role {
			return true
		}
	}
	return false
}

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

// AccountAuthMiddleware 只用于账户自身处理器，查询始终绑定已认证租户和用户。尚未分配任务的教师应能修改密码或撤销会话，而不因此获得业务访问权。
func AccountAuthMiddleware(store Store, options ...HandlerOptions) func(http.Handler) http.Handler {
	return authMiddleware(store, true, false, options...)
}

// ReauthenticationAuthMiddleware 仅允许锁定会话访问重新认证和退出的有限恢复入口。业务处理器始终使用常规中间件，在服务端拒绝锁定会话。
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
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
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
					httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
					return
				}
				httpx.Error(w, r, http.StatusInternalServerError, "auth_lookup_failed", "failed to authenticate request")
				return
			}
			scope, allowed := resolveRequestAccessScope(store, w, r, user, allowMissingDataScope)
			if !allowed {
				return
			}
			organizationScope := scope.OrganizationScope()
			user.OrganizationScope = &organizationScope
			ctx := WithAccessScope(WithUser(r.Context(), user), scope)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func resolveRequestAccessScope(store Store, w http.ResponseWriter, r *http.Request, user User, allowMissingDataScope bool) (AccessScope, bool) {
	scope, err := store.ResolveAccessScope(r.Context(), user)
	if err != nil {
		if errors.Is(err, ErrAccessScopeMissing) || errors.Is(err, ErrAccessScopeInvalid) {
			if !allowMissingDataScope {
				httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
				return AccessScope{}, false
			}
			// 自账户接口可接受空作用域；解析失败的部分结果必须丢弃，不能扩大访问范围。
			scope = AccessScope{TenantID: user.TenantID, ActorID: user.ID}
		} else {
			httpx.Error(w, r, http.StatusInternalServerError, "access_scope_lookup_failed", "failed to resolve data access scope")
			return AccessScope{}, false
		}
	}
	// 普通业务接口拒绝没有数据范围的人类账户；服务身份仍由工作器专用接口限制。
	if !allowMissingDataScope && !scope.HasDataAccess() && !scope.syntheticUnbounded && !IsServiceUser(user) {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return AccessScope{}, false
	}
	return scope, true
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
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
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
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
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
				httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
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

// RequirePlatformAdmin 拒绝未绑定保留平台租户的角色字符串，使跨租户管理不只依赖分配角色时的校验。
func RequirePlatformAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", authRequiredMessage)
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

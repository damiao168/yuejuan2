package auth

import (
	"context"

	database "edugrade-enterprise/services/api-gateway/internal/db"
)

type contextKey string

const userContextKey contextKey = "auth_user"

// WithUser 同时绑定业务身份和数据库租户；两者必须来自同一次认证，避免权限与 RLS 使用不同租户。
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(database.WithTenant(ctx, user.TenantID), userContextKey, user)
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}

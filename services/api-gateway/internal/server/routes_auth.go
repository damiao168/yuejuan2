package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"net/http"
)

// 登录和恢复接口按状态选择不同 guard；凭据恢复、权限变更等操作要求近期密码认证。
func registerAuthRoutes(mux *http.ServeMux, ctx routerContext) {
	mux.Handle("POST /api/v1/auth/login", http.HandlerFunc(ctx.modules.Identity.AuthHandler.Login))
	mux.Handle("POST /api/v1/auth/token", http.HandlerFunc(ctx.modules.Identity.AuthHandler.TokenLogin))
	mux.Handle("POST /api/v1/auth/wechat/challenges", http.HandlerFunc(ctx.modules.Identity.AuthHandler.StartWechatLogin))
	mux.Handle("GET /api/v1/auth/wechat/callback", http.HandlerFunc(ctx.modules.Identity.AuthHandler.WechatCallback))
	mux.Handle("POST /api/v1/auth/wechat/session", http.HandlerFunc(ctx.modules.Identity.AuthHandler.PollWechatLogin))
	mux.Handle("POST /api/v1/auth/activation/verify", http.HandlerFunc(ctx.modules.Identity.AuthHandler.VerifyActivation))
	mux.Handle("POST /api/v1/auth/activation/complete", http.HandlerFunc(ctx.modules.Identity.AuthHandler.CompleteActivation))
	mux.Handle("POST /api/v1/auth/recovery/verify", http.HandlerFunc(ctx.modules.Identity.AuthHandler.VerifyRecovery))
	mux.Handle("POST /api/v1/auth/recovery/complete", http.HandlerFunc(ctx.modules.Identity.AuthHandler.CompleteRecovery))
	mux.Handle("POST /api/v1/auth/logout", ctx.guards.requireLockedAccount(ctx.modules.Identity.AuthHandler.Logout))
	mux.Handle("POST /api/v1/auth/password", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.ChangePassword))
	mux.Handle("POST /api/v1/auth/lock", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.LockSession))
	mux.Handle("POST /api/v1/auth/reauthenticate", ctx.guards.requireLockedAccount(ctx.modules.Identity.AuthHandler.Reauthenticate))
	mux.Handle("GET /api/v1/auth/me", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.Me))
	mux.Handle("GET /api/v1/auth/profile", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.AccountProfile))
	mux.Handle("GET /api/v1/auth/sessions", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.ListSessions))
	mux.Handle("GET /api/v1/auth/security-events", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.ListSecurityEvents))
	mux.Handle("DELETE /api/v1/auth/sessions/{id}", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.RevokeSession))
	mux.Handle("POST /api/v1/auth/logout-all", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.LogoutAll))
	mux.Handle("DELETE /api/v1/users/{id}/sessions", ctx.guards.requireRecentPermission("session:revoke", ctx.modules.Identity.AuthHandler.AdminRevokeUserSessions))
	dashboard.RegisterRoutes(mux, ctx.modules.Exam.DashboardHandler, ctx.guards.requireDashboardRead)
	mux.Handle("GET /api/v1/ai-grading/status", ctx.guards.requireAuth(auth.RequireAnyPermission("grading:manage", "review:work", "review:manage", "model:read", "system:read")(http.HandlerFunc(ctx.modules.Grading.SubjectiveHandler.Availability))))
	mux.Handle("GET /api/v1/users", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.ListManagedUsers))
	mux.Handle("POST /api/v1/users", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.CreateManagedUser))
	mux.Handle("POST /api/v1/users/{id}/activation", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.AdminCreateActivation))
	mux.Handle("PATCH /api/v1/users/{id}/status", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.UpdateManagedUserStatus))
	mux.Handle("PATCH /api/v1/users/{id}/access", ctx.guards.requireRecentPermission("org:manage", ctx.modules.Identity.AuthHandler.UpdateManagedUserAccess))
	// 人员资料维护不重复验证密码；当前会话、管理权限和目标学校范围仍逐次核对。
	mux.Handle("PATCH /api/v1/users/{id}/profile", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.UpdateManagedUserProfile))
	mux.Handle("POST /api/v1/users/{id}/credential-reset", ctx.guards.requireRecentPermission("org:manage", ctx.modules.Identity.AuthHandler.AdminCreateRecovery))
	mux.Handle("GET /api/v1/roles", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.ListAssignableRoles))
	mux.Handle("GET /api/v1/audit-logs", ctx.guards.requireAuditRead(ctx.modules.Identity.AuthHandler.ListAudits))
	mux.Handle("POST /api/v1/audit-logs/export", ctx.guards.requirePermission("audit:export", ctx.modules.Identity.AuthHandler.ExportAudits))
}

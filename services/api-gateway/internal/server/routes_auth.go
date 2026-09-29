package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"net/http"
)

// 登录、MFA 和恢复接口按状态选择不同 guard：一次性凭据不走通用幂等收据，管理操作则要求近期认证。
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
	mux.Handle("GET /api/v1/auth/mfa", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.MFAStatus))
	mux.Handle("POST /api/v1/auth/mfa/totp/enroll", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.EnrollTOTP))
	mux.Handle("POST /api/v1/auth/mfa/totp/confirm", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.ConfirmTOTP))
	mux.Handle("POST /api/v1/auth/step-up/start", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.StartMFAChallenge))
	mux.Handle("POST /api/v1/auth/step-up/verify", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.VerifyMFAChallenge))
	mux.Handle("POST /api/v1/auth/mfa/totp/disable", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.DisableTOTP))
	mux.Handle("POST /api/v1/auth/mfa/recovery-codes/rotate", ctx.guards.requireMFAAccount(ctx.modules.Identity.AuthHandler.RotateMFARecoveryCodes))
	mux.Handle("GET /api/v1/auth/me", ctx.guards.requireAccount(ctx.modules.Identity.AuthHandler.Me))
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
	mux.Handle("POST /api/v1/users/{id}/credential-reset", ctx.guards.requireRecentPermission("org:manage", ctx.modules.Identity.AuthHandler.AdminCreateRecovery))
	mux.Handle("GET /api/v1/roles", ctx.guards.requireOrgManage(ctx.modules.Identity.AuthHandler.ListAssignableRoles))
	mux.Handle("GET /api/v1/audit-logs", ctx.guards.requireAuditRead(ctx.modules.Identity.AuthHandler.ListAudits))
	mux.Handle("POST /api/v1/audit-logs/export", ctx.guards.requirePermission("audit:export", ctx.modules.Identity.AuthHandler.ExportAudits))
}

package auth

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r, h.cookieName)
	if token == "" {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	user, _ := UserFromContext(r.Context())
	if err := h.store.DeleteSession(r.Context(), HashToken(token), "user_logout"); err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "logout_failed", "failed to logout")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID:   user.TenantID,
		ActorID:    user.ID,
		Action:     "auth.logout",
		TargetType: "user",
		TargetID:   user.ID,
		IPAddress:  h.remoteIP(r),
		UserAgent:  r.UserAgent(),
		RequestID:  logger.RequestID(r.Context()),
	})
	http.SetCookie(w, h.clearSessionCookie())
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "logged_out"})
}

func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := UserFromContext(r.Context())
	if !ok || IsServiceUser(user) {
		httpx.Error(w, r, http.StatusForbidden, "password_change_forbidden", "password change is only available to signed-in users")
		return
	}
	var input changePasswordRequest
	if err := decodeAuthJSON(w, r, &input, false); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if input.CurrentPassword == "" || input.NewPassword == "" || len(input.CurrentPassword) > maxPasswordBytes || len(input.NewPassword) > maxPasswordBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "current_password and new_password are required")
		return
	}
	if !StrongPassword(input.NewPassword) {
		httpx.Error(w, r, http.StatusBadRequest, "weak_password", "new password must be a long passphrase of at least 15 characters")
		return
	}
	currentPasswordHash, err := h.store.FindPasswordHash(r.Context(), user.TenantID, user.ID)
	if err != nil || !CheckPassword(currentPasswordHash, input.CurrentPassword) {
		httpx.Error(w, r, http.StatusUnauthorized, "current_password_invalid", "current password is incorrect")
		return
	}
	if CheckPassword(currentPasswordHash, input.NewPassword) {
		httpx.Error(w, r, http.StatusBadRequest, "password_unchanged", "new password must differ from the current password")
		return
	}
	newHash, err := HashPassword(input.NewPassword)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "password_hash_failed", "failed to update password")
		return
	}
	// 传入刚验证的旧哈希供存储层比较，避免覆盖验证期间发生的另一次改密。
	updated, revoked, err := h.store.UpdatePasswordAndRevokeSessions(r.Context(), user.TenantID, user.ID, currentPasswordHash, newHash)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "password_change_failed", "failed to update password")
		return
	}
	if !updated {
		httpx.Error(w, r, http.StatusConflict, "password_changed_concurrently", "password changed in another session; sign in again")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.password_changed",
		TargetType: "user", TargetID: user.ID, Reason: "user changed password and revoked all sessions",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	http.SetCookie(w, h.clearSessionCookie())
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "password_changed", "revoked_count": revoked})
}

func (h *Handler) Reauthenticate(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now().UTC()
	w.Header().Set("Cache-Control", "no-store")
	user, ok := UserFromContext(r.Context())
	if !ok || IsServiceUser(user) {
		httpx.Error(w, r, http.StatusForbidden, "reauthentication_forbidden", "reauthentication is only available to signed-in users")
		return
	}
	var input reauthenticateRequest
	if err := decodeAuthJSON(w, r, &input, false); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	if input.Password == "" || len(input.Password) > maxPasswordBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "password is required")
		return
	}
	clientIP := h.remoteIP(r)
	attempt := LoginAttempt{TenantCode: user.TenantCode, Identifier: user.Username, AccountID: user.ID, IPAddress: clientIP}
	limit, blocked := h.loginGuard.Check(r.Context(), attempt, time.Now().UTC())
	if !h.loginLimiterAvailable(w, r) {
		return
	}
	if blocked {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(limit.RetryAfter.Seconds()))))
		httpx.Error(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many failed authentication attempts; retry later")
		return
	}
	passwordHash, err := h.store.FindPasswordHash(r.Context(), user.TenantID, user.ID)
	if err != nil && !errors.Is(err, ErrInvalidCredentials) {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	passwordValid := err == nil && CheckPassword(passwordHash, input.Password)
	if !passwordValid {
		limit, blocked := h.loginGuard.RegisterFailure(r.Context(), attempt, time.Now().UTC())
		RecordAudit(r.Context(), h.store, AuditEvent{
			TenantID: user.TenantID, ActorID: user.ID, Action: "auth.reauthentication_failed",
			TargetType: "user", TargetID: user.ID, Reason: "invalid current credential",
			IPAddress: clientIP, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
		})
		if !h.loginLimiterAvailable(w, r) {
			return
		}
		if blocked {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(limit.RetryAfter.Seconds()))))
			httpx.Error(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many failed authentication attempts; retry later")
			return
		}
		httpx.Error(w, r, http.StatusUnauthorized, "reauthentication_failed", "verification information is incorrect")
		return
	}
	token := sessionToken(r, h.cookieName)
	now := time.Now().UTC()
	updated, err := h.store.MarkSessionReauthenticated(r.Context(), user.TenantID, user.ID, HashToken(token), startedAt, now)
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	if !updated {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	h.loginGuard.RegisterSuccess(r.Context(), attempt)
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.session_reauthenticated",
		TargetType: "user", TargetID: user.ID, AfterValue: map[string]any{
			"auth_method": "password", "risk_level": normalizeRiskLevel(user.CurrentRiskLevel),
			"risk_action": normalizeRiskAction(user.CurrentRiskAction), "risk_policy_version": normalizeRiskPolicyVersion(user.RiskPolicyVersion),
		},
		IPAddress: clientIP, UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "reauthenticated", "reauthenticated_at": now.Format(time.RFC3339)})
}

func (h *Handler) LockSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := UserFromContext(r.Context())
	if !ok || user.CurrentSessionType != SessionTypePublicDevice {
		httpx.Error(w, r, http.StatusForbidden, "session_lock_forbidden", "session lock is only available in public computer mode")
		return
	}
	token := sessionToken(r, h.cookieName)
	now := time.Now().UTC()
	locked, err := h.store.LockSession(r.Context(), user.TenantID, user.ID, HashToken(token), now)
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	if !locked {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.session_locked",
		TargetType: "user", TargetID: user.ID, AfterValue: map[string]any{
			"risk_level": normalizeRiskLevel(user.CurrentRiskLevel), "risk_action": normalizeRiskAction(user.CurrentRiskAction),
			"reason": "public_computer_idle",
		},
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "locked", "locked_at": now.Format(time.RFC3339)})
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	token := sessionToken(r, h.cookieName)
	sessions, err := h.store.ListSessions(r.Context(), user.TenantID, user.ID, HashToken(token), time.Now().UTC())
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "session_lookup_failed", "failed to list sessions")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	sessionID := strings.TrimSpace(r.PathValue("id"))
	revoked, err := h.store.RevokeSession(r.Context(), user.TenantID, user.ID, sessionID, "user_revoked_device")
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "session_revoke_failed", "failed to revoke session")
		return
	}
	if !revoked {
		httpx.Error(w, r, http.StatusNotFound, "session_not_found", "session not found")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.session_revoked",
		TargetType: "auth_session", TargetID: sessionID, Reason: "user revoked device session",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	currentToken := sessionToken(r, h.cookieName)
	currentSessions, _ := h.store.ListSessions(r.Context(), user.TenantID, user.ID, HashToken(currentToken), time.Now().UTC())
	currentStillActive := false
	for _, session := range currentSessions {
		if session.Current {
			currentStillActive = true
			break
		}
	}
	if !currentStillActive {
		http.SetCookie(w, h.clearSessionCookie())
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked"})
}

func (h *Handler) LogoutAll(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	revoked, err := h.store.RevokeAllSessions(r.Context(), user.TenantID, user.ID, "user_logout_all")
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "session_revoke_failed", "failed to revoke sessions")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.sessions_revoked_all",
		TargetType: "user", TargetID: user.ID, Reason: "user logged out all devices",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	http.SetCookie(w, h.clearSessionCookie())
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "logged_out_all", "revoked_count": revoked})
}

func (h *Handler) AdminRevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	targetUserID := strings.TrimSpace(r.PathValue("id"))
	revoked, err := h.store.RevokeAllSessions(r.Context(), actor.TenantID, targetUserID, "administrator_revoked_all")
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "session_revoke_failed", "failed to revoke sessions")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.user_sessions_revoked",
		TargetType: "user", TargetID: targetUserID, Reason: "administrator revoked user sessions",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "revoked", "revoked_count": revoked})
}

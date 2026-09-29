package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type recoveryRequest struct {
	Token string `json:"token"`
}

type completeRecoveryRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (h *Handler) AdminCreateRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	actor, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	actorScope, ok := AccessScopeFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusForbidden, "access_scope_missing", "no valid data access scope is assigned")
		return
	}
	userID := strings.TrimSpace(r.PathValue("id"))
	if userID == "" || len(userID) > 128 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "user id is required")
		return
	}
	token, tokenHash, err := NewToken()
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create recovery link")
		return
	}
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	preview, err := h.store.CreateRecovery(r.Context(), actor, actorScope, userID, tokenHash, expiresAt)
	if err != nil {
		switch {
		case errors.Is(err, ErrManagedUserNotFound):
			httpx.Error(w, r, http.StatusNotFound, "managed_user_not_found", "managed user not found")
		case errors.Is(err, ErrUserStatusForbidden), errors.Is(err, ErrOrganizationScope):
			httpx.Error(w, r, http.StatusForbidden, "credential_recovery_forbidden", "current identity cannot recover this user")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "credential_recovery_failed", "failed to create recovery link")
		}
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.credential_recovery_created",
		TargetType: "user", TargetID: userID, AfterValue: map[string]any{"channel": "admin_assisted", "expires_at": expiresAt},
		Reason: "administrator created one-time credential recovery", IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"recovery": map[string]any{
			"token": token, "expires_at": expiresAt, "path": "/recover?token=" + token,
			"display_name": preview.DisplayName, "phone_masked": preview.PhoneMasked,
		},
	})
}

func (h *Handler) VerifyRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input recoveryRequest
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		writeRecoveryDecodeError(w, r, err)
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" || len(input.Token) > 512 {
		httpx.Error(w, r, http.StatusBadRequest, "recovery_invalid", "recovery link is invalid or expired")
		return
	}
	preview, err := h.store.FindRecovery(r.Context(), HashToken(input.Token), time.Now().UTC())
	if errors.Is(err, ErrRecoveryInvalid) {
		httpx.Error(w, r, http.StatusBadRequest, "recovery_invalid", "recovery link is invalid or expired")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"recovery": preview})
}

func (h *Handler) CompleteRecovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input completeRecoveryRequest
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		writeRecoveryDecodeError(w, r, err)
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" || len(input.Token) > 512 || input.Password == "" || len(input.Password) > maxPasswordBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "recovery token and password are required")
		return
	}
	tokenHash := HashToken(input.Token)
	// 预检只减少无效请求的哈希开销；是否仍可消费令牌，由最终的 CompleteRecovery 再确认。
	if _, err := h.store.FindRecovery(r.Context(), tokenHash, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrRecoveryInvalid) {
			httpx.Error(w, r, http.StatusBadRequest, "recovery_invalid", "recovery link is invalid or expired")
			return
		}
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	if !StrongPassword(input.Password) {
		httpx.Error(w, r, http.StatusBadRequest, "weak_password", "password must be a long passphrase of at least 15 characters")
		return
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	result, err := h.store.CompleteRecovery(r.Context(), tokenHash, passwordHash, time.Now().UTC())
	if errors.Is(err, ErrRecoveryInvalid) {
		httpx.Error(w, r, http.StatusBadRequest, "recovery_invalid", "recovery link is invalid or expired")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "credential_recovery_failed", "account recovery failed")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: result.TenantID, ActorID: result.UserID, Action: "auth.credential_recovery_completed",
		TargetType: "user", TargetID: result.UserID, Reason: "user completed credential recovery and revoked all sessions",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "password_reset"})
}

func writeRecoveryDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if authRequestBodyTooLarge(err) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
		return
	}
	httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid recovery request")
}

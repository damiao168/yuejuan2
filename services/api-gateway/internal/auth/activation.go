package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type activationRequest struct {
	Token string `json:"token"`
}

type completeActivationRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (h *Handler) AdminCreateActivation(w http.ResponseWriter, r *http.Request) {
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
		httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create invitation")
		return
	}
	expiresAt := time.Now().UTC().Add(48 * time.Hour)
	preview, err := h.store.CreateActivation(r.Context(), actor, actorScope, userID, tokenHash, expiresAt)
	if err != nil {
		switch {
		case errors.Is(err, ErrManagedUserNotFound):
			httpx.Error(w, r, http.StatusNotFound, "managed_user_not_found", "invited user not found")
		case errors.Is(err, ErrUserStatusForbidden), errors.Is(err, ErrOrganizationScope):
			httpx.Error(w, r, http.StatusForbidden, "activation_reissue_forbidden", "current identity cannot reissue this invitation")
		default:
			httpx.Error(w, r, http.StatusInternalServerError, "activation_reissue_failed", "failed to create invitation")
		}
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: actor.TenantID, ActorID: actor.ID, Action: "auth.activation_reissued",
		TargetType: "user", TargetID: userID, AfterValue: map[string]any{"expires_at": expiresAt},
		Reason: "administrator reissued one-time account activation", IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"activation": map[string]any{
			"token": token, "expires_at": expiresAt, "path": "/activate?token=" + token,
			"display_name": preview.DisplayName, "phone_masked": preview.PhoneMasked,
		},
	})
}

func (h *Handler) VerifyActivation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input activationRequest
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		writeActivationDecodeError(w, r, err)
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" || len(input.Token) > 512 {
		httpx.Error(w, r, http.StatusBadRequest, "activation_invalid", "activation link is invalid or expired")
		return
	}
	preview, err := h.store.FindActivation(r.Context(), HashToken(input.Token), time.Now().UTC())
	if errors.Is(err, ErrActivationInvalid) {
		httpx.Error(w, r, http.StatusBadRequest, "activation_invalid", "activation link is invalid or expired")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"activation": preview})
}

func (h *Handler) CompleteActivation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var input completeActivationRequest
	if err := decodeAuthJSON(w, r, &input, true); err != nil {
		writeActivationDecodeError(w, r, err)
		return
	}
	input.Token = strings.TrimSpace(input.Token)
	if input.Token == "" || len(input.Token) > 512 || input.Password == "" || len(input.Password) > maxPasswordBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "activation token and password are required")
		return
	}
	tokenHash := HashToken(input.Token)
	// 先检查邀请再计算昂贵的密码哈希；最终消费仍由 ActivateUser 核查，不能依赖这次预检。
	if _, err := h.store.FindActivation(r.Context(), tokenHash, time.Now().UTC()); err != nil {
		if errors.Is(err, ErrActivationInvalid) {
			httpx.Error(w, r, http.StatusBadRequest, "activation_invalid", "activation link is invalid or expired")
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
	result, err := h.store.ActivateUser(r.Context(), tokenHash, passwordHash, time.Now().UTC())
	if errors.Is(err, ErrActivationInvalid) {
		httpx.Error(w, r, http.StatusBadRequest, "activation_invalid", "activation link is invalid or expired")
		return
	}
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "activation_failed", "account activation failed")
		return
	}
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: result.TenantID, ActorID: result.UserID, Action: "auth.account_activated",
		TargetType: "user", TargetID: result.UserID, Reason: "user completed invitation activation",
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "activated"})
}

func writeActivationDecodeError(w http.ResponseWriter, r *http.Request, err error) {
	if authRequestBodyTooLarge(err) {
		httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
		return
	}
	httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid activation request")
}

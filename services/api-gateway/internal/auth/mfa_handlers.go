package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	qrcode "github.com/skip2/go-qrcode"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func (h *Handler) mfaAccess(w http.ResponseWriter, r *http.Request) (MFAStore, User, bool) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := UserFromContext(r.Context())
	if !ok || IsServiceUser(user) || user.CurrentSessionType == SessionTypeService || user.CurrentSessionType == SessionTypePublicDevice {
		httpx.Error(w, r, http.StatusForbidden, "mfa_forbidden", "MFA management requires a personal signed-in session")
		return nil, user, false
	}
	store, supported := h.store.(MFAStore)
	if !h.mfaEnabled || h.mfaCipher == nil || !supported {
		httpx.Error(w, r, http.StatusServiceUnavailable, "mfa_unavailable", "MFA management is not available")
		return nil, user, false
	}
	return store, user, true
}

func (h *Handler) mfaError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrMFAInvalid):
		httpx.Error(w, r, http.StatusUnauthorized, "mfa_verification_failed", "verification information is incorrect or expired")
	case errors.Is(err, ErrMFAConflict):
		httpx.Error(w, r, http.StatusConflict, "mfa_already_enabled", "an authenticator is already enabled")
	default:
		httpx.Error(w, r, http.StatusServiceUnavailable, "mfa_unavailable", "MFA service temporarily unavailable")
	}
}

func mfaDecode(w http.ResponseWriter, r *http.Request, input any) bool {
	if err := decodeAuthJSON(w, r, input, true); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
		} else {
			httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		}
		return false
	}
	return true
}

func (h *Handler) mfaAudit(r *http.Request, user User, action, method string) {
	after := map[string]any{
		"risk_level":          string(normalizeRiskLevel(user.CurrentRiskLevel)),
		"risk_action":         string(normalizeRiskAction(user.CurrentRiskAction)),
		"risk_policy_version": normalizeRiskPolicyVersion(user.RiskPolicyVersion),
	}
	if method != "" {
		after["auth_method"] = method
	}
	RecordAudit(r.Context(), h.store, AuditEvent{TenantID: user.TenantID, ActorID: user.ID, Action: action, TargetType: "user", TargetID: user.ID, AfterValue: after, IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context())})
}

func (h *Handler) mfaAttempt(user User, r *http.Request) LoginAttempt {
	return LoginAttempt{TenantCode: user.TenantCode, Identifier: user.Username, AccountID: user.ID, IPAddress: h.remoteIP(r)}
}

func (h *Handler) mfaLimit(w http.ResponseWriter, r *http.Request, limit LoginLimit) {
	w.Header().Set("Retry-After", strconv.Itoa(max(1, int(limit.RetryAfter.Seconds()))))
	httpx.Error(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many failed authentication attempts; retry later")
}

func (h *Handler) mfaGuard(w http.ResponseWriter, r *http.Request, user User) bool {
	limit, blocked := h.loginGuard.Check(r.Context(), h.mfaAttempt(user, r), time.Now().UTC())
	if !h.loginLimiterAvailable(w, r) {
		return false
	}
	if blocked {
		h.mfaLimit(w, r, limit)
		return false
	}
	return true
}

func (h *Handler) mfaFailure(w http.ResponseWriter, r *http.Request, user User, err error) {
	if !errors.Is(err, ErrMFAInvalid) {
		h.mfaError(w, r, err)
		return
	}
	h.mfaAudit(r, user, "auth.mfa_verification_failed", "")
	limit, blocked := h.loginGuard.RegisterFailure(r.Context(), h.mfaAttempt(user, r), time.Now().UTC())
	if !h.loginLimiterAvailable(w, r) {
		return
	}
	if blocked {
		h.mfaLimit(w, r, limit)
		return
	}
	h.mfaError(w, r, err)
}

func (h *Handler) mfaPassword(w http.ResponseWriter, r *http.Request, user User, password string) (string, bool) {
	if password == "" || len(password) > maxPasswordBytes {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "password is required")
		return "", false
	}
	if !h.mfaGuard(w, r, user) {
		return "", false
	}
	hash, err := h.store.FindPasswordHash(r.Context(), user.TenantID, user.ID)
	if err != nil && !errors.Is(err, ErrInvalidCredentials) {
		h.mfaError(w, r, err)
		return "", false
	}
	if err != nil || !CheckPassword(hash, password) {
		h.mfaFailure(w, r, user, ErrMFAInvalid)
		return "", false
	}
	// Password alone must not clear accumulated OTP failures or promote assurance.
	return hash, true
}

func (h *Handler) MFAStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user, ok := UserFromContext(r.Context())
	if !ok || IsServiceUser(user) || user.CurrentSessionType == SessionTypeService {
		httpx.Error(w, r, http.StatusForbidden, "mfa_forbidden", "MFA management requires a signed-in user")
		return
	}
	store, supported := h.store.(MFAStore)
	available := h.mfaEnabled && h.mfaCipher != nil && supported
	if !available {
		httpx.JSON(w, http.StatusOK, map[string]any{"available": false, "enabled": false, "recovery_codes_remaining": 0})
		return
	}
	record, err := store.FindTOTP(r.Context(), user.TenantID, user.ID)
	if err != nil && !errors.Is(err, ErrMFAInvalid) {
		h.mfaError(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"available": true, "enabled": record.EnabledAt != nil, "recovery_codes_remaining": record.RecoveryCodesRemaining})
}

func (h *Handler) EnrollTOTP(w http.ResponseWriter, r *http.Request) {
	store, user, ok := h.mfaAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !mfaDecode(w, r, &input) {
		return
	}
	passwordHash, ok := h.mfaPassword(w, r, user, input.Password)
	if !ok {
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "阅卷 EduGrade", AccountName: user.TenantCode + "/" + user.Username, Period: 30, SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		h.mfaError(w, r, err)
		return
	}
	png, err := qrcode.Encode(key.URL(), qrcode.Medium, 256)
	if err != nil {
		h.mfaError(w, r, err)
		return
	}
	now := time.Now().UTC()
	record := TOTPRecord{ID: uuid.NewString(), TenantID: user.TenantID, UserID: user.ID, EnrollmentSessionHash: HashToken(sessionToken(r, h.cookieName)), ExpectedPasswordHash: passwordHash, PendingExpiresAt: now.Add(MFAEnrollmentTTL), LastUsedStep: -1}
	if err = h.mfaCipher.encrypt(key.Secret(), &record); err == nil {
		err = store.SavePendingTOTP(r.Context(), record, now)
	}
	if err != nil {
		h.mfaError(w, r, err)
		return
	}
	h.mfaAudit(r, user, "auth.mfa_enrollment_started", "")
	httpx.JSON(w, http.StatusOK, map[string]any{"enrollment_id": record.ID, "secret": key.Secret(), "qr_code_data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), "expires_at": record.PendingExpiresAt})
}

func (h *Handler) ConfirmTOTP(w http.ResponseWriter, r *http.Request) {
	store, user, ok := h.mfaAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		EnrollmentID string `json:"enrollment_id"`
		Code         string `json:"code"`
	}
	if !mfaDecode(w, r, &input) || !h.mfaGuard(w, r, user) {
		return
	}
	now := time.Now().UTC()
	sessionHash := HashToken(sessionToken(r, h.cookieName))
	record, err := store.FindTOTP(r.Context(), user.TenantID, user.ID)
	if err == nil && (record.ID != input.EnrollmentID || record.EnabledAt != nil || record.EnrollmentSessionHash != sessionHash || !record.PendingExpiresAt.After(now) || record.FailedAttempts >= 5) {
		err = ErrMFAInvalid
	}
	var step int64
	if err == nil {
		var secret string
		secret, err = h.mfaCipher.decrypt(record)
		if err == nil {
			step, err = matchTOTPStep(input.Code, secret, now)
		}
	}
	if err != nil {
		if errors.Is(err, ErrMFAInvalid) && record.ID == input.EnrollmentID && record.EnrollmentSessionHash == sessionHash {
			if failureErr := store.RecordTOTPFailure(r.Context(), user.TenantID, user.ID, record.ID); failureErr != nil {
				err = failureErr
			}
		}
		h.mfaFailure(w, r, user, err)
		return
	}
	codes, hashes, err := newMFARecoveryCodes()
	if err == nil {
		err = store.ConfirmTOTP(h.mfaSecurityContext(r.Context(), h.remoteIP(r), r.UserAgent()), record, sessionHash, step, hashes, now)
	}
	if err != nil {
		h.mfaFailure(w, r, user, err)
		return
	}
	h.loginGuard.RegisterSuccess(r.Context(), h.mfaAttempt(user, r))
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "enabled", "recovery_codes": codes})
}

func (h *Handler) StartMFAChallenge(w http.ResponseWriter, r *http.Request) {
	store, user, ok := h.mfaAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		Operation string `json:"operation"`
		Password  string `json:"password"`
	}
	if !mfaDecode(w, r, &input) {
		return
	}
	if !validMFAOperation(input.Operation) {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_mfa_operation", "unsupported operation")
		return
	}
	hash, ok := h.mfaPassword(w, r, user, input.Password)
	if !ok {
		return
	}
	record, err := store.FindTOTP(r.Context(), user.TenantID, user.ID)
	if err == nil && record.EnabledAt == nil {
		err = ErrMFAInvalid
	}
	if err != nil {
		h.mfaError(w, r, err)
		return
	}
	token, tokenHash, err := NewToken()
	if err != nil {
		h.mfaError(w, r, err)
		return
	}
	now := time.Now().UTC()
	challenge := MFAChallenge{TokenHash: tokenHash, TenantID: user.TenantID, UserID: user.ID, CredentialID: record.ID, SessionHash: HashToken(sessionToken(r, h.cookieName)), ExpectedPasswordHash: hash, Operation: input.Operation, ExpiresAt: now.Add(MFAChallengeTTL)}
	if err = store.CreateMFAChallenge(r.Context(), challenge, now); err != nil {
		h.mfaError(w, r, err)
		return
	}
	methods := []string{"totp"}
	if input.Operation == MFAOperationDisable {
		methods = append(methods, "recovery_code")
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"challenge_id": token, "operation": input.Operation, "methods": methods, "expires_at": challenge.ExpiresAt})
}

func (h *Handler) VerifyMFAChallenge(w http.ResponseWriter, r *http.Request) {
	store, user, ok := h.mfaAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		ChallengeID string `json:"challenge_id"`
		Method      string `json:"method"`
		Code        string `json:"code"`
	}
	if !mfaDecode(w, r, &input) || !h.mfaGuard(w, r, user) {
		return
	}
	now := time.Now().UTC()
	proof := MFAProof{TenantID: user.TenantID, UserID: user.ID, SessionHash: HashToken(sessionToken(r, h.cookieName)), ChallengeHash: HashToken(input.ChallengeID), Now: now, Step: -1}
	challenge, err := store.FindMFAChallenge(r.Context(), user.TenantID, user.ID, proof.SessionHash, proof.ChallengeHash, now)
	if err == nil && challenge.VerifiedAt != nil {
		err = ErrMFAInvalid
	}
	if err == nil {
		proof.CredentialID = challenge.CredentialID
		switch input.Method {
		case "totp":
			var record TOTPRecord
			record, err = store.FindTOTP(r.Context(), user.TenantID, user.ID)
			if err == nil && record.ID != proof.CredentialID {
				err = ErrMFAInvalid
			}
			if err == nil {
				var secret string
				secret, err = h.mfaCipher.decrypt(record)
				if err == nil {
					proof.Step, err = matchTOTPStep(input.Code, secret, now)
				}
			}
		case "recovery_code":
			if challenge.Operation != MFAOperationDisable {
				err = ErrMFAInvalid
			} else {
				proof.RecoveryHash, err = recoveryCodeHash(input.Code)
			}
		default:
			err = ErrMFAInvalid
		}
	}
	if err == nil {
		err = store.VerifyMFAChallenge(h.mfaSecurityContext(r.Context(), h.remoteIP(r), r.UserAgent()), proof)
	}
	if err != nil {
		if errors.Is(err, ErrMFAInvalid) {
			if failureErr := store.RecordMFAChallengeFailure(r.Context(), user.TenantID, user.ID, proof.SessionHash, proof.ChallengeHash); failureErr != nil {
				err = failureErr
			}
		}
		h.mfaFailure(w, r, user, err)
		return
	}
	h.loginGuard.RegisterSuccess(r.Context(), h.mfaAttempt(user, r))
	h.mfaAudit(r, user, "auth.mfa_challenge_verified", input.Method)
	// This verifies only the named command, never the whole session/device.
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "verified", "operation": challenge.Operation, "expires_at": challenge.ExpiresAt})
}

func (h *Handler) finishMFA(w http.ResponseWriter, r *http.Request, operation string) {
	store, user, ok := h.mfaAccess(w, r)
	if !ok {
		return
	}
	var input struct {
		ChallengeID string `json:"challenge_id"`
	}
	if !mfaDecode(w, r, &input) {
		return
	}
	var codes, hashes []string
	var err error
	if operation == MFAOperationRotateRecovery {
		codes, hashes, err = newMFARecoveryCodes()
		if err != nil {
			h.mfaError(w, r, err)
			return
		}
	}
	// 最终命令由存储层再次校验并消费已验证挑战，不能只信任此前 verify 的响应。
	proof := MFAProof{TenantID: user.TenantID, UserID: user.ID, SessionHash: HashToken(sessionToken(r, h.cookieName)), ChallengeHash: HashToken(input.ChallengeID), Now: time.Now().UTC()}
	if err = store.FinishMFACommand(h.mfaSecurityContext(r.Context(), h.remoteIP(r), r.UserAgent()), proof, operation, hashes); err != nil {
		h.mfaError(w, r, err)
		return
	}
	if operation == MFAOperationDisable {
		http.SetCookie(w, h.clearSessionCookie())
		http.SetCookie(w, h.clearDeviceCookie())
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "disabled"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "rotated", "recovery_codes": codes})
}

func (h *Handler) DisableTOTP(w http.ResponseWriter, r *http.Request) {
	h.finishMFA(w, r, MFAOperationDisable)
}
func (h *Handler) RotateMFARecoveryCodes(w http.ResponseWriter, r *http.Request) {
	h.finishMFA(w, r, MFAOperationRotateRecovery)
}

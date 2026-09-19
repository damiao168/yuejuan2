package auth

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type Handler struct {
	store                  Store
	loginService           *LoginService
	sessionTTL             time.Duration
	rememberedTTL          time.Duration
	publicTTL              time.Duration
	loginGuard             LoginAttemptGuard
	loginLimiterFailClosed bool
	cookieName             string
	deviceCookieName       string
	cookieSecure           bool
	trustedProxies         []*net.IPNet
	mfaEnabled             bool
	mfaCipher              *mfaCipher
}

type HandlerOptions struct {
	LoginFailureLimit      int
	LoginFailureWindow     time.Duration
	RememberedSessionTTL   time.Duration
	PublicSessionTTL       time.Duration
	CookieName             string
	DeviceCookieName       string
	CookieSecure           bool
	RiskMode               string
	DeviceBindingTTL       time.Duration
	LoginLimiter           LoginLimiter
	LoginGuard             LoginAttemptGuard
	LoginLimiterFailClosed bool
	TrustedProxyCIDRs      []string
	MFAEnabled             bool
	MFAMasterKey           string
}

const DefaultSessionCookieName = "edugrade_session"

func NewHandler(store Store, sessionTTL time.Duration, options ...HandlerOptions) *Handler {
	cfg := HandlerOptions{
		LoginFailureLimit:  5,
		LoginFailureWindow: 15 * time.Minute,
		RiskMode:           "shadow",
		DeviceBindingTTL:   DefaultDeviceBindingTTL,
	}
	if len(options) > 0 {
		if options[0].LoginFailureLimit > 0 {
			cfg.LoginFailureLimit = options[0].LoginFailureLimit
		}
		if options[0].LoginFailureWindow > 0 {
			cfg.LoginFailureWindow = options[0].LoginFailureWindow
		}
		if options[0].RememberedSessionTTL > 0 {
			cfg.RememberedSessionTTL = options[0].RememberedSessionTTL
		}
		if options[0].PublicSessionTTL > 0 {
			cfg.PublicSessionTTL = options[0].PublicSessionTTL
		}
		if strings.TrimSpace(options[0].CookieName) != "" {
			cfg.CookieName = strings.TrimSpace(options[0].CookieName)
		}
		if strings.TrimSpace(options[0].DeviceCookieName) != "" {
			cfg.DeviceCookieName = strings.TrimSpace(options[0].DeviceCookieName)
		}
		cfg.CookieSecure = options[0].CookieSecure
		cfg.RiskMode = normalizeRiskMode(options[0].RiskMode)
		if options[0].DeviceBindingTTL > 0 {
			cfg.DeviceBindingTTL = options[0].DeviceBindingTTL
		}
		cfg.LoginLimiter = options[0].LoginLimiter
		cfg.LoginGuard = options[0].LoginGuard
		cfg.LoginLimiterFailClosed = options[0].LoginLimiterFailClosed
		cfg.TrustedProxyCIDRs = options[0].TrustedProxyCIDRs
		cfg.MFAEnabled = options[0].MFAEnabled
		cfg.MFAMasterKey = options[0].MFAMasterKey
	}
	if cfg.CookieName == "" {
		cfg.CookieName = DefaultSessionCookieName
	}
	if cfg.DeviceCookieName == "" {
		cfg.DeviceCookieName = "edugrade_device"
	}
	if cfg.RememberedSessionTTL <= 0 {
		cfg.RememberedSessionTTL = 30 * 24 * time.Hour
	}
	if cfg.PublicSessionTTL <= 0 {
		cfg.PublicSessionTTL = 4 * time.Hour
	}
	if cfg.LoginGuard == nil {
		if cfg.LoginLimiter != nil {
			cfg.LoginGuard = NewLayeredLoginAttemptGuard(
				NewLoginFailureLimiter(cfg.LoginFailureLimit, cfg.LoginFailureWindow),
				NewLoginFailureLimiter(max(cfg.LoginFailureLimit*10, 50), cfg.LoginFailureWindow),
				cfg.LoginLimiter,
			)
		} else {
			cfg.LoginGuard = NewMemoryLoginAttemptGuard(cfg.LoginFailureLimit, cfg.LoginFailureWindow)
		}
	}
	var credentialCipher *mfaCipher
	if cfg.MFAEnabled {
		credentialCipher, _ = newMFACipher(cfg.MFAMasterKey) // Invalid configuration fails closed.
	}
	handler := &Handler{
		store:                  store,
		sessionTTL:             sessionTTL,
		rememberedTTL:          cfg.RememberedSessionTTL,
		publicTTL:              cfg.PublicSessionTTL,
		loginGuard:             cfg.LoginGuard,
		loginLimiterFailClosed: cfg.LoginLimiterFailClosed,
		cookieName:             cfg.CookieName,
		deviceCookieName:       cfg.DeviceCookieName,
		cookieSecure:           cfg.CookieSecure,
		trustedProxies:         parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs),
		mfaEnabled:             cfg.MFAEnabled,
		mfaCipher:              credentialCipher,
	}
	riskStore, _ := store.(RiskStore)
	handler.loginService = NewLoginService(
		store,
		store,
		store,
		store,
		cfg.LoginGuard,
		NewStoreRiskEvaluator(riskStore, cfg.RiskMode),
		LoginServiceOptions{
			SessionTTL:             sessionTTL,
			RememberedSessionTTL:   cfg.RememberedSessionTTL,
			PublicSessionTTL:       cfg.PublicSessionTTL,
			DeviceBindingTTL:       cfg.DeviceBindingTTL,
			RiskMode:               cfg.RiskMode,
			LoginLimiterFailClosed: cfg.LoginLimiterFailClosed,
		},
	)
	return handler
}

type loginRequest struct {
	TenantCode     string `json:"tenant_code"`
	TenantHint     string `json:"tenant_hint,omitempty"`
	Username       string `json:"username"`
	Identifier     string `json:"identifier,omitempty"`
	Password       string `json:"password"`
	RememberDevice bool   `json:"remember_device,omitempty"`
	PublicDevice   bool   `json:"public_device,omitempty"`
	DeviceName     string `json:"device_name,omitempty"`
	ClientType     string `json:"client_type,omitempty"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type reauthenticateRequest struct {
	Password string `json:"password"`
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, false)
}

func (h *Handler) TokenLogin(w http.ResponseWriter, r *http.Request) {
	h.login(w, r, true)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request, tokenResponse bool) {
	w.Header().Set("Cache-Control", "no-store")
	var req loginRequest
	if err := decodeAuthJSON(w, r, &req, false); err != nil {
		if authRequestBodyTooLarge(err) {
			httpx.Error(w, r, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body is too large")
			return
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}

	result, err := h.loginService.Login(r.Context(), LoginCommand{
		TenantCode: req.TenantCode, TenantHint: req.TenantHint,
		Username: req.Username, Identifier: req.Identifier, Password: req.Password,
		RememberDevice: req.RememberDevice, PublicDevice: req.PublicDevice,
		DeviceName: req.DeviceName, ClientType: req.ClientType, TokenResponse: tokenResponse,
		IPAddress: h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
		DeviceToken: h.deviceToken(r),
	})
	if err != nil {
		h.writeLoginError(w, r, err)
		return
	}
	if result.DeviceToken != "" {
		http.SetCookie(w, h.deviceCookie(result.DeviceToken, result.DeviceTokenExpiresAt))
	}
	if result.ClearDeviceCookie {
		http.SetCookie(w, h.clearDeviceCookie())
	}
	if tokenResponse {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"token_type": "Bearer", "access_token": result.Token,
			"expires_at": result.ExpiresAt.Format(time.RFC3339), "user": result.User,
		})
		return
	}
	http.SetCookie(w, h.sessionCookie(result.Token, result.ExpiresAt, result.PersistentCookie))
	httpx.JSON(w, http.StatusOK, map[string]any{
		"expires_at": result.ExpiresAt.Format(time.RFC3339),
		"user":       result.User,
	})
}

func (h *Handler) writeLoginError(w http.ResponseWriter, r *http.Request, err error) {
	var loginErr *LoginServiceError
	if !errors.As(err, &loginErr) {
		httpx.Error(w, r, http.StatusInternalServerError, "session_create_failed", "failed to create session")
		return
	}
	switch loginErr.Kind {
	case LoginFailureInvalidRequest:
		message := "tenant and login identifier are required"
		if loginErr.Detail == "fields_too_large" {
			message = "login fields exceed supported size limits"
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", message)
	case LoginFailureInvalidDeviceMode:
		message := "public computers cannot be remembered devices"
		if loginErr.Detail == "public_token" {
			message = "public computer mode is only available to browser sessions"
		}
		httpx.Error(w, r, http.StatusBadRequest, "invalid_device_mode", message)
	case LoginFailureRateLimited:
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(loginErr.RetryAfter.Seconds()))))
		httpx.Error(w, r, http.StatusTooManyRequests, "login_rate_limited", "too many failed login attempts; retry later")
	case LoginFailureLimiterUnavailable:
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_rate_limiter_unavailable", "authentication rate limiter is temporarily unavailable")
	case LoginFailureServiceUnavailable:
		httpx.Error(w, r, http.StatusServiceUnavailable, "auth_service_unavailable", "authentication service temporarily unavailable")
	case LoginFailureInvalidCredentials:
		httpx.Error(w, r, http.StatusUnauthorized, "invalid_credentials", "login information is incorrect")
	case LoginFailureClientTypeForbidden:
		message := "client type is not allowed for this account"
		if loginErr.Detail == "browser_service_account" {
			message = "service accounts cannot create browser sessions"
		}
		httpx.Error(w, r, http.StatusForbidden, "client_type_forbidden", message)
	case LoginFailureClientTypeRequired:
		httpx.Error(w, r, http.StatusBadRequest, "client_type_required", "client_type must be desktop or service")
	case LoginFailureTokenGeneration:
		httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create session")
	case LoginFailureSessionCreate:
		httpx.Error(w, r, http.StatusInternalServerError, "session_create_failed", "failed to create session")
	default:
		httpx.Error(w, r, http.StatusInternalServerError, "session_create_failed", "failed to create session")
	}
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, r, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"user": user})
}

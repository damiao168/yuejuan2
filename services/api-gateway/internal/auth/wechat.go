package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"github.com/google/uuid"
	qrcode "github.com/skip2/go-qrcode"
)

var tenantCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type startWechatLoginRequest struct {
	TenantCode     string `json:"tenant_code"`
	TenantHint     string `json:"tenant_hint,omitempty"`
	RememberDevice bool   `json:"remember_device,omitempty"`
	PublicDevice   bool   `json:"public_device,omitempty"`
}

type pollWechatLoginRequest struct {
	ChallengeID string `json:"challenge_id"`
	PollToken   string `json:"poll_token"`
}

type wechatTokenResponse struct {
	OpenID  string `json:"openid"`
	UnionID string `json:"unionid"`
	ErrCode int    `json:"errcode"`
}

func (h *Handler) wechatRepository() (WechatLoginRepository, bool) {
	repository, ok := h.store.(WechatLoginRepository)
	return repository, ok
}

func (h *Handler) StartWechatLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.wechatEnabled || h.wechatAppID == "" || h.wechatAppSecret == "" || h.wechatRedirectURL == "" {
		httpx.Error(w, r, http.StatusServiceUnavailable, "wechat_login_unavailable", "wechat login is not configured")
		return
	}
	repository, ok := h.wechatRepository()
	if !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "wechat_login_unavailable", "wechat login storage is unavailable")
		return
	}
	var request startWechatLoginRequest
	if err := decodeAuthJSON(w, r, &request, false); err != nil {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid json body")
		return
	}
	tenantCode := strings.TrimSpace(request.TenantCode)
	if tenantCode == "" {
		tenantCode = strings.TrimSpace(request.TenantHint)
	}
	if !tenantCodePattern.MatchString(tenantCode) || request.RememberDevice && request.PublicDevice {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "tenant and device mode are invalid")
		return
	}
	state, stateHash, err := NewToken()
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create login challenge")
		return
	}
	// 二维码回调 state 与电脑轮询凭据分别生成；拿到扫码链接不代表能领取电脑会话。
	pollToken, pollTokenHash, err := NewToken()
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "token_generation_failed", "failed to create login challenge")
		return
	}
	id := uuid.NewString()
	expiresAt := time.Now().UTC().Add(h.wechatChallengeTTL)
	if err := repository.CreateWechatLoginChallenge(r.Context(), CreateWechatLoginChallengeInput{
		ID: id, StateHash: stateHash, PollTokenHash: pollTokenHash, TenantCode: tenantCode,
		RememberDevice: request.RememberDevice, PublicDevice: request.PublicDevice, ExpiresAt: expiresAt,
	}); err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "wechat_login_unavailable", "failed to persist login challenge")
		return
	}
	query := url.Values{
		"appid": {h.wechatAppID}, "redirect_uri": {h.wechatRedirectURL},
		"response_type": {"code"}, "scope": {"snsapi_login"}, "state": {state},
	}
	authorizeURL := "https://open.weixin.qq.com/connect/qrconnect?" + query.Encode() + "#wechat_redirect"
	png, err := qrcode.Encode(authorizeURL, qrcode.Medium, 320)
	if err != nil {
		httpx.Error(w, r, http.StatusInternalServerError, "wechat_qr_failed", "failed to create qr code")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"challenge_id": id, "poll_token": pollToken, "expires_at": expiresAt.Format(time.RFC3339),
		"qr_code_data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
	})
}

func (h *Handler) WechatCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !h.wechatEnabled {
		h.wechatCallbackPage(w, http.StatusServiceUnavailable, "微信登录暂不可用", "请返回电脑端改用账号密码登录。")
		return
	}
	repository, ok := h.wechatRepository()
	if !ok {
		h.wechatCallbackPage(w, http.StatusServiceUnavailable, "微信登录暂不可用", "请返回电脑端稍后重试。")
		return
	}
	state, code := strings.TrimSpace(r.URL.Query().Get("state")), strings.TrimSpace(r.URL.Query().Get("code"))
	if len(state) > 256 || len(code) > 512 || state == "" || code == "" {
		h.wechatCallbackPage(w, http.StatusBadRequest, "授权未完成", "授权参数无效，请返回电脑端刷新二维码。")
		return
	}
	now := time.Now().UTC()
	challenge, err := repository.FindWechatLoginChallengeByState(r.Context(), HashToken(state), now)
	if err != nil || challenge.Status != "pending" {
		h.wechatCallbackPage(w, http.StatusBadRequest, "二维码已失效", "请返回电脑端刷新二维码后重试。")
		return
	}
	identity, err := h.exchangeWechatCode(r, code)
	if err != nil {
		_ = repository.FailWechatLoginChallenge(r.Context(), challenge.ID, "wechat_authorization_failed", now)
		h.wechatCallbackPage(w, http.StatusBadGateway, "微信授权失败", "请返回电脑端重新扫码。")
		return
	}
	user, err := repository.FindUserByWechatIdentity(r.Context(), challenge.TenantCode, h.wechatAppID, identity.UnionID, identity.OpenID)
	if err != nil {
		failure := "wechat_account_unbound"
		if !errors.Is(err, ErrWechatIdentityUnbound) {
			failure = "wechat_login_unavailable"
		}
		_ = repository.FailWechatLoginChallenge(r.Context(), challenge.ID, failure, now)
		h.wechatCallbackPage(w, http.StatusForbidden, "账号尚未绑定", "请联系学校管理员绑定微信后再试，或返回电脑端使用账号密码登录。")
		return
	}
	if err := repository.AuthorizeWechatLoginChallenge(r.Context(), challenge.ID, user.TenantID, user.ID, now); err != nil {
		h.wechatCallbackPage(w, http.StatusConflict, "二维码已失效", "请返回电脑端刷新二维码后重试。")
		return
	}
	h.wechatCallbackPage(w, http.StatusOK, "授权成功", "电脑端正在登录，现在可以关闭此页面。")
}

func (h *Handler) exchangeWechatCode(r *http.Request, code string) (wechatTokenResponse, error) {
	query := url.Values{
		"appid": {h.wechatAppID}, "secret": {h.wechatAppSecret}, "code": {code}, "grant_type": {"authorization_code"},
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.weixin.qq.com/sns/oauth2/access_token?"+query.Encode(), nil)
	if err != nil {
		return wechatTokenResponse{}, err
	}
	response, err := h.wechatHTTPClient.Do(request)
	if err != nil {
		return wechatTokenResponse{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return wechatTokenResponse{}, fmt.Errorf("wechat status %d", response.StatusCode)
	}
	var payload wechatTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&payload); err != nil {
		return wechatTokenResponse{}, err
	}
	if payload.ErrCode != 0 || payload.OpenID == "" {
		return wechatTokenResponse{}, errors.New("wechat rejected authorization code")
	}
	return payload, nil
}

func (h *Handler) PollWechatLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	repository, ok := h.wechatRepository()
	if !h.wechatEnabled || !ok {
		httpx.Error(w, r, http.StatusServiceUnavailable, "wechat_login_unavailable", "wechat login is unavailable")
		return
	}
	var request pollWechatLoginRequest
	if err := decodeAuthJSON(w, r, &request, false); err != nil || len(request.PollToken) > 256 {
		httpx.Error(w, r, http.StatusBadRequest, "invalid_request", "invalid login challenge")
		return
	}
	now := time.Now().UTC()
	challenge, err := repository.FindWechatLoginChallenge(r.Context(), strings.TrimSpace(request.ChallengeID), HashToken(request.PollToken), now)
	if err != nil {
		httpx.Error(w, r, http.StatusUnauthorized, "wechat_challenge_invalid", "login challenge is invalid")
		return
	}
	if challenge.Status != "authorized" {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"status": challenge.Status, "error_code": challenge.ErrorCode, "expires_at": challenge.ExpiresAt.Format(time.RFC3339),
		})
		return
	}
	// 先一次性消费挑战再创建会话；之后创建失败也不回退挑战，客户端需重新扫码。
	challenge, err = repository.ConsumeWechatLoginChallenge(r.Context(), challenge.ID, HashToken(request.PollToken), now)
	if err != nil {
		httpx.Error(w, r, http.StatusConflict, "wechat_challenge_consumed", "login challenge was already consumed")
		return
	}
	user, err := repository.FindUserByID(r.Context(), challenge.TenantID, challenge.UserID)
	if err != nil {
		httpx.Error(w, r, http.StatusUnauthorized, "wechat_account_unavailable", "bound account is unavailable")
		return
	}
	result, err := h.issueWechatSession(r, user, challenge)
	if err != nil {
		httpx.Error(w, r, http.StatusServiceUnavailable, "session_create_failed", "failed to create session")
		return
	}
	if challenge.PublicDevice {
		http.SetCookie(w, h.clearDeviceCookie())
	}
	http.SetCookie(w, h.sessionCookie(result.Token, result.ExpiresAt, result.PersistentCookie))
	httpx.JSON(w, http.StatusOK, map[string]any{
		"status": "authenticated", "expires_at": result.ExpiresAt.Format(time.RFC3339), "user": result.User,
	})
}

func (h *Handler) issueWechatSession(r *http.Request, user UserWithPassword, challenge WechatLoginChallenge) (LoginResult, error) {
	if IsServiceUser(user.User) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err := h.store.RecordSuccessfulLogin(r.Context(), user.TenantID, user.ID, user.PasswordHash, ""); err != nil {
		return LoginResult{}, err
	}
	if scope, err := h.store.ResolveAccessScope(r.Context(), user.User); err == nil {
		organizationScope := scope.OrganizationScope()
		user.OrganizationScope = &organizationScope
	}
	sessionType, ttl := SessionTypeStandard, h.sessionTTL
	if challenge.PublicDevice {
		sessionType, ttl = SessionTypePublicDevice, h.publicTTL
	}
	if challenge.RememberDevice {
		sessionType, ttl = SessionTypeRememberedDevice, h.rememberedTTL
	}
	token, tokenHash, err := NewToken()
	if err != nil {
		return LoginResult{}, err
	}
	_, deviceID, err := NewToken()
	if err != nil {
		return LoginResult{}, err
	}
	now, expiresAt := time.Now().UTC(), time.Now().UTC().Add(ttl)
	_, err = h.store.CreateSession(r.Context(), CreateSessionInput{
		TenantID: user.TenantID, UserID: user.ID, TokenHash: tokenHash, SessionType: sessionType,
		DeviceID: deviceID, DeviceName: "微信扫码登录", UserAgentHash: hashUserAgent(r.UserAgent()),
		IPPrefix: networkPrefix(h.remoteIP(r)), ExpiresAt: expiresAt, SecurityEpoch: user.SecurityEpoch,
		RiskLevel: RiskLevelLow, RiskAction: RiskActionAllow, RiskEvaluatedAt: now,
		RiskPolicyVersion: "wechat-login-v1", RiskEvidenceQuality: RiskEvidenceSufficient, AuthMethod: "wechat",
	})
	if err != nil {
		return LoginResult{}, err
	}
	user.CurrentSessionType = sessionType
	RecordAudit(r.Context(), h.store, AuditEvent{
		TenantID: user.TenantID, ActorID: user.ID, Action: "auth.login_succeeded", TargetType: "user", TargetID: user.ID,
		AfterValue: map[string]any{"auth_method": "wechat", "session_type": sessionType},
		IPAddress:  h.remoteIP(r), UserAgent: r.UserAgent(), RequestID: logger.RequestID(r.Context()),
	})
	return LoginResult{Token: token, ExpiresAt: expiresAt, User: user.User, PersistentCookie: challenge.RememberDevice}, nil
}

func (h *Handler) wechatCallbackPage(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh-CN"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title><body style="font-family:system-ui;padding:48px 24px;text-align:center;color:#172033"><h1 style="font-size:24px">%s</h1><p style="color:#667085">%s</p></body></html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message))
}

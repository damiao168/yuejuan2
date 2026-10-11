package auth

import (
	"net/http"
	"strings"
	"time"
)

func (h *Handler) sessionCookie(token string, expiresAt time.Time, persistent bool) *http.Cookie {
	cookie := &http.Cookie{
		Name:     h.cookieName,
		Value:    token,
		Path:     "/api/v1",
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
	if persistent {
		cookie.Expires = expiresAt
		cookie.MaxAge = int(time.Until(expiresAt).Seconds())
	}
	return cookie
}

func (h *Handler) clearSessionCookie() *http.Cookie {
	// 删除时 Path 必须与设置时一致，否则浏览器会保留原会话 Cookie。
	return &http.Cookie{
		Name:     h.cookieName,
		Value:    "",
		Path:     "/api/v1",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (h *Handler) deviceToken(r *http.Request) string {
	cookie, err := r.Cookie(h.deviceCookieName)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(cookie.Value)
	// 当前令牌含 256 位，编码为 43 个 base64url 字符。
	// 较宽松的上限为未来格式前缀留出空间，同时避免攻击者控制的 Cookie 请求头成为无上限的查询键。
	if len(value) < 32 || len(value) > 128 {
		return ""
	}
	return value
}

func (h *Handler) deviceCookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name: h.deviceCookieName, Value: token, Path: "/api/v1/auth",
		Expires: expiresAt, MaxAge: max(1, int(time.Until(expiresAt).Seconds())),
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	}
}

func (h *Handler) clearDeviceCookie() *http.Cookie {
	return &http.Cookie{
		Name: h.deviceCookieName, Value: "", Path: "/api/v1/auth",
		Expires: time.Unix(0, 0).UTC(), MaxAge: -1,
		HttpOnly: true, Secure: h.cookieSecure, SameSite: http.SameSiteLaxMode,
	}
}

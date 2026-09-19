package auth

import (
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func normalizedDeviceName(value string, sessionType string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 128 {
		value = string(runes[:128])
	}
	if value != "" {
		return value
	}
	switch sessionType {
	case SessionTypeService:
		return "后台服务"
	case SessionTypeDesktopDevice:
		return "桌面客户端"
	default:
		return "浏览器"
	}
}

func (h *Handler) loginLimiterAvailable(w http.ResponseWriter, r *http.Request) bool {
	status, ok := h.loginGuard.(LoginLimiterStatus)
	if !h.loginLimiterFailClosed || !ok || !status.Degraded() {
		return true
	}
	httpx.Error(w, r, http.StatusServiceUnavailable, "auth_rate_limiter_unavailable", "authentication rate limiter is temporarily unavailable")
	return false
}

func hashUserAgent(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func networkPrefix(value string) string {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return (&net.IPNet{IP: ipv4.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}).String()
	}
	return (&net.IPNet{IP: ip.Mask(net.CIDRMask(64, 128)), Mask: net.CIDRMask(64, 128)}).String()
}

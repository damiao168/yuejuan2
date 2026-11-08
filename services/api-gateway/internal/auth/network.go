package auth

import (
	"net"
	"net/http"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func (h *Handler) remoteIP(r *http.Request) string {
	return httpx.ResolveClientIP(r, h.trustedProxies)
}

func parseTrustedProxyCIDRs(values []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err == nil {
			out = append(out, network)
		}
	}
	return out
}

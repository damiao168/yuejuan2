package auth

import (
	"net"
	"net/http"
	"strings"
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
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if peer == nil || !trustedIP(peer, h.trustedProxies) {
		return host
	}
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	current := peer
	for index := len(forwarded) - 1; index >= 0 && trustedIP(current, h.trustedProxies); index-- {
		next := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if next == nil {
			return host
		}
		current = next
	}
	return current.String()
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

func trustedIP(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

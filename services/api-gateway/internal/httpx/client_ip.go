package httpx

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type clientIPKey struct{}

// ClientIP 返回统一解析的客户端 IP；未经过中间件时只使用 TCP 对端，绝不直接相信转发头。
func ClientIP(r *http.Request) string {
	if value, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return value
	}
	return ResolveClientIP(r, nil)
}

// ResolveClientIP 只从可信对端沿 X-Forwarded-For 向左追溯，遇到首个不可信节点即停止。
func ResolveClientIP(r *http.Request, trusted []*net.IPNet) string {
	if value, ok := r.Context().Value(clientIPKey{}).(string); ok {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer := net.ParseIP(strings.TrimSpace(host))
	if peer == nil {
		return host
	}
	current := peer
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for index := len(forwarded) - 1; index >= 0 && trustedClientProxy(current, trusted); index-- {
		if strings.TrimSpace(forwarded[index]) == "" {
			return peer.String()
		}
		next := net.ParseIP(strings.TrimSpace(forwarded[index]))
		if next == nil {
			return peer.String()
		}
		current = next
	}
	return current.String()
}

func trustedClientProxy(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIPMiddleware 在业务路由前解析一次来源，认证、审计及公共查询限流共用这一事实。
func ClientIPMiddleware(cidrs []string) func(http.Handler) http.Handler {
	var networks []*net.IPNet
	for _, cidr := range cidrs {
		if _, network, err := net.ParseCIDR(strings.TrimSpace(cidr)); err == nil {
			networks = append(networks, network)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), clientIPKey{}, ResolveClientIP(r, networks))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

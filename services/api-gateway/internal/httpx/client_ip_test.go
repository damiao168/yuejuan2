package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPUsesOnlyTrustedProxyChain(t *testing.T) {
	for _, tc := range []struct{ peer, forwarded, want string }{
		{"10.44.0.2:4567", "203.0.113.5", "203.0.113.5"},
		{"203.0.113.8:4567", "192.0.2.99", "203.0.113.8"},
		{"10.44.0.2:4567", "192.0.2.99, 203.0.113.5", "203.0.113.5"},
		{"10.44.0.2:4567", "203.0.113.5, 10.44.0.3", "203.0.113.5"},
		{"10.44.0.2:4567", "bad header", "10.44.0.2"},
		{"[2001:db8::1]:4567", "192.0.2.99", "2001:db8::1"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		handler := ClientIPMiddleware([]string{"10.44.0.0/24"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := ClientIP(r); got != tc.want {
				t.Errorf("peer=%s forwarded=%s got=%s want=%s", tc.peer, tc.forwarded, got, tc.want)
			}
		}))
		handler.ServeHTTP(httptest.NewRecorder(), r)
	}
}

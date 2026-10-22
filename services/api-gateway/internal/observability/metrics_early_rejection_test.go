package observability

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/middleware"
)

// 指标层位于 CORS/CSRF 外；提前拒绝尚无 Pattern，私有路径和任意方法不能成为标签。
func TestEarlyRejectionMetricsDoNotRetainClientPathsOrMethods(t *testing.T) {
	registry := NewRegistry()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	handler := middleware.Chain(mux, registry.Middleware(), middleware.CORS([]string{"https://allowed.invalid"}, nil, nil), middleware.BrowserCSRF("session"))
	for i := 0; i < 100; i++ {
		for _, method := range []string{http.MethodOptions, http.MethodPost, fmt.Sprintf("CUSTOM%d", i)} {
			req := httptest.NewRequest(method, fmt.Sprintf("/private-student-%d", i), nil)
			req.Header.Set("Origin", "https://untrusted.invalid")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			wantStatus := http.StatusForbidden
			if method != http.MethodOptions && method != http.MethodPost {
				// 未知方法由路由 fallback 拒绝；方法仍来自客户端，须收敛标签。
				wantStatus = http.StatusNotFound
			}
			if rec.Code != wantStatus {
				t.Fatalf("expected rejection %d, got %d", wantStatus, rec.Code)
			}
		}
	}
	rec := httptest.NewRecorder()
	registry.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), "private-student-") || strings.Contains(rec.Body.String(), "CUSTOM") {
		t.Fatal("client identifiers leaked into metric labels")
	}
	if len(registry.requests) != 3 || len(registry.durations) != 3 || len(registry.errors) > 3 {
		t.Fatalf("unbounded metric maps: requests=%d durations=%d errors=%d", len(registry.requests), len(registry.durations), len(registry.errors))
	}
}

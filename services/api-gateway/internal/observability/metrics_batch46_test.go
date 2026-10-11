package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryCallsDatabaseThenAuthProvidersOutsideSnapshotLock(t *testing.T) {
	registry := NewRegistry()
	var calls []string
	// TryLock 不等待，锁边界回归会直接失败，不会让测试挂在回调重入上。
	checkUnlocked := func(name string) {
		calls = append(calls, name)
		if !registry.mu.TryLock() {
			t.Errorf("%s provider called while registry lock is held", name)
			return
		}
		registry.mu.Unlock()
	}
	registry.SetDatabaseStats(func() DatabaseStats {
		checkUnlocked("database")
		return DatabaseStats{OpenConnections: 3}
	})
	registry.SetAuthRateLimiterDegraded(func() bool {
		checkUnlocked("auth")
		return true
	})

	recorder := httptest.NewRecorder()
	registry.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if got := strings.Join(calls, ","); got != "database,auth" {
		t.Fatalf("provider order=%q want database,auth", got)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `edugrade_postgres_connections{state="open"} 3`) || !strings.Contains(body, "edugrade_auth_rate_limiter_degraded 1") {
		t.Fatalf("provider values missing from metrics: %s", body)
	}
}

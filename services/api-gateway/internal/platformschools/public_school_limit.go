package platformschools

import (
	"net/http"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

// 名称查询使用独立的每来源窗口，不能计入账号登录失败；内存桶只在本进程生效。
// 来源由可信代理中间件解析；直接调用时只取 TCP 对端，多副本仍需在入口统一限制。
func LimitPublicSchoolRead(next http.HandlerFunc) http.Handler {
	type bucket struct {
		since time.Time
		count int
	}
	var mu sync.Mutex
	buckets := map[string]bucket{}
	cleanupAt := time.Now().Add(time.Minute)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		ip := httpx.ClientIP(r)
		mu.Lock()
		if !now.Before(cleanupAt) {
			for key, item := range buckets {
				if now.Sub(item.since) >= time.Minute {
					delete(buckets, key)
				}
			}
			cleanupAt = now.Add(time.Minute)
		}
		item, exists := buckets[ip]
		if !exists || now.Sub(item.since) >= time.Minute {
			item = bucket{since: now}
		}
		blocked := item.count >= 60 || !exists && len(buckets) >= 10000
		if !blocked {
			item.count++
			buckets[ip] = item
		}
		mu.Unlock()
		if blocked {
			w.Header().Set("Retry-After", "60")
			httpx.Error(w, r, http.StatusTooManyRequests, "school_lookup_rate_limited", "too many school name lookups; retry later")
			return
		}
		next(w, r)
	})
}

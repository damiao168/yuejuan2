package schoolassistant

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

const (
	publicWebPageCacheTTL      = 30 * time.Second
	publicWebPageCacheMaxItems = 64
	publicWebPageCacheMaxBytes = 2 << 20
)

type publicWebPageCacheEntry struct {
	content   WebPageContent
	expiresAt time.Time
	bytes     int
}

// publicWebPageCache 仅接收匿名公开读取器取得的文本，不保存校内工具结果或模型回答。
// 缓存命中保留原读取时间且不续期；同键未命中共享读取，等待者的取消由独立任务管理。
type publicWebPageCache struct {
	mu          sync.Mutex
	entries     map[[32]byte]publicWebPageCacheEntry
	bytes       int
	now         func() time.Time
	flights     map[[32]byte]*publicWebPageFlight
	closed      bool
	stats       publicWebPageCacheStats
	fetchSlots  *semaphore.Weighted
	fetching    int
	closedCtx   context.Context
	closeCancel context.CancelFunc
}

func newPublicWebPageCache() *publicWebPageCache {
	closedCtx, closeCancel := context.WithCancel(context.Background())
	return &publicWebPageCache{entries: make(map[[32]byte]publicWebPageCacheEntry), flights: make(map[[32]byte]*publicWebPageFlight), now: time.Now,
		fetchSlots: semaphore.NewWeighted(publicWebPageMaxFlights), closedCtx: closedCtx, closeCancel: closeCancel}
}

func publicWebPageCacheKey(address string) [32]byte {
	// URL 已由读取入口验证。片段不会发送到服务器；查询参数与顺序仍原样区分，避免误合并资料。
	u, _ := url.Parse(address)
	u.Fragment = ""
	return sha256.Sum256([]byte(u.String()))
}

func (cache *publicWebPageCache) removeLocked(key [32]byte) {
	cache.bytes -= cache.entries[key].bytes
	delete(cache.entries, key)
}

func (cache *publicWebPageCache) expireLocked(now time.Time) {
	for key, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			cache.removeLocked(key)
			cache.stats.Expirations++
		}
	}
}

func (cache *publicWebPageCache) get(key [32]byte) (WebPageContent, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.expireLocked(cache.now())
	entry, exists := cache.entries[key]
	if exists {
		cache.stats.Hits++
	} else {
		cache.stats.Misses++
	}
	return entry.content, exists
}

func (cache *publicWebPageCache) put(key [32]byte, content WebPageContent, ttl time.Duration) {
	size := len(content.Text) + len(content.Title) + len(content.URL)
	if ttl <= 0 || size == 0 || size > publicWebPageCacheMaxBytes || content.ReadAt.IsZero() || !validWebPageURL(content.URL) {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.putLocked(key, content, ttl)
}

func (cache *publicWebPageCache) putLocked(key [32]byte, content WebPageContent, ttl time.Duration) {
	size := len(content.Text) + len(content.Title) + len(content.URL)
	if cache.closed || ttl <= 0 || size == 0 || size > publicWebPageCacheMaxBytes || content.ReadAt.IsZero() || !validWebPageURL(content.URL) {
		return
	}
	now := cache.now()
	cache.expireLocked(now)
	if _, exists := cache.entries[key]; exists {
		cache.removeLocked(key)
	}
	for len(cache.entries) >= publicWebPageCacheMaxItems || cache.bytes+size > publicWebPageCacheMaxBytes {
		var oldestKey [32]byte
		var oldestExpiry time.Time
		for candidate, entry := range cache.entries {
			if oldestExpiry.IsZero() || entry.expiresAt.Before(oldestExpiry) {
				oldestKey, oldestExpiry = candidate, entry.expiresAt
			}
		}
		cache.removeLocked(oldestKey)
		cache.stats.Evictions++
	}
	cache.entries[key] = publicWebPageCacheEntry{content: content, expiresAt: now.Add(min(ttl, publicWebPageCacheTTL)), bytes: size}
	cache.bytes += size
}

func publicWebPageCacheLifetime(header http.Header, now time.Time) time.Duration {
	// 公开 URL 也可能返回个性化或禁止存储的内容；没有条件请求支持时，no-cache 也直接跳过。
	if len(header.Values("Set-Cookie")) > 0 || strings.Contains(strings.ToLower(header.Get("Pragma")), "no-cache") {
		return 0
	}
	currentAge := time.Duration(0)
	if age := header.Get("Age"); age != "" {
		seconds, err := strconv.ParseInt(strings.TrimSpace(age), 10, 64)
		if err != nil || seconds < 0 || seconds >= int64(publicWebPageCacheTTL/time.Second) {
			return 0
		}
		currentAge = time.Duration(seconds) * time.Second
	}
	if date := header.Get("Date"); date != "" {
		issuedAt, err := http.ParseTime(date)
		if err != nil {
			return 0
		}
		currentAge = max(currentAge, now.Sub(issuedAt))
	}
	ttl := publicWebPageCacheTTL - currentAge
	for _, value := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(value, ",") {
			parts := strings.SplitN(strings.TrimSpace(strings.ToLower(directive)), "=", 2)
			switch strings.TrimSpace(parts[0]) {
			case "no-store", "no-cache", "private":
				return 0
			case "max-age", "s-maxage":
				if len(parts) != 2 {
					return 0
				}
				seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(parts[1]), "\""), 10, 64)
				if err != nil || seconds <= 0 {
					return 0
				}
				// 先限制整数再换算 duration，避免不可信响应头造成溢出。
				ttl = min(ttl, time.Duration(min(seconds, int64(publicWebPageCacheTTL/time.Second)))*time.Second-currentAge)
			}
		}
	}
	if expires := header.Get("Expires"); expires != "" {
		deadline, err := http.ParseTime(expires)
		if err != nil {
			return 0
		}
		ttl = min(ttl, deadline.Sub(now))
	}
	for _, vary := range header.Values("Vary") {
		for _, field := range strings.Split(vary, ",") {
			if strings.TrimSpace(field) == "*" {
				return 0
			}
		}
	}
	return max(ttl, 0)
}

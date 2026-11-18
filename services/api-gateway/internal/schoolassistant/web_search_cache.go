package schoolassistant

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	publicSearchCacheTTL      = 30 * time.Second
	publicSearchCacheMaxItems = 128
)

type publicSearchCacheEntry struct {
	sources   []Source
	expiresAt time.Time
}

// publicSearchCache 只保存搜索适配器取得的公开候选；不接收会话历史、模型判断或校内业务结果。
// 同时请求不合并，一个调用方取消不会中断其他调用方自己的搜索请求。
type publicSearchCache struct {
	mu      sync.Mutex
	entries map[[32]byte]publicSearchCacheEntry
	now     func() time.Time
}

func newPublicSearchCache() *publicSearchCache {
	return &publicSearchCache{entries: make(map[[32]byte]publicSearchCacheEntry), now: time.Now}
}

func clonePublicSearchSources(sources []Source) []Source {
	cloned := append([]Source(nil), sources...)
	for index := range cloned {
		if version := cloned[index].ReleaseVersion; version != nil {
			copyVersion := *version
			cloned[index].ReleaseVersion = &copyVersion
		}
	}
	return cloned
}

func (cache *publicSearchCache) expireLocked(now time.Time) {
	for key, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
}

func (cache *publicSearchCache) get(key [32]byte) ([]Source, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.expireLocked(cache.now())
	entry, exists := cache.entries[key]
	if !exists {
		return nil, false
	}
	return clonePublicSearchSources(entry.sources), true
}

func (cache *publicSearchCache) put(key [32]byte, sources []Source) {
	if len(sources) == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := cache.now()
	cache.expireLocked(now)
	if _, replacing := cache.entries[key]; !replacing && len(cache.entries) >= publicSearchCacheMaxItems {
		var oldestKey [32]byte
		var oldestExpiry time.Time
		for candidate, entry := range cache.entries {
			if oldestExpiry.IsZero() || entry.expiresAt.Before(oldestExpiry) {
				oldestKey, oldestExpiry = candidate, entry.expiresAt
			}
		}
		delete(cache.entries, oldestKey)
	}
	// 编排层会修改候选编号、正文和注释，缓存必须独立持有副本；命中时也不能刷新原取得时间。
	cache.entries[key] = publicSearchCacheEntry{sources: clonePublicSearchSources(sources), expiresAt: now.Add(publicSearchCacheTTL)}
}

func (s *searXNGSearch) Search(ctx context.Context, query string) (sources []Source, err error) {
	started := time.Now()
	cacheHit := false
	defer func() {
		status, code := publicSearchLogStatus(err)
		// 检索耗时与模型阶段分开记录；日志不包含搜索词、网址、正文或任何凭据。
		slog.InfoContext(ctx, "school assistant search", "event", "school_assistant_search", "duration_ms", time.Since(started).Milliseconds(), "result_count", len(sources), "cache_hit", cacheHit, "status", status, "error_code", code)
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" || utf8.RuneCountInString(query) > webSearchMaxQueryRunes {
		return nil, reject(400, "invalid_web_search_query", "请输入不超过 500 字的公开搜索问题。")
	}
	// 仅去除首尾空白；不合并日期、标点或内部空格，避免把不同意图误当成同一公开检索。
	key := sha256.Sum256([]byte(query))
	if s.cache != nil {
		if cached, exists := s.cache.get(key); exists {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			cacheHit = true
			return cached, nil
		}
	}
	sources, err = s.searchUncached(ctx, query)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err == nil && len(sources) > 0 && s.cache != nil {
		s.cache.put(key, sources)
	}
	return sources, err
}

func publicSearchLogStatus(err error) (status, code string) {
	if err == nil {
		return "succeeded", ""
	}
	var failure *queryError
	switch {
	case errors.As(err, &failure):
		return "failed", failure.code
	case errors.Is(err, context.Canceled):
		return "failed", "request_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "failed", "web_search_timeout"
	default:
		return "failed", "web_search_failed"
	}
}

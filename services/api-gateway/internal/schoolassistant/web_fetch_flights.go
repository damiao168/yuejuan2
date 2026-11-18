package schoolassistant

import (
	"context"
	"time"
)

const (
	publicWebPageMaxFlights = 8
	publicWebPageMaxWaiters = 64
)

type publicWebPageCacheStats struct {
	Hits, Misses, Coalesced, Evictions, Expirations, Fetches, Rejected uint64
	Active, Fetching, Waiters, Items, Bytes                            int
}

type publicWebPageFlight struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	waiters int
	content WebPageContent
	err     error
	ended   bool
}

type publicWebPageFetch func(context.Context) (WebPageContent, time.Duration, error)

// load 将同一公开 URL 的冷请求合并。共享任务不继承任何调用方的请求值和取消链，防止一个标签页取消其他等待者。
// 独立任务仍受固定超时、键数和等待者数限制；全部等待者退出时立即取消，不能积累无界后台读取。
func (cache *publicWebPageCache) load(ctx context.Context, key [32]byte, fetch publicWebPageFetch) (WebPageContent, bool, bool, error) {
	if err := ctx.Err(); err != nil {
		return WebPageContent{}, false, false, err
	}
	cache.mu.Lock()
	if cache.closed {
		cache.mu.Unlock()
		return WebPageContent{}, false, false, webPageFailure("closed")
	}
	cache.expireLocked(cache.now())
	if entry, exists := cache.entries[key]; exists {
		cache.stats.Hits++
		cache.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return WebPageContent{}, false, false, err
		}
		return entry.content, true, false, nil
	}
	cache.stats.Misses++
	flight, shared := cache.flights[key]
	if shared && (flight.waiters >= publicWebPageMaxWaiters || flight.ctx.Err() != nil) || !shared && len(cache.flights) >= publicWebPageMaxFlights {
		cache.stats.Rejected++
		cache.mu.Unlock()
		return WebPageContent{}, false, false, webPageFailure("busy")
	}
	if shared {
		flight.waiters++
		cache.stats.Coalesced++
	} else {
		callCtx, cancel := context.WithTimeout(context.Background(), webPageReadTimeout)
		flight = &publicWebPageFlight{ctx: callCtx, cancel: cancel, done: make(chan struct{}), waiters: 1}
		cache.flights[key] = flight
		go cache.fetch(key, flight, fetch)
	}
	cache.mu.Unlock()
	defer cache.leave(flight)
	select {
	case <-ctx.Done():
		return WebPageContent{}, false, shared, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return WebPageContent{}, false, shared, err
		}
		return flight.content, false, shared, flight.err
	}
}

func (cache *publicWebPageCache) fetch(key [32]byte, flight *publicWebPageFlight, fetch publicWebPageFetch) {
	content, ttl, err := cache.fetchLimited(flight.ctx, fetch)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if flight.ctx.Err() != nil {
		content, err = WebPageContent{}, flight.ctx.Err()
	}
	if err == nil && flight.waiters > 0 && !cache.closed {
		cache.putLocked(key, content, ttl)
	}
	flight.content, flight.err, flight.ended = content, err, true
	delete(cache.flights, key)
	flight.cancel()
	close(flight.done)
}

// fetchLimited 直接复用 Go 官方 Weighted，限制真正尚未返回的读取，而不是只数共享键。
// 回退到独立请求时也经过同一信号量；取消尚未完成清理的读取继续占槽，不能提前释放而放大并发。
func (cache *publicWebPageCache) fetchLimited(ctx context.Context, fetch publicWebPageFetch) (WebPageContent, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return WebPageContent{}, 0, err
	}
	cache.mu.Lock()
	if cache.closed {
		cache.mu.Unlock()
		return WebPageContent{}, 0, webPageFailure("closed")
	}
	// 已有等待者和请求都有上限，这里不再排一条无界资源等待队列。
	if !cache.fetchSlots.TryAcquire(1) {
		cache.stats.Rejected++
		cache.mu.Unlock()
		return WebPageContent{}, 0, webPageFailure("busy")
	}
	cache.fetching++
	cache.stats.Fetches++
	cache.mu.Unlock()
	callCtx, cancel := context.WithCancel(ctx)
	stopClose := context.AfterFunc(cache.closedCtx, cancel)
	defer func() {
		stopClose()
		cancel()
		cache.mu.Lock()
		cache.fetching--
		cache.fetchSlots.Release(1)
		cache.mu.Unlock()
	}()
	return fetch(callCtx)
}

func (cache *publicWebPageCache) leave(flight *publicWebPageFlight) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	flight.waiters--
	if flight.waiters == 0 && !flight.ended {
		flight.cancel()
	}
}

func (cache *publicWebPageCache) snapshot() publicWebPageCacheStats {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.expireLocked(cache.now())
	stats := cache.stats
	stats.Active, stats.Items, stats.Bytes = len(cache.flights), len(cache.entries), cache.bytes
	stats.Fetching = cache.fetching
	for _, flight := range cache.flights {
		stats.Waiters += flight.waiters
	}
	return stats
}

func (cache *publicWebPageCache) close() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.closed = true
	cache.closeCancel()
	for _, flight := range cache.flights {
		flight.cancel()
	}
	clear(cache.entries)
	cache.bytes = 0
}

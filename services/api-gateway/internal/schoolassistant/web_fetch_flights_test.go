package schoolassistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitPublicPageState(t *testing.T, cache *publicWebPageCache, matches func(publicWebPageCacheStats) bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if matches(cache.snapshot()) {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("shared reader did not reach expected state: %+v", cache.snapshot())
		}
	}
}

func TestPublicPageCoalescingAllCanceledStopsReadAndAllowsRetry(t *testing.T) {
	var calls atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-request.Context().Done()
			close(stopped)
			return nil, request.Context().Err()
		}
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	first, cancelFirst := context.WithCancel(t.Context())
	second, cancelSecond := context.WithCancel(t.Context())
	defer cancelFirst()
	defer cancelSecond()
	results := make(chan error, 2)
	go func() { _, err := reader.Read(first, "https://example.org/page"); results <- err }()
	<-started
	go func() { _, err := reader.Read(second, "https://example.org/page"); results <- err }()
	waitPublicPageState(t, reader.cache, func(stats publicWebPageCacheStats) bool { return stats.Waiters == 2 })
	cancelFirst()
	cancelSecond()
	for range 2 {
		if err := <-results; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter got %v", err)
		}
	}
	<-stopped
	waitPublicPageState(t, reader.cache, func(stats publicWebPageCacheStats) bool { return stats.Active == 0 })
	if stats := reader.cache.snapshot(); stats.Items != 0 {
		t.Fatalf("canceled shared read entered cache: %+v", stats)
	}
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil || calls.Load() != 2 {
		t.Fatalf("cancellation prevented a fresh retry: %v calls=%d", err, calls.Load())
	}
}

func TestPublicPageCoalescingBoundsTasksAndWaiters(t *testing.T) {
	cache := newPublicWebPageCache()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var group sync.WaitGroup
	fetch := func(callCtx context.Context) (WebPageContent, time.Duration, error) {
		<-callCtx.Done()
		return WebPageContent{}, 0, callCtx.Err()
	}
	for index := range publicWebPageMaxFlights {
		group.Add(1)
		go func() {
			defer group.Done()
			cache.load(ctx, publicWebPageCacheKey(fmt.Sprintf("https://example.org/%d", index)), fetch)
		}()
	}
	waitPublicPageState(t, cache, func(stats publicWebPageCacheStats) bool {
		return stats.Active == publicWebPageMaxFlights && stats.Fetching == publicWebPageMaxFlights
	})
	if _, _, _, err := cache.load(t.Context(), publicWebPageCacheKey("https://example.org/overflow"), fetch); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("new key exceeded active task limit: %v", err)
	}
	key := publicWebPageCacheKey("https://example.org/0")
	for range publicWebPageMaxWaiters - 1 {
		group.Add(1)
		go func() { defer group.Done(); cache.load(ctx, key, fetch) }()
	}
	waitPublicPageState(t, cache, func(stats publicWebPageCacheStats) bool {
		return stats.Waiters == publicWebPageMaxWaiters+publicWebPageMaxFlights-1
	})
	if _, _, _, err := cache.load(t.Context(), key, fetch); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("same key exceeded waiter limit: %v", err)
	}
	cancel()
	group.Wait()
	waitPublicPageState(t, cache, func(stats publicWebPageCacheStats) bool { return stats.Active == 0 })
	if stats := cache.snapshot(); stats.Fetches != publicWebPageMaxFlights || stats.Rejected != 2 {
		t.Fatalf("task accounting differs from limits: %+v", stats)
	}
}

func TestPublicPageCoalescingCanceledReadsHoldSlotsUntilTransportReturns(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, publicWebPageMaxFlights)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) <= publicWebPageMaxFlights {
			started <- struct{}{}
			<-request.Context().Done()
			// 模拟已收到取消但尚在清理连接的传输；槽位必须等真正返回后释放。
			<-release
			return nil, request.Context().Err()
		}
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, publicWebPageMaxFlights)
	for index := range publicWebPageMaxFlights {
		go func() { _, err := reader.Read(ctx, fmt.Sprintf("https://example.org/held/%d", index)); done <- err }()
	}
	for range publicWebPageMaxFlights {
		<-started
	}
	cancel()
	for range publicWebPageMaxFlights {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled waiter returned %v", err)
		}
	}
	if stats := reader.cache.snapshot(); stats.Fetching != publicWebPageMaxFlights {
		t.Fatalf("canceled transport released its slot before returning: %+v", stats)
	}
	for _, address := range []string{"https://example.org/held/0", "https://example.org/new"} {
		if _, err := reader.Read(t.Context(), address); err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("unfinished cancellation admitted another read: %v", err)
		}
	}
	if calls.Load() != publicWebPageMaxFlights {
		t.Fatal("actual transport concurrency exceeded its limit")
	}
	releaseOnce.Do(func() { close(release) })
	waitPublicPageState(t, reader.cache, func(stats publicWebPageCacheStats) bool { return stats.Fetching == 0 && stats.Active == 0 })
	if _, err := reader.Read(t.Context(), "https://example.org/new"); err != nil {
		t.Fatalf("returned transports did not release permits: %v", err)
	}
}

func TestPublicPageIndependentReadsReuseGlobalSemaphoreAndReleaseAfterFailure(t *testing.T) {
	reader := NewPublicWebPageReaderWithOptions(PublicWebPageReaderOptions{DisableCoalescing: true}).(*publicWebPageReader)
	var calls atomic.Int32
	started := make(chan struct{}, publicWebPageMaxFlights)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	reader.client.Transport = webPageRoundTrip(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) <= publicWebPageMaxFlights {
			started <- struct{}{}
			<-release
			return nil, errors.New("synthetic transport failure")
		}
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	done := make(chan error, publicWebPageMaxFlights)
	for index := range publicWebPageMaxFlights {
		go func() {
			_, err := reader.Read(t.Context(), fmt.Sprintf("https://example.org/independent/%d", index))
			done <- err
		}()
	}
	for range publicWebPageMaxFlights {
		<-started
	}
	if _, err := reader.Read(t.Context(), "https://example.org/overflow"); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("rollback disabled the actual fetch limit: %v", err)
	}
	if stats := reader.cache.snapshot(); stats.Active != 0 || stats.Fetching != publicWebPageMaxFlights || calls.Load() != publicWebPageMaxFlights {
		t.Fatalf("independent reads escaped semaphore accounting: %+v", stats)
	}
	releaseOnce.Do(func() { close(release) })
	for range publicWebPageMaxFlights {
		if err := <-done; err == nil {
			t.Fatal("transport failure was accepted")
		}
	}
	waitPublicPageState(t, reader.cache, func(stats publicWebPageCacheStats) bool { return stats.Fetching == 0 })
	if _, err := reader.Read(t.Context(), "https://example.org/after-failure"); err != nil {
		t.Fatalf("failed reads leaked semaphore permits: %v", err)
	}
}

func TestPublicPageCloseCancelsIndependentReadAndReleasesPermit(t *testing.T) {
	reader := NewPublicWebPageReaderWithOptions(PublicWebPageReaderOptions{DisableCoalescing: true}).(*publicWebPageReader)
	started := make(chan struct{})
	reader.client.Transport = webPageRoundTrip(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	done := make(chan error, 1)
	go func() { _, err := reader.Read(t.Context(), "https://example.org/independent"); done <- err }()
	<-started
	reader.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("closing reader did not cancel independent read: %v", err)
	}
	if stats := reader.cache.snapshot(); stats.Fetching != 0 || stats.Items != 0 {
		t.Fatalf("closed independent read leaked permit or cached result: %+v", stats)
	}
	if _, err := reader.Read(t.Context(), "https://example.org/new"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatal("closed independent reader accepted work")
	}
}

func TestPublicPageCoalescingCloseCancelsAndRejectsNewReads(t *testing.T) {
	started := make(chan struct{})
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	done := make(chan error, 1)
	go func() { _, err := reader.Read(t.Context(), "https://example.org/page"); done <- err }()
	<-started
	reader.Close()
	reader.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("closed reader did not stop its shared task: %v", err)
	}
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("closed reader accepted new task: %v", err)
	}
}

func TestPublicPageCoalescingRollbackKeepsCacheAndIndependentCancellation(t *testing.T) {
	reader := NewPublicWebPageReaderWithOptions(PublicWebPageReaderOptions{DisableCoalescing: true}).(*publicWebPageReader)
	var calls atomic.Int32
	started := make(chan struct{})
	reader.client.Transport = webPageRoundTrip(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := reader.Read(ctx, "https://example.org/page"); done <- err }()
	<-started
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil || calls.Load() != 2 {
		t.Fatalf("rollback disabled ordinary cache: %v calls=%d", err, calls.Load())
	}
}

func BenchmarkPublicPageCacheLoad(b *testing.B) {
	for _, mode := range []string{"cold", "hot", "burst"} {
		b.Run(mode, func(b *testing.B) {
			cache := newPublicWebPageCache()
			key := publicWebPageCacheKey("https://example.org/page")
			content := WebPageContent{Text: publicPageCacheBody, URL: "https://example.org/page", ReadAt: time.Now()}
			fetch := func(context.Context) (WebPageContent, time.Duration, error) {
				if mode == "burst" {
					time.Sleep(100 * time.Microsecond)
				}
				return content, publicWebPageCacheTTL, nil
			}
			if mode == "hot" {
				cache.put(key, content, publicWebPageCacheTTL)
			}
			b.ResetTimer()
			if mode == "burst" {
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						cache.load(b.Context(), key, fetch)
					}
				})
			} else {
				for index := 0; index < b.N; index++ {
					if mode == "cold" {
						key = publicWebPageCacheKey(fmt.Sprintf("https://example.org/%d", index))
					}
					cache.load(b.Context(), key, fetch)
				}
			}
			b.StopTimer()
			stats := cache.snapshot()
			b.ReportMetric(float64(stats.Fetches)/float64(b.N), "fetch/op")
			b.ReportMetric(float64(stats.Coalesced)/float64(b.N), "shared/op")
		})
	}
}

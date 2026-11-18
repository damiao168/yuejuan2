package schoolassistant

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const publicPageCacheBody = "这是一份已经读取的公开资料，包含具体的学校教育通知、日期和材料说明。"

func TestPublicPageCachePreservesReadTimeAndExpiresWithoutRenewal(t *testing.T) {
	calls := 0
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		calls++
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	now := time.Now().UTC()
	reader.cache.now = func() time.Time { return now }
	first, err := reader.Read(t.Context(), "https://example.org/page#first")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(29 * time.Second)
	second, err := reader.Read(t.Context(), "https://example.org/page#second")
	if err != nil || calls != 1 || first != second {
		t.Fatalf("cache changed original acquisition: calls=%d content=%+v err=%v", calls, second, err)
	}
	second.Text = "调用方改写不会污染缓存"
	third, _ := reader.Read(t.Context(), "https://example.org/page")
	if third != first {
		t.Fatal("caller contaminated cached content")
	}
	// 正好到期必须读取新资料，不能因上一秒命中而延长旧网页寿命。
	now = now.Add(time.Second)
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil || calls != 2 {
		t.Fatalf("expired page remained cached: calls=%d err=%v", calls, err)
	}
}

func TestPublicPageCacheHonorsResponseStorageRestrictions(t *testing.T) {
	for _, item := range []struct{ name, field, value string }{
		{"no store", "Cache-Control", "public, no-store"},
		{"private fields", "Cache-Control", `private="Set-Cookie"`},
		{"revalidate", "Cache-Control", "no-cache"},
		{"zero age", "Cache-Control", "max-age=0"},
		{"invalid age", "Cache-Control", "max-age=invalid"},
		{"cookie", "Set-Cookie", "session=private"},
		{"pragma", "Pragma", "no-cache"},
		{"vary all", "Vary", "*"},
		{"expired", "Expires", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)},
		{"old upstream", "Age", "30"},
		{"old response date", "Date", time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)},
	} {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
				calls++
				response := webPageResponse(request, "text/plain", publicPageCacheBody)
				response.Header.Set(item.field, item.value)
				return response, nil
			})
			for range 2 {
				if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil {
					t.Fatal(err)
				}
			}
			if calls != 2 {
				t.Fatalf("forbidden storage reused page: calls=%d", calls)
			}
		})
	}
	now := time.Now().UTC()
	for _, item := range []struct {
		header http.Header
		want   time.Duration
	}{
		{http.Header{"Cache-Control": {"max-age=8"}, "Age": {"3"}}, 5 * time.Second},
		{http.Header{"Cache-Control": {"max-age=9223372036854775807"}}, publicWebPageCacheTTL},
		{http.Header{"Expires": {now.Add(4 * time.Second).Format(http.TimeFormat)}}, now.Add(4 * time.Second).Truncate(time.Second).Sub(now)},
	} {
		if got := publicWebPageCacheLifetime(item.header, now); got != item.want {
			t.Fatalf("incorrect freshness lifetime: got=%v want=%v", got, item.want)
		}
	}
}

func TestPublicPageCacheDoesNotStoreFailedOrCanceledReads(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			calls := 0
			reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
				calls++
				response := webPageResponse(request, "text/plain", publicPageCacheBody)
				if calls == 1 {
					response.StatusCode = status
				}
				return response, nil
			})
			if _, err := reader.Read(t.Context(), "https://example.org/page"); err == nil {
				t.Fatal("failed read accepted")
			}
			if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil || calls != 2 {
				t.Fatalf("failure poisoned retry: calls=%d err=%v", calls, err)
			}
		})
	}
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		return webPageResponse(request, "text/plain", publicPageCacheBody), nil
	})
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if content, err := reader.Read(ctx, "https://example.org/page"); !errors.Is(err, context.Canceled) || content.Text != "" {
		t.Fatalf("cache overrode cancellation: content=%+v err=%v", content, err)
	}
}

func TestPublicPageCacheChecksURLBeforeLookupAndKeepsRedirectProvenance(t *testing.T) {
	calls := 0
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		calls++
		response := webPageResponse(request, "text/plain", publicPageCacheBody)
		if request.URL.Path == "/start" {
			response.StatusCode = http.StatusFound
			response.Header.Set("Location", "https://example.org/canonical")
		}
		return response, nil
	})
	first, err := reader.Read(t.Context(), "https://example.org/start")
	second, cachedErr := reader.Read(t.Context(), "https://example.org/start")
	if err != nil || cachedErr != nil || calls != 2 || first != second || second.URL != "https://example.org/canonical" {
		t.Fatalf("cached redirect lost provenance: calls=%d first=%+v second=%+v errors=%v/%v", calls, first, second, err, cachedErr)
	}
	for _, unsafe := range []string{"http://127.0.0.1/private", "http://169.254.169.254/latest/meta-data", "https://example.org:9443/page", "file:///private"} {
		// 即使缓存表被同包错误写入，也必须先拒绝非法入口，不能由命中绕过 URL 规则。
		reader.cache.entries[publicWebPageCacheKey(unsafe)] = publicWebPageCacheEntry{content: first, expiresAt: time.Now().Add(time.Minute)}
		if content, err := reader.Read(t.Context(), unsafe); err == nil || content.Text != "" || calls != 2 {
			t.Fatalf("unsafe URL bypassed validation through cache: %s err=%v", unsafe, err)
		}
	}
}

func TestPublicPageCacheBoundsEntriesAndTextBytes(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint(large), func(t *testing.T) {
			cache := newPublicWebPageCache()
			now := time.Now()
			cache.now = func() time.Time { return now }
			content := WebPageContent{Text: publicPageCacheBody, URL: "https://example.org/page", ReadAt: now}
			if large {
				content.Text = strings.Repeat("字", webPageMaxRunes)
			}
			firstKey := publicWebPageCacheKey("https://example.org/first")
			cache.put(firstKey, content, publicWebPageCacheTTL)
			for index := range publicWebPageCacheMaxItems {
				now = now.Add(time.Nanosecond)
				cache.put(publicWebPageCacheKey(fmt.Sprintf("https://example.org/%d", index)), content, publicWebPageCacheTTL)
			}
			if len(cache.entries) > publicWebPageCacheMaxItems || cache.bytes > publicWebPageCacheMaxBytes || cache.bytes <= 0 {
				t.Fatalf("unbounded cache: entries=%d bytes=%d", len(cache.entries), cache.bytes)
			}
			if _, exists := cache.get(firstKey); exists {
				t.Fatal("oldest entry not evicted")
			}
			now = now.Add(publicWebPageCacheTTL)
			cache.get(firstKey)
			if len(cache.entries) != 0 || cache.bytes != 0 {
				t.Fatalf("expired entries retained: count=%d bytes=%d", len(cache.entries), cache.bytes)
			}
		})
	}
}

func TestPublicPageCacheConcurrentMissCancellationDoesNotCancelOtherReader(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	finish := make(chan struct{})
	reader := stubPublicPageReader(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		case <-finish:
			return webPageResponse(request, "text/plain", publicPageCacheBody), nil
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	firstResult := make(chan error, 1)
	go func() {
		_, err := reader.Read(ctx, "https://example.org/page")
		firstResult <- err
	}()
	<-started
	secondResult := make(chan error, 1)
	go func() {
		content, err := reader.Read(t.Context(), "https://example.org/page")
		if err == nil && content.Text != publicPageCacheBody {
			err = errors.New("missing shared content")
		}
		secondResult <- err
	}()
	waitPublicPageState(t, reader.cache, func(stats publicWebPageCacheStats) bool { return stats.Waiters == 2 })
	cancel()
	if firstErr := <-firstResult; !errors.Is(firstErr, context.Canceled) {
		t.Fatalf("canceled caller did not stop independently: %v", firstErr)
	}
	close(finish)
	if err := <-secondResult; err != nil {
		t.Fatalf("one caller canceled the shared reader: %v", err)
	}
	if _, err := reader.Read(t.Context(), "https://example.org/page"); err != nil || calls.Load() != 1 {
		t.Fatalf("successful shared call was not cached: calls=%d err=%v", calls.Load(), err)
	}
}

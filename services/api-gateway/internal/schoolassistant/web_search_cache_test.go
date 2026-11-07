package schoolassistant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const publicSearchCacheResponse = `{"results":[{"title":"公开通知","url":"https://example.org/notice","content":"公开通知原始摘要"}]}`

func TestSearXNGSearchCachePreservesOriginalTimeAndIsolatesCandidateChanges(t *testing.T) {
	var calls atomic.Int32
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, publicSearchCacheResponse)
	})
	first, err := provider.Search(context.Background(), " 公开通知 ")
	if err != nil || len(first) != 1 {
		t.Fatalf("initial search failed: %v", err)
	}
	original := first[0]
	first[0].ID, first[0].URL, first[0].Excerpt, first[0].Note = "candidate-9", "https://different.example/", "后续读取的正文", "已读取网页正文。"
	second, err := provider.Search(context.Background(), "公开通知")
	if err != nil || len(second) != 1 || second[0] != original || calls.Load() != 1 {
		t.Fatalf("caller contaminated cached candidates: sources=%+v calls=%d err=%v", second, calls.Load(), err)
	}
	second[0].Label = "调用方修改"
	third, err := provider.Search(context.Background(), "公开通知")
	if err != nil || third[0] != original || !third[0].ReadAt.Equal(original.ReadAt) {
		t.Fatalf("cache hit changed original result or acquisition time: %+v %v", third, err)
	}
}

func TestSearXNGSearchCacheExpiresWithoutExtendingOnHits(t *testing.T) {
	var calls atomic.Int32
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, publicSearchCacheResponse)
	}).(*searXNGSearch)
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	provider.cache.now = func() time.Time { return now }
	if _, err := provider.Search(context.Background(), "公开通知"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(29 * time.Second)
	if _, err := provider.Search(context.Background(), "公开通知"); err != nil || calls.Load() != 1 {
		t.Fatalf("unexpired entry missed: calls=%d err=%v", calls.Load(), err)
	}
	// 命中不延长有效期；正好到30秒必须重新取资料，不能无限使用旧的实时检索结果。
	now = now.Add(time.Second)
	if _, err := provider.Search(context.Background(), "公开通知"); err != nil || calls.Load() != 2 {
		t.Fatalf("expired entry remained available: calls=%d err=%v", calls.Load(), err)
	}
}

func TestSearXNGSearchCacheDoesNotStoreFailuresOrEmptyResults(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "failed"}[failure], func(t *testing.T) {
			var calls atomic.Int32
			provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					if failure {
						w.WriteHeader(http.StatusBadGateway)
					}
					_, _ = io.WriteString(w, `{"results":[]}`)
					return
				}
				_, _ = io.WriteString(w, publicSearchCacheResponse)
			})
			_, _ = provider.Search(context.Background(), "公开通知")
			results, err := provider.Search(context.Background(), "公开通知")
			if err != nil || len(results) != 1 || calls.Load() != 2 {
				t.Fatalf("failed or empty result poisoned later attempt: calls=%d results=%v err=%v", calls.Load(), results, err)
			}
			if _, err := provider.Search(context.Background(), "公开通知"); err != nil || calls.Load() != 2 {
				t.Fatalf("successful retry was not cached: calls=%d err=%v", calls.Load(), err)
			}
		})
	}
}

func TestPublicSearchCacheIsBoundedAndEvictsOldestResult(t *testing.T) {
	cache := newPublicSearchCache()
	now := time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	firstKey := sha256.Sum256([]byte("first"))
	cache.put(firstKey, []Source{{Kind: "web", Label: "first"}})
	for index := 0; index < publicSearchCacheMaxItems; index++ {
		now = now.Add(time.Nanosecond)
		cache.put(sha256.Sum256([]byte{byte(index), 1}), []Source{{Kind: "web", Label: "newer"}})
	}
	if len(cache.entries) != publicSearchCacheMaxItems {
		t.Fatalf("unbounded cache: %d entries", len(cache.entries))
	}
	if _, exists := cache.get(firstKey); exists {
		t.Fatal("oldest result was not evicted")
	}
}

func TestSearXNGSearchCacheNeverOverridesCancellation(t *testing.T) {
	var calls atomic.Int32
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, publicSearchCacheResponse)
	})
	if _, err := provider.Search(context.Background(), "公开通知"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := provider.Search(ctx, "公开通知")
	if !errors.Is(err, context.Canceled) || results != nil || calls.Load() != 1 {
		t.Fatalf("cancelled caller received cached results: results=%v calls=%d err=%v", results, calls.Load(), err)
	}
}

func TestSearXNGConcurrentCallersDoNotShareCancellation(t *testing.T) {
	var calls atomic.Int32
	started, secondRelease := make(chan int32, 2), make(chan struct{})
	provider := newSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		number := calls.Add(1)
		started <- number
		if number == 1 {
			<-r.Context().Done()
			return
		}
		<-secondRelease
		_, _ = io.WriteString(w, publicSearchCacheResponse)
	})
	releaseSecond := sync.OnceFunc(func() { close(secondRelease) })
	t.Cleanup(releaseSecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { _, err := provider.Search(ctx, "公开通知"); first <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first search did not start")
	}
	go func() { _, err := provider.Search(context.Background(), "公开通知"); second <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("second search was incorrectly joined to the first")
	}
	cancel()
	releaseSecond()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first cancellation was lost: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("independent second request was cancelled: %v", err)
	}
	if _, err := provider.Search(context.Background(), "公开通知"); err != nil || calls.Load() != 2 {
		t.Fatalf("successful independent result not cached: calls=%d err=%v", calls.Load(), err)
	}
}

func TestSearXNGSearchObservesLatencyWithoutLoggingPublicContent(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, publicSearchCacheResponse)
	})
	for index := 0; index < 2; index++ {
		if _, err := provider.Search(context.Background(), "不应写入日志的独立公开查询"); err != nil {
			t.Fatal(err)
		}
	}
	for _, secret := range []string{"不应写入日志", "公开通知", "example.org", "原始摘要"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("search diagnostics leaked query or content: %s", secret)
		}
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("missing per-request search metrics: %d records", len(lines))
	}
	for index, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["event"] != "school_assistant_search" || record["duration_ms"] == nil || record["result_count"] != float64(1) || record["cache_hit"] != (index == 1) || record["status"] != "succeeded" || record["error_code"] != "" {
			t.Fatalf("incomplete search-only metrics: %v", record)
		}
	}
}

package schoolassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type parallelSearchProvider func(context.Context, string) ([]Source, error)

func (search parallelSearchProvider) Search(ctx context.Context, query string) ([]Source, error) {
	return search(ctx, query)
}

func parallelSearchService(search SearchProvider) *ConversationService {
	return NewConversationService(NewService(Dependencies{}), ConversationDependencies{Search: search})
}

func TestPublicSearchBatchStartsConcurrentlyAndPreservesInputOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, finished := make(chan string, 2), make(chan string, 2)
	releases := map[string]chan struct{}{"第一条": make(chan struct{}), "第二条": make(chan struct{})}
	service := parallelSearchService(parallelSearchProvider(func(ctx context.Context, query string) ([]Source, error) {
		started <- query
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-releases[query]:
			return []Source{{Label: query}}, nil
		}
	}))
	var groups [][]Source
	var events []ConversationEvent
	result := make(chan error, 1)
	go func() {
		var err error
		groups, err = service.searchWebBatch(ctx, []string{"第一条", "第二条"}, 4, func(event ConversationEvent) error {
			events = append(events, event)
			if event.Type == "tool_finished" {
				finished <- event.CallID
			}
			return nil
		})
		result <- err
	}()
	seen := map[string]bool{}
	for range 2 {
		seen[waitParallelRead(t, ctx, started)] = true
	}
	if len(seen) != 2 {
		t.Fatalf("batch did not start both searches: %v", seen)
	}
	close(releases["第二条"])
	if id := waitParallelRead(t, ctx, finished); id != "web-search-6" {
		t.Fatalf("second result identity drifted: %s", id)
	}
	close(releases["第一条"])
	if id := waitParallelRead(t, ctx, finished); id != "web-search-5" {
		t.Fatalf("first result identity drifted: %s", id)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0][0].Label != "第一条" || groups[1][0].Label != "第二条" || len(events) != 4 {
		t.Fatalf("completion order changed grouped sources: groups=%+v events=%+v", groups, events)
	}
}

func TestPublicSearchBatchPartialFailureKeepsOtherGroupAndSafeFailureEvent(t *testing.T) {
	service := parallelSearchService(parallelSearchProvider(func(_ context.Context, query string) ([]Source, error) {
		if query == "失败" {
			return nil, errors.New("private upstream credential detail")
		}
		return []Source{{Label: "成功资料"}}, nil
	}))
	var events []ConversationEvent
	groups, err := service.searchWebBatch(context.Background(), []string{"失败", "成功"}, 0, func(event ConversationEvent) error { events = append(events, event); return nil })
	if err != nil || len(groups) != 2 || len(groups[0]) != 0 || len(groups[1]) != 1 {
		t.Fatalf("partial failure discarded success: groups=%+v err=%v", groups, err)
	}
	failed := 0
	for _, event := range events {
		if strings.Contains(event.Message, "private upstream") {
			t.Fatal("raw provider error escaped")
		}
		if event.Status == "failed" {
			failed++
			if event.CallID != "web-search-1" || event.Message != webSearchFailed {
				t.Fatalf("wrong failed search event: %+v", event)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("failed group not represented exactly once: %+v", events)
	}
}

func TestPublicSearchBatchAllFailuresReturnFirstInputError(t *testing.T) {
	service := parallelSearchService(parallelSearchProvider(func(_ context.Context, query string) ([]Source, error) {
		if query == "第一条" {
			return nil, reject(503, "web_search_failed", webSearchFailed)
		}
		return nil, context.DeadlineExceeded
	}))
	_, err := service.searchWebBatch(context.Background(), []string{"第一条", "第二条"}, 0, func(ConversationEvent) error { return nil })
	requireToolError(t, err, "web_search_failed")
}

func TestPublicSearchBatchCancellationCollectsAllStartedSearches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	guard, stopGuard := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopGuard()
	started, cancelled := make(chan string, 2), make(chan string, 2)
	service := parallelSearchService(parallelSearchProvider(func(ctx context.Context, query string) ([]Source, error) {
		started <- query
		<-ctx.Done()
		cancelled <- query
		<-guard.Done()
		return nil, ctx.Err()
	}))
	result := make(chan error, 1)
	finished := 0
	go func() {
		_, err := service.searchWebBatch(ctx, []string{"第一条", "第二条"}, 0, func(event ConversationEvent) error {
			if event.Type == "tool_finished" {
				finished++
			}
			return nil
		})
		result <- err
	}()
	for range 2 {
		waitParallelRead(t, guard, started)
	}
	cancel()
	for range 2 {
		waitParallelRead(t, guard, cancelled)
	}
	select {
	case err := <-result:
		t.Fatalf("batch returned before searches exited: %v", err)
	default:
	}
	stopGuard()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || finished != 0 {
			t.Fatalf("cancelled batch published result: err=%v finished=%d", err, finished)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled searches were not collected")
	}
}

func TestPublicSearchBatchSSEFailureCancelsAndCollectsInFlightSearch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, exited := make(chan string, 2), make(chan string, 2)
	service := parallelSearchService(parallelSearchProvider(func(ctx context.Context, query string) ([]Source, error) {
		started <- query
		<-ctx.Done()
		exited <- query
		return nil, ctx.Err()
	}))
	writeFailure := errors.New("client stream closed")
	events := 0
	_, err := service.searchWebBatch(ctx, []string{"第一条", "第二条"}, 0, func(event ConversationEvent) error {
		if event.Type == "tool_finished" {
			t.Fatal("failed stream emitted completion")
		}
		events++
		if events == 2 {
			waitParallelRead(t, ctx, started)
			return writeFailure
		}
		return nil
	})
	if !errors.Is(err, writeFailure) || len(exited) != 1 {
		t.Fatalf("SSE failure leaked search: err=%v exited=%d", err, len(exited))
	}
}

func TestPublicSearchBatchRejectsOversizedBatchBeforeNetwork(t *testing.T) {
	service := parallelSearchService(parallelSearchProvider(func(context.Context, string) ([]Source, error) {
		t.Fatal("invalid batch reached provider")
		return nil, nil
	}))
	for _, queries := range [][]string{nil, {"一", "二", "三"}, {" "}, {strings.Repeat("中", webSearchMaxQueryRunes+1)}} {
		_, err := service.searchWebBatch(context.Background(), queries, 0, func(ConversationEvent) error { t.Fatal("invalid batch emitted tool event"); return nil })
		requireToolError(t, err, "invalid_web_search_query")
	}
}

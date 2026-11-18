package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func publicPrefetchSearch(question, query string, additional []string, reads int) string {
	fields := map[string]any{"action": "search", "query": query, "read_top_results": reads}
	if question != "" {
		fields["question"] = question
	}
	if len(additional) > 0 {
		fields["additional_queries"] = additional
	}
	data, _ := json.Marshal(fields)
	return string(data)
}

func TestPublicSearchLoopPrefetchesBodyBeforeSecondModelDecision(t *testing.T) {
	source := researchSource("forecast", "摘要仅说明有预报，缺少日期和具体温度")
	body := "2026年11月2日，北京预报晴，最高17℃，最低4℃。"
	question := "查询北京2026年11月2日的天气和最高最低温度"
	reader := &researchPageReader{pages: map[string]WebPageContent{source.URL: {URL: source.URL, Text: body}}}
	model := &publicLoopModel{replies: []string{publicPrefetchSearch(question, "北京 2026年11月2日 预报", nil, 1), publicLoopAnswer("北京当日预报晴，最高17℃、最低4℃。[1]", webSearchEvidence{SourceID: "candidate-1", Quote: body})}}
	search := &researchSearchProvider{results: [][]Source{{source}}}
	events, usage, err := runPublicLoopFixture(t, model, search, reader)
	if err != nil || len(model.requests) != 2 || len(search.queries) != 1 || len(reader.urls) != 1 || len(usage.events) != 2 {
		t.Fatalf("prefetch added a separate model decision: err=%v models=%d searches=%d reads=%d usage=%d", err, len(model.requests), len(search.queries), len(reader.urls), len(usage.events))
	}
	first, next := model.requests[0].Messages, model.requests[1].Messages
	if !strings.Contains(first[len(first)-1].Content, `"allow_read":false`) || !strings.Contains(first[len(first)-1].Content, `"can_read_search_results":true`) {
		t.Fatal("empty first-step candidates disabled search-attached reading")
	}
	if strings.Contains(first[0].Content, webSearchEvidenceInstructions) || !strings.Contains(next[0].Content, webSearchEvidenceInstructions) {
		t.Fatal("first decision carried unavailable page and evidence instructions")
	}
	nextState := lastTestUserMessage(model.requests[1]).Content
	if !strings.Contains(nextState, body) || !strings.Contains(nextState, `"question":"`+question+`"`) || !strings.Contains(nextState, `"read_source_ids":["candidate-1"]`) || !strings.Contains(nextState, `"reads_remaining":2`) {
		t.Fatal("prefetched body lost whole question, stable source or read budget")
	}
	sources := events[len(events)-2]
	if sources.Type != "sources" || len(sources.Sources) != 1 || sources.Sources[0].ID != "web-1" || sources.Sources[0].Note != "已读取网页正文。" || sources.Sources[0].Excerpt != body || events[len(events)-1].Type != "done" {
		t.Fatalf("prefetched evidence did not finish correctly: %+v", sources)
	}
}

func TestPublicSearchLoopPrefetchRoundRobinPreservesStableSourceIDs(t *testing.T) {
	a1, a2, b1 := researchSource("a1", "甲地第一条摘要"), researchSource("a2", "甲地第二条摘要"), researchSource("b1", "乙地摘要")
	groups := map[string][]Source{"甲地通知": {a1, a2}, "乙地通知": {a1, b1}}
	search := parallelSearchProvider(func(_ context.Context, query string) ([]Source, error) { return groups[query], nil })
	readURLs := make(chan string, 2)
	reader := parallelResearchReader(func(_ context.Context, address string) (WebPageContent, error) {
		readURLs <- address
		return WebPageContent{URL: address, Text: "该地正式通知时间已核实。"}, nil
	})
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("比较甲乙两地正式通知时间", "甲地通知", []string{"乙地通知"}, 2), publicLoopAnswer("两地正式通知已核实。[1][3]", webSearchEvidence{SourceID: "candidate-1", Quote: "该地正式通知时间已核实。"}, webSearchEvidence{SourceID: "candidate-3", Quote: "该地正式通知时间已核实。"})}}
	events, _, err := runPublicLoopFixture(t, model, search, reader)
	if err != nil || len(model.requests) != 2 || len(readURLs) != 2 {
		t.Fatalf("round-robin prefetch failed: err=%v calls=%d reads=%d", err, len(model.requests), len(readURLs))
	}
	seen := map[string]bool{<-readURLs: true, <-readURLs: true}
	if !seen[a1.URL] || !seen[b1.URL] || seen[a2.URL] {
		t.Fatalf("first query monopolized prefetched pages: %v", seen)
	}
	state := lastTestUserMessage(model.requests[1]).Content
	if !strings.Contains(state, `"read_source_ids":["candidate-1","candidate-3"]`) || !strings.Contains(state, `"reads_remaining":1`) {
		t.Fatal("duplicate result consumed a page or source IDs followed completion order")
	}
	sources := events[len(events)-2].Sources
	if len(sources) != 2 || sources[0].ID != "web-1" || sources[1].ID != "web-3" || sources[0].URL != a1.URL || sources[1].URL != b1.URL {
		t.Fatalf("prefetch renumbered citations: %+v", sources)
	}
}

func TestPublicSearchLoopPrefetchSkipsKnownRedirectAndSharesThreeReadBudget(t *testing.T) {
	entry, target, next1, next2 := researchSource("redirect-entry", "入口摘要"), researchSource("redirect-target", "同页摘要"), researchSource("next1", "新通知摘要一"), researchSource("next2", "新通知摘要二")
	readURLs := make(chan string, 3)
	reader := parallelResearchReader(func(_ context.Context, address string) (WebPageContent, error) {
		readURLs <- address
		if address == entry.URL {
			return WebPageContent{URL: target.URL, Text: "入口已安全重定向到正式正文。"}, nil
		}
		return WebPageContent{URL: address, Text: "新通知正文。"}, nil
	})
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("查询正式通知及补充通知", "正式通知", nil, 1), publicPrefetchSearch("", "补充通知", nil, 2), publicLoopAnswer("补充通知已核实。[3]", webSearchEvidence{SourceID: "candidate-3", Quote: "新通知正文。"})}}
	search := &researchSearchProvider{results: [][]Source{{entry, target}, {target, next1, next2}}}
	_, _, err := runPublicLoopFixture(t, model, search, reader)
	if err != nil || len(readURLs) != 3 || len(model.requests) != 3 {
		t.Fatalf("known redirect prefetch failed: err=%v reads=%d models=%d", err, len(readURLs), len(model.requests))
	}
	seen := map[string]bool{<-readURLs: true, <-readURLs: true, <-readURLs: true}
	if !seen[entry.URL] || !seen[next1.URL] || !seen[next2.URL] || seen[target.URL] {
		t.Fatalf("known alias was fetched again: %v", seen)
	}
	state := lastTestUserMessage(model.requests[2]).Content
	if !strings.Contains(state, `"read_source_ids":["candidate-1","candidate-3","candidate-4"]`) || !strings.Contains(state, `"reads_remaining":0`) || !strings.Contains(state, `"can_read_search_results":false`) {
		t.Fatal("prefetch and prior redirect did not share the three-page budget")
	}
}

func TestPublicSearchLoopRejectsPrefetchWithoutReaderBeforeSearch(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("公开通知全文", "公开通知", nil, 1)}}
	search := &researchSearchProvider{}
	events, _, err := runPublicLoopFixture(t, model, search, nil)
	code, _ := conversationError(err)
	if code != "invalid_web_search_action" || len(search.queries) != 0 {
		t.Fatalf("unavailable reading was rejected only after search: code=%s queries=%v", code, search.queries)
	}
	for _, event := range events {
		if event.Type == "tool_started" || event.Type == "done" {
			t.Fatalf("unavailable reading started network or completed: %+v", event)
		}
	}
}

func TestPublicSearchLoopRejectsPrefetchBeyondSharedBudgetBeforeNextSearch(t *testing.T) {
	a, b, c := researchSource("budget-a", "摘要甲"), researchSource("budget-b", "摘要乙"), researchSource("budget-c", "摘要丙")
	var reads atomic.Int32
	reader := parallelResearchReader(func(_ context.Context, address string) (WebPageContent, error) {
		reads.Add(1)
		return WebPageContent{URL: address, Text: "已核实通知。"}, nil
	})
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("公开通知全文", "公开通知", nil, 2), `{"action":"read","source_ids":["candidate-3"],"information_needed":"最后一页必要正文"}`, publicPrefetchSearch("", "补充通知", nil, 1)}}
	search := &researchSearchProvider{results: [][]Source{{a, b, c}}}
	_, _, err := runPublicLoopFixture(t, model, search, reader)
	code, _ := conversationError(err)
	if code != "invalid_web_search_action" || len(search.queries) != 1 || reads.Load() != 3 {
		t.Fatalf("read overspend reached another search: code=%s searches=%d reads=%d", code, len(search.queries), reads.Load())
	}
}

func TestPublicSearchLoopFailedPrefetchKeepsSummaryAndContinuesAnswer(t *testing.T) {
	source := researchSource("failed-body", "公开通知摘要说明今天9点开始。")
	reader := parallelResearchReader(func(context.Context, string) (WebPageContent, error) {
		return WebPageContent{}, errors.New("private upstream diagnostic")
	})
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("公开通知开始时间", "公开通知", nil, 1), publicLoopAnswer("摘要说明今天9点开始，正文未能读取。[1]", webSearchEvidence{SourceID: "candidate-1", Quote: source.Excerpt})}}
	events, _, err := runPublicLoopFixture(t, model, &researchSearchProvider{results: [][]Source{{source}}}, reader)
	if err != nil || len(model.requests) != 2 {
		t.Fatalf("body failure aborted usable summary: err=%v calls=%d", err, len(model.requests))
	}
	failed := 0
	for _, event := range events {
		if strings.Contains(event.Message, "private upstream") {
			t.Fatal("raw read failure reached user")
		}
		if event.Type == "tool_finished" && event.ToolName == "web_fetch" && event.Status == "failed" {
			failed++
		}
	}
	if failed != 1 || !strings.Contains(lastTestUserMessage(model.requests[1]).Content, `"reads_remaining":2`) {
		t.Fatal("failed body read disappeared from events or budget")
	}
	sources := events[len(events)-2].Sources
	if len(sources) != 1 || sources[0].Excerpt != source.Excerpt || sources[0].Note != "网页正文未能读取，仅使用搜索摘要。" {
		t.Fatalf("failed body was misrepresented as full text: %+v", sources)
	}
}

func TestPublicSearchLoopPrefetchCancellationJoinsReadersAndStopsNextModel(t *testing.T) {
	ctx, cancel := context.WithCancel(toolTestContext(t, "school_admin"))
	defer cancel()
	guard, stopGuard := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopGuard()
	started := make(chan string, 2)
	var stopped atomic.Int32
	reader := parallelResearchReader(func(ctx context.Context, address string) (WebPageContent, error) {
		started <- address
		<-ctx.Done()
		stopped.Add(1)
		return WebPageContent{}, ctx.Err()
	})
	model := &publicLoopModel{replies: []string{publicPrefetchSearch("公开通知全文", "公开通知", nil, 2)}}
	search := &researchSearchProvider{results: [][]Source{{researchSource("cancel-a", "摘要甲"), researchSource("cancel-b", "摘要乙")}}}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: search, Reader: reader})
	var events []ConversationEvent
	result := make(chan error, 1)
	go func() {
		result <- service.Converse(ctx, researchConversationInput("查询公开通知全文"), func(event ConversationEvent) error {
			events = append(events, event)
			return nil
		})
	}()
	waitParallelRead(t, guard, started)
	waitParallelRead(t, guard, started)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || stopped.Load() != 2 || len(model.requests) != 1 {
			t.Fatalf("cancellation left readers or started another decision: err=%v stopped=%d calls=%d", err, stopped.Load(), len(model.requests))
		}
	case <-guard.Done():
		t.Fatal("prefetch did not stop on cancellation")
	}
	for _, event := range events {
		if event.Type == "done" || event.Type == "sources" {
			t.Fatalf("cancelled prefetch committed response: %+v", event)
		}
	}
}

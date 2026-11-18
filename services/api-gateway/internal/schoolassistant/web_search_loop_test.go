package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type publicLoopModel struct {
	replies         []string
	requests        []modelgovernance.AgentRequest
	err             error
	responseContent func(string) string
}

func (m *publicLoopModel) Complete(context.Context, modelgovernance.ManagedAPIConnection, modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	return modelgovernance.AgentResponse{}, errors.New("public loop should use the same streaming decision call")
}

func (m *publicLoopModel) Stream(ctx context.Context, _ modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	index := len(m.requests)
	m.requests = append(m.requests, request)
	response := modelgovernance.AgentResponse{Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 5, OutputTokens: 5, TotalTokens: 10}, FinishReason: "stop"}
	if m.err != nil {
		return response, m.err
	}
	if index >= len(m.replies) {
		return response, errors.New("unexpected extra model call")
	}
	response.Message = modelgovernance.AgentMessage{Role: "assistant", Content: m.replies[index]}
	for _, char := range response.Message.Content {
		if err := ctx.Err(); err != nil {
			return response, err
		}
		if err := emit(modelgovernance.AgentEvent{Type: "content", Delta: string(char)}); err != nil {
			return response, err
		}
	}
	if m.responseContent != nil {
		response.Message.Content = m.responseContent(response.Message.Content)
	}
	return response, nil
}

func publicLoopAnswer(answer string, evidence ...webSearchEvidence) string {
	if evidence == nil {
		evidence = []webSearchEvidence{}
	}
	covered := []webSearchCoveredClaim{}
	for _, item := range evidence {
		covered = append(covered, webSearchCoveredClaim{Claim: "本轮事实" + item.SourceID, SourceIDs: []string{item.SourceID}})
	}
	data, _ := json.Marshal(map[string]any{"action": "answer", "answer": answer, "covered": covered, "missing": []string{}, "evidence": evidence})
	return string(data)
}

func publicLoopSearch(question, query string) string {
	data, _ := json.Marshal(map[string]any{"action": "search", "question": question, "query": query})
	return string(data)
}

func runPublicLoopFixture(t *testing.T, model *publicLoopModel, search SearchProvider, reader WebPageReader) ([]ConversationEvent, *conversationUsage, error) {
	t.Helper()
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: search, Reader: reader, Usage: usage})
	var events []ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("查看公开活动通知"), func(event ConversationEvent) error { events = append(events, event); return nil })
	return events, usage, err
}

func TestPublicSearchLoopAnswersDirectlyInOneModelCall(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicLoopAnswer("可以把已有通知整理成三句话。")}}
	search := &researchSearchProvider{}
	events, usage, err := runPublicLoopFixture(t, model, search, nil)
	if err != nil || len(model.requests) != 1 || len(usage.events) != 1 || len(search.queries) != 0 {
		t.Fatalf("direct response incurred extra work: calls=%d usage=%d err=%v", len(model.requests), len(usage.events), err)
	}
	if events[len(events)-1].Type != "done" || events[len(events)-1].Completion.Usage.TotalTokens != 10 {
		t.Fatalf("missing completed response: %+v", events)
	}
	for _, event := range events {
		if event.Type == "delta" && (strings.Contains(event.Delta, `"action"`) || strings.Contains(event.Delta, "covered")) {
			t.Fatal("decision JSON leaked")
		}
	}
}

func TestPublicSearchLoopAcceptsAdapterTrimmedJSONWhitespace(t *testing.T) {
	for _, test := range []struct {
		name                     string
		searchOuter, answerOuter bool
	}{
		{"empty additional queries", false, false},
		{"search outer whitespace", true, false},
		{"answer outer whitespace", false, true},
		{"both outer whitespace", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := `{"action":"search","question":"查询北京今天的天气","query":"北京 天气","additional_queries":[]}`
			answer := "\n 北京今天晴，最高17℃。[1]\n"
			last := publicLoopAnswer(answer, webSearchEvidence{SourceID: "candidate-1", Quote: "北京今天晴，最高17℃。"})
			if test.searchOuter {
				first = " \t\n" + first + "\r\n "
			}
			if test.answerOuter {
				last = " \t\n" + last + "\r\n "
			}
			// 真实HTTP适配器先发送原始增量，返回结果再TrimSpace；不能只用原样返回的fake覆盖此边界。
			model := &publicLoopModel{replies: []string{first, last}, responseContent: strings.TrimSpace}
			search := &researchSearchProvider{results: [][]Source{{researchSource("weather", "北京今天晴，最高17℃。")}}}
			events, usage, err := runPublicLoopFixture(t, model, search, nil)
			if err != nil || len(model.requests) != 2 || len(search.queries) != 1 || len(usage.events) != 2 {
				t.Fatalf("adapter normalization rejected valid JSON: err=%v calls=%d searches=%d usage=%d", err, len(model.requests), len(search.queries), len(usage.events))
			}
			var text strings.Builder
			for _, event := range events {
				if event.Type == "delta" && event.Channel == "content" {
					text.WriteString(event.Delta)
				}
			}
			if text.String() != answer || events[len(events)-1].Type != "done" || events[len(events)-1].Completion.Message.Content != answer {
				t.Fatalf("answer whitespace changed or JSON leaked: text=%q final=%+v", text.String(), events[len(events)-1])
			}
			sources := events[len(events)-2]
			if sources.Type != "sources" || len(sources.Sources) != 1 || sources.Sources[0].ID != "web-1" || sources.Sources[0].Excerpt != "北京今天晴，最高17℃。" {
				t.Fatalf("normalized JSON bypassed evidence validation: %+v", sources)
			}
		})
	}
}

func TestPublicSearchLoopRejectsActualStreamAndResponseDifferences(t *testing.T) {
	for _, test := range []struct {
		name, changedAction string
		wantSearches        int
	}{
		{"changed search", "search", 0},
		{"changed answer", "answer", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := publicLoopSearch("查询北京今天的天气", "北京 天气")
			answer := publicLoopAnswer("已核实天气。[1]", webSearchEvidence{SourceID: "candidate-1", Quote: "已核实天气。"})
			model := &publicLoopModel{replies: []string{" \n" + first + "\n", " \n" + answer + "\n"}}
			model.responseContent = func(content string) string {
				content = strings.TrimSpace(content)
				if strings.Contains(content, `"action":"`+test.changedAction+`"`) {
					if test.changedAction == "search" {
						return strings.Replace(content, `"query":"北京 天气"`, `"query":"上海 天气"`, 1)
					}
					return strings.Replace(content, `"answer":"已核实天气。[1]"`, `"answer":"返回值另写的天气。[1]"`, 1)
				}
				return content
			}
			search := &researchSearchProvider{results: [][]Source{{researchSource("weather", "已核实天气。")}}}
			events, usage, err := runPublicLoopFixture(t, model, search, nil)
			code, _ := conversationError(err)
			if code != "invalid_web_search_action" || len(search.queries) != test.wantSearches || len(usage.events) != len(model.requests) {
				t.Fatalf("content mismatch was accepted or lost usage: code=%s searches=%d usage=%d calls=%d", code, len(search.queries), len(usage.events), len(model.requests))
			}
			var text strings.Builder
			for _, event := range events {
				if event.Type == "done" || event.Type == "sources" {
					t.Fatalf("mismatched response completed or committed evidence: %+v", event)
				}
				if event.Type == "delta" && event.Channel == "content" {
					text.WriteString(event.Delta)
				}
			}
			// answer已开始时可有未完成正文，但不能显示工具JSON或适配器另写的返回值，也不能发送成功终态。
			if (test.changedAction == "search" && text.Len() > 0) || strings.Contains(text.String(), `"action"`) || strings.Contains(text.String(), "返回值另写") {
				t.Fatalf("internal or conflicting content leaked: %q", text.String())
			}
		})
	}
}

func TestPublicSearchLoopUsesSummaryInTwoCallsAndKeepsThirdSourceNumber(t *testing.T) {
	sources := []Source{researchSource("one", "无关材料一"), researchSource("two", "无关材料二"), researchSource("three", "活动时间为9点。")}
	model := &publicLoopModel{replies: []string{publicLoopSearch("公开活动时间", "公开活动时间"), publicLoopAnswer("活动时间为9点。[3]", webSearchEvidence{SourceID: "candidate-3", Quote: "活动时间为9点。"})}}
	search := &researchSearchProvider{results: [][]Source{sources}}
	events, usage, err := runPublicLoopFixture(t, model, search, nil)
	if err != nil || len(model.requests) != 2 || len(usage.events) != 2 || len(search.queries) != 1 {
		t.Fatalf("summary should need two decisions: err=%v calls=%d", err, len(model.requests))
	}
	if !strings.Contains(lastTestUserMessage(model.requests[1]).Content, `"question":"公开活动时间"`) {
		t.Fatal("independent public question was lost")
	}
	finalSources := events[len(events)-2]
	if finalSources.Type != "sources" || len(finalSources.Sources) != 1 || finalSources.Sources[0].ID != "web-3" || finalSources.Sources[0].URL != sources[2].URL {
		t.Fatalf("source was renumbered: %+v", finalSources)
	}
}

func TestPublicSearchLoopReadsOnlySelectedBodyInThreeCalls(t *testing.T) {
	source := researchSource("body", "搜索摘要尚无具体时间")
	reader := &researchPageReader{pages: map[string]WebPageContent{source.URL: {URL: source.URL, Text: "2026年11月2日，活动9点开始。"}}}
	model := &publicLoopModel{replies: []string{publicLoopSearch("公开活动具体时间", "公开活动具体时间"), `{"action":"read","source_ids":["candidate-1"],"information_needed":"活动具体日期和开始时间"}`, publicLoopAnswer("11月2日9点开始。[1]", webSearchEvidence{SourceID: "candidate-1", Quote: "2026年11月2日，活动9点开始。"})}}
	events, _, err := runPublicLoopFixture(t, model, &researchSearchProvider{results: [][]Source{{source}}}, reader)
	if err != nil || len(model.requests) != 3 || len(reader.urls) != 1 {
		t.Fatalf("unexpected read flow: err=%v calls=%d reads=%v", err, len(model.requests), reader.urls)
	}
	if events[len(events)-2].Sources[0].Note != "已读取网页正文。" {
		t.Fatal("read evidence was mislabelled")
	}
	if !strings.Contains(lastTestUserMessage(model.requests[2]).Content, `"read_source_ids":["candidate-1"]`) {
		t.Fatal("read history omitted")
	}
}

func TestPublicSearchLoopDoesNotRepeatSameSearch(t *testing.T) {
	source := researchSource("same", "公开通知")
	model := &publicLoopModel{replies: []string{publicLoopSearch("公开通知", "公开  通知"), `{"action":"search","query":"公开 通知"}`, publicLoopAnswer("当前材料不足以确定时间。")}}
	search := &researchSearchProvider{results: [][]Source{{source}}}
	events, _, err := runPublicLoopFixture(t, model, search, nil)
	if err != nil || len(search.queries) != 1 || len(model.requests) != 3 || events[len(events)-1].Type != "done" {
		t.Fatalf("duplicate search was not stopped: err=%v searches=%d", err, len(search.queries))
	}
	if !strings.Contains(lastTestUserMessage(model.requests[2]).Content, `"allow_search":false`) {
		t.Fatal("server stop was not returned to model")
	}
}

func TestPublicSearchLoopCannotExecuteThirdSearchOrFinishInvalidEvidence(t *testing.T) {
	for _, test := range []struct {
		name         string
		replies      []string
		wantCode     string
		wantSearches int
	}{
		{"third search", []string{publicLoopSearch("公开通知", "第一次"), `{"action":"search","query":"第二次"}`, `{"action":"search","query":"第三次"}`}, "invalid_web_search_action", 2},
		{"invented quote", []string{publicLoopSearch("公开通知", "公开通知"), publicLoopAnswer("捏造内容[1]", webSearchEvidence{SourceID: "candidate-1", Quote: "并不存在的原文"})}, "invalid_web_search_action", 1},
		{"invented reference", []string{publicLoopSearch("公开通知", "公开通知"), publicLoopAnswer("公开内容[2]", webSearchEvidence{SourceID: "candidate-1", Quote: "公开通知"})}, "invalid_web_search_citation", 1},
		{"late business", []string{publicLoopSearch("公开通知", "公开通知"), `{"action":"business"}`}, "invalid_web_search_action", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &publicLoopModel{replies: test.replies}
			search := &researchSearchProvider{results: [][]Source{{researchSource("one", "公开通知")}, {researchSource("two", "其他公开通知")}}}
			events, usage, err := runPublicLoopFixture(t, model, search, nil)
			code, _ := conversationError(err)
			if code != test.wantCode || len(search.queries) != test.wantSearches || len(usage.events) != len(model.requests) {
				t.Fatalf("invalid decision not contained: code=%s err=%v searches=%d usage=%d", code, err, len(search.queries), len(usage.events))
			}
			for _, event := range events {
				if event.Type == "done" || event.Type == "sources" {
					t.Fatal("invalid decision committed sources or success")
				}
			}
		})
	}
}

func TestPublicSearchLoopPublicStateNeverCopiesOriginalHistoryOrPageHint(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicLoopSearch("北京公开天气", "北京公开天气"), publicLoopAnswer("本次尚未核实天气。")}}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: &researchSearchProvider{results: [][]Source{{}}}})
	input := researchConversationInput("告诉我结果")
	input.Messages = append([]modelgovernance.ManagedChatMessage{{Role: "user", Content: "历史公开主题标记：北京天气"}, {Role: "assistant", Content: "已知上下文标记，不是本轮证据"}}, input.Messages...)
	input.Context = &ConversationContext{ExamID: toolTestExam, OriginModule: "private-page-marker"}
	if err := service.Converse(toolTestContext(t, "school_admin"), input, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(model.requests[0])
	later, _ := json.Marshal(model.requests[1])
	if !strings.Contains(string(first), "历史公开主题标记") || strings.Contains(string(first), "private-page-marker") || strings.Contains(string(later), "历史公开主题标记") || strings.Contains(string(later), "已知上下文标记") || strings.Contains(string(later), "private-page-marker") {
		t.Fatal("public context boundary changed")
	}
}

func TestPublicSearchLoopReservesRemainingTimeForAnswer(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicLoopAnswer("尚未取得可靠资料，无法核实。")}}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: &researchSearchProvider{}})
	ctx, cancel := context.WithTimeout(toolTestContext(t, "school_admin"), 30*time.Second)
	defer cancel()
	if err := service.Converse(ctx, researchConversationInput("查询公开资料"), nil); err != nil {
		t.Fatal(err)
	}
	request, _ := json.Marshal(model.requests[0])
	if !strings.Contains(string(request), `\"allow_search\":false`) || !strings.Contains(string(request), `\"allow_read\":false`) {
		t.Fatal("answer time was not reserved")
	}
}

func TestPublicSearchLoopPreservesWholeQuestionBeyondFirstSubquery(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicLoopSearch("比较北京和上海今天的气温", "北京今天的气温"), `{"action":"search","query":"上海今天的气温"}`, publicLoopAnswer("本次材料不足以可靠比较两个城市。")}}
	_, _, err := runPublicLoopFixture(t, model, &researchSearchProvider{results: [][]Source{{researchSource("beijing", "北京公开预报")}, {researchSource("shanghai", "上海公开预报")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range model.requests[1:] {
		if !strings.Contains(lastTestUserMessage(request).Content, `"question":"比较北京和上海今天的气温"`) {
			t.Fatal("first subquery overwrote whole task")
		}
	}
}

func TestPublicSearchLoopRejectsMissingOrChangedWholeQuestionBeforeSearch(t *testing.T) {
	for _, replies := range [][]string{
		{`{"action":"search","query":"北京天气"}`},
		{publicLoopSearch("比较北京上海天气", "北京天气"), publicLoopSearch("只查北京天气", "北京未来天气")},
	} {
		model := &publicLoopModel{replies: replies}
		search := &researchSearchProvider{results: [][]Source{{researchSource("weather", "公开预报")}}}
		_, _, err := runPublicLoopFixture(t, model, search, nil)
		code, _ := conversationError(err)
		if code != "invalid_web_search_action" || len(search.queries) != len(replies)-1 {
			t.Fatalf("task corruption reached search: %v searches=%d", err, len(search.queries))
		}
	}
}

func TestPublicSearchLoopDoesNotReadKnownRedirectAliasTwice(t *testing.T) {
	first, target := researchSource("redirect", "入口摘要"), researchSource("target", "终点摘要")
	reader := &researchPageReader{pages: map[string]WebPageContent{first.URL: {URL: target.URL, Text: "已读取的正文"}, target.URL: {URL: target.URL, Text: "不应再次读取"}}}
	model := &publicLoopModel{replies: []string{publicLoopSearch("查询公开通知", "公开通知"), `{"action":"read","source_ids":["candidate-1"],"information_needed":"通知正文"}`, `{"action":"read","source_ids":["candidate-2"],"information_needed":"重复终点"}`}}
	_, _, err := runPublicLoopFixture(t, model, &researchSearchProvider{results: [][]Source{{first, target}}}, reader)
	code, _ := conversationError(err)
	if code != "invalid_web_search_action" || len(reader.urls) != 1 {
		t.Fatalf("known alias was re-read: err=%v calls=%v", err, reader.urls)
	}
	last := lastTestUserMessage(model.requests[2]).Content
	if !strings.Contains(last, `"reads_remaining":2`) || !strings.Contains(last, `"available_read_source_ids":[]`) {
		t.Fatal("alias consumed a read or stayed available")
	}
}

func TestPublicSearchLoopBatchesIndependentQueriesWithoutAnotherModelRound(t *testing.T) {
	model := &publicLoopModel{replies: []string{`{"action":"search","question":"比较北京上海天气","query":"北京天气","additional_queries":["上海天气"]}`, publicLoopAnswer("两地预报有所不同。[1][2]", webSearchEvidence{SourceID: "candidate-1", Quote: "北京公开预报"}, webSearchEvidence{SourceID: "candidate-2", Quote: "上海公开预报"})}}
	var calls atomic.Int32
	provider := parallelSearchProvider(func(_ context.Context, query string) ([]Source, error) {
		calls.Add(1)
		return []Source{researchSource(query, strings.TrimSuffix(query, "天气")+"公开预报")}, nil
	})
	events, usage, err := runPublicLoopFixture(t, model, provider, nil)
	if err != nil || len(model.requests) != 2 || calls.Load() != 2 || len(usage.events) != 2 {
		t.Fatalf("batch added model round or lost query: err=%v models=%d searches=%d", err, len(model.requests), calls.Load())
	}
	sources := events[len(events)-2].Sources
	if len(sources) != 2 || sources[0].Excerpt != "北京公开预报" || sources[1].Excerpt != "上海公开预报" {
		t.Fatalf("concurrent source order changed: %+v", sources)
	}
}

func TestPublicSearchLoopRejectsBatchBeyondRemainingSearchBudget(t *testing.T) {
	model := &publicLoopModel{replies: []string{publicLoopSearch("公开通知", "首次通知"), `{"action":"search","query":"第二次通知","additional_queries":["第三次通知"]}`}}
	search := &researchSearchProvider{results: [][]Source{{researchSource("one", "公开通知")}}}
	_, _, err := runPublicLoopFixture(t, model, search, nil)
	code, _ := conversationError(err)
	if code != "invalid_web_search_action" || len(search.queries) != 1 {
		t.Fatalf("oversized batch reached network: err=%v searches=%v", err, search.queries)
	}
}

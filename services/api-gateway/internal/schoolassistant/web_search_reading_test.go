package schoolassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestWebResearchHTTPStreamsSelectedPageBodyAndItsSource(t *testing.T) {
	source := researchSource("notice", "摘要里没有通知的具体时间。")
	quote := "通知正文明确规定：本次活动于11月2日上午9时开始。"
	reader := &researchPageReader{pages: map[string]WebPageContent{source.URL: {Text: quote, Title: "公开活动通知", URL: source.URL}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"公开活动具体时间"}`},
		{content: researchReadAction([]string{"candidate-1"}, "需要通知中的具体时间")},
		{content: researchAnswerAction("活动于11月2日上午9时开始。[1]", []webSearchEvidence{{SourceID: "candidate-1", Quote: quote}}, nil)},
	}, answer: "活动于11月2日上午9时开始。[1]"}
	handler := NewHandler(Dependencies{Conversation: ConversationDependencies{Models: conversationStore(), Model: model, Search: &researchSearchProvider{results: [][]Source{{source}}}, Reader: reader}})
	data, _ := json.Marshal(researchConversationInput("查询活动具体时间"))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/school-assistant/conversations/stream", bytes.NewReader(data)).WithContext(toolTestContext(t, "school_admin"))
	response := httptest.NewRecorder()
	handler.ConversationStream(response, request)
	for _, want := range []string{`"tool_name":"web_fetch"`, `"note":"已读取网页正文。"`, quote, `"type":"done"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Fatalf("HTTP omitted read page evidence %q: %s", want, response.Body.String())
		}
	}
	if response.Code != 200 || len(reader.urls) != 1 {
		t.Fatalf("HTTP did not use configured page reader: status=%d calls=%v", response.Code, reader.urls)
	}
}

type researchPageReader struct {
	mu     sync.Mutex
	pages  map[string]WebPageContent
	urls   []string
	err    error
	onRead func(context.Context)
}

func (r *researchPageReader) Read(ctx context.Context, address string) (WebPageContent, error) {
	r.mu.Lock()
	r.urls = append(r.urls, address)
	r.mu.Unlock()
	if r.onRead != nil {
		r.onRead(ctx)
	}
	if ctx.Err() != nil {
		return WebPageContent{}, ctx.Err()
	}
	if r.err != nil {
		return WebPageContent{}, r.err
	}
	page, ok := r.pages[address]
	if !ok {
		return WebPageContent{}, errors.New("unexpected selected page")
	}
	return page, nil
}

func researchReadAction(ids []string, needed string) string {
	if ids == nil {
		ids = []string{}
	}
	data, _ := json.Marshal(map[string]any{"action": "read", "source_ids": ids, "information_needed": needed})
	return string(data)
}

// 同一官方网页可在补搜中再次出现；正文不能被第二次更短的摘要覆盖，实际读取总数也不能因补搜重置。
func TestWebResearchReadsModelSelectedPagesAndKeepsFiveCallThreePageBudget(t *testing.T) {
	first := researchSource("first", "搜索摘要只有温度。")
	second, third := researchSource("second", "候选二。"), researchSource("third", "候选三。")
	provider := &researchSearchProvider{results: [][]Source{{first}, {researchSource("first#again", "错误替代摘要。"), second, third}}}
	reader := &researchPageReader{pages: map[string]WebPageContent{
		first.URL:  {Text: "2026年11月2日08时发布。北京气温17℃。", Title: "北京官方预报", URL: first.URL, ReadAt: time.Now().UTC()},
		second.URL: {Text: "上海的无关资料。", URL: second.URL},
		third.URL:  {Text: "北京2026年11月2日天气晴，西南风。", URL: third.URL},
	}}
	needed := "需要北京所问日期的天气现象、气温和预报时间"
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"北京 2026年11月2日 天气"}`},
		{content: researchReadAction([]string{"candidate-1"}, needed)},
		{content: researchSearchAction("北京 2026年11月2日 天气现象 官方预报")},
		{content: researchReadAction([]string{"candidate-2", "candidate-3"}, needed)},
		{content: researchAnswerAction("北京17℃、晴，西南风。[1][3]", []webSearchEvidence{{SourceID: "candidate-1", Quotes: []string{"2026年11月2日08时发布。", "北京气温17℃。"}}, {SourceID: "candidate-3", Quote: "北京2026年11月2日天气晴，西南风。"}}, nil)},
	}, answer: "北京17℃、晴，西南风。[1][3]"}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Reader: reader, Usage: usage})
	var events []ConversationEvent
	if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("联网查北京天气"), func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(reader.urls) != 3 || len(provider.queries) != 2 || len(model.requests) != 5 {
		t.Fatalf("bounded reading failed: reads=%v searches=%v models=%d", reader.urls, provider.queries, len(model.requests))
	}
	sources := researchSourcesBeforeDone(t, events)
	if len(sources) != 2 || sources[0].Note != "已读取网页正文。" || sources[0].Label != "北京官方预报" || sources[0].Excerpt != "2026年11月2日08时发布。\n\n北京气温17℃。" {
		t.Fatalf("read text or multi-part evidence lost: %+v", sources)
	}
	var assessment struct {
		Candidates []Source `json:"candidates"`
		Needed     string   `json:"information_needed"`
	}
	stateContent := model.requests[4].Messages[1].Content
	stateContent = stateContent[strings.IndexByte(stateContent, '{'):]
	if err := json.Unmarshal([]byte(stateContent), &assessment); err != nil {
		t.Fatal(err)
	}
	if assessment.Needed != needed || len(assessment.Candidates) != 3 || strings.Contains(assessment.Candidates[0].Excerpt, "错误替代") || !strings.Contains(assessment.Candidates[0].Excerpt, "08时发布") {
		t.Fatalf("refinement lost coverage or overwrote body: %+v", assessment)
	}
	fetches := 0
	for _, event := range events {
		if event.ToolName == "web_fetch" && event.Type == "tool_started" {
			fetches++
		}
	}
	if fetches != 3 {
		t.Fatalf("actual fetch progress missing: %d", fetches)
	}
	assertResearchUsageSteps(t, usage, events, 5, 75)
}

func TestWebResearchReaderFailureFallsBackToAccurateSnippetAndMissingCitationsKeepSources(t *testing.T) {
	source := researchSource("official", "北京当日预报气温17℃。")
	provider := &researchSearchProvider{results: [][]Source{{source}}}
	reader := &researchPageReader{err: errors.New("private diagnostic must not escape")}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"北京所问日期天气"}`},
		{content: researchReadAction([]string{"candidate-1"}, "需要日期相符的天气预报")},
		{content: researchAnswerAction("搜索摘要提供的气温为17℃。", []webSearchEvidence{{SourceID: "candidate-1", Quote: source.Excerpt}}, nil)},
	}, answer: "搜索摘要提供的气温为17℃。"}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Reader: reader})
	var events []ConversationEvent
	if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("北京天气"), func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	sources := researchSourcesBeforeDone(t, events)
	if len(sources) != 1 || sources[0].Excerpt != source.Excerpt || sources[0].Note != "网页正文未能读取，仅使用搜索摘要。回答未逐句标注，以下为已核实资料。" {
		t.Fatalf("missing citation or failed fetch hid the evidence boundary: %+v", sources)
	}
	for _, event := range events {
		raw, _ := json.Marshal(event)
		if strings.Contains(string(raw), "private diagnostic") {
			t.Fatal("reader diagnostic escaped")
		}
	}
}

func TestWebResearchRejectsInventedPageSelectionBeforeAnyFetch(t *testing.T) {
	source := researchSource("one", "摘要")
	provider := &researchSearchProvider{results: [][]Source{{source}}}
	reader := &researchPageReader{}
	model := &researchScriptModel{replies: []researchModelReply{{content: `{"action":"search","query":"公开资料"}`}, {content: researchReadAction([]string{"https://invented.example/"}, "正文资料")}}}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Reader: reader, Usage: usage})
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("公开资料"), nil)
	requireToolError(t, err, "invalid_web_search_action")
	if len(reader.urls) != 0 || model.streamCalls != 2 || len(usage.events) != 2 {
		t.Fatalf("invalid selection reached reader or lost incurred usage: reader=%v models=%d usage=%d", reader.urls, model.streamCalls, len(usage.events))
	}
}

func TestWebResearchReadCancellationStopsAssessmentAndSources(t *testing.T) {
	ctx, cancel := context.WithCancel(toolTestContext(t, "school_admin"))
	defer cancel()
	source := researchSource("one", "摘要")
	reader := &researchPageReader{onRead: func(context.Context) { cancel() }}
	model := &researchScriptModel{replies: []researchModelReply{{content: `{"action":"search","query":"公开资料"}`}, {content: researchReadAction([]string{"candidate-1"}, "正文资料")}}}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: &researchSearchProvider{results: [][]Source{{source}}}, Reader: reader})
	err := service.Converse(ctx, researchConversationInput("公开资料"), func(event ConversationEvent) error {
		if event.Type == "done" || event.Type == "sources" {
			t.Error("cancelled reading published sources")
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || len(model.requests) != 2 || model.streamCalls != 2 || len(reader.urls) != 1 {
		t.Fatalf("cancelled read continued: err=%v calls=%d reads=%v", err, len(model.requests), reader.urls)
	}
}

func TestDecodeWebReadActionRequiresOnlyKnownUniqueCandidateIDs(t *testing.T) {
	candidates := []Source{{ID: "candidate-1"}, {ID: "candidate-2"}}
	for _, content := range []string{
		`{"source_ids":null,"information_needed":"正文"}`,
		`{"source_ids":[],"information_needed":null}`,
		`{"source_ids":[],"information_needed":""}`,
		`{"source_ids":[],"Information_Needed":"正文"}`,
		`{"source_ids":["candidate-1","candidate-1"],"information_needed":"正文"}`,
		`{"source_ids":["candidate-1","candidate-2"],"information_needed":"正文"}`,
		`{"source_ids":["candidate-9"],"information_needed":"正文"}`,
		`{"source_ids":[],"information_needed":"正文","extra":true}`,
		`{"source_ids":[],"source_ids":["candidate-1"],"information_needed":"正文"}`,
	} {
		content = `{"action":"read",` + strings.TrimPrefix(content, "{")
		_, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: content}}, candidates, 1)
		requireToolError(t, err, "invalid_web_search_action")
	}
	selection, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: researchReadAction([]string{"candidate-1"}, "需正文")}}, candidates, 1)
	if err != nil || len(selection.SourceIDs) != 1 {
		t.Fatalf("valid source selection rejected: %+v %v", selection, err)
	}
}

func TestDecodeWebSearchActionAcceptsBoundedMultipleActualPassages(t *testing.T) {
	candidates := []Source{{ID: "candidate-1", Excerpt: "2026年11月2日08时发布。其他段落。北京晴，气温17℃。"}}
	evidence := webSearchEvidence{SourceID: "candidate-1", Quotes: []string{"2026年11月2日08时发布。", "北京晴，气温17℃。"}}
	assessment, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: researchAnswerAction("北京天气已核实。[1]", []webSearchEvidence{evidence}, nil)}}, candidates, 3)
	if err != nil || len(assessment.Evidence) != 1 {
		t.Fatalf("bounded distinct passages rejected: %+v %v", assessment, err)
	}
	for _, quotes := range [][]string{{"北京晴，气温17℃。", "北京晴，气温17℃。"}, {"北京晴，气温17℃。", "虚构文本"}, {"1", "2", "3", "4", "5"}, {strings.Repeat("气", 2001)}} {
		evidence.Quotes = quotes
		_, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: researchAnswerAction("北京天气已核实。[1]", []webSearchEvidence{evidence}, nil)}}, candidates, 3)
		requireToolError(t, err, "invalid_web_search_action")
	}
}

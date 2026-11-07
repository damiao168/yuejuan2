package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type researchModelReply struct {
	content string
	calls   []modelgovernance.AgentToolCall
	usage   modelgovernance.ManagedAPIProbeUsage
	err     error
}

func researchIsActionRequest(request modelgovernance.AgentRequest) bool {
	return len(request.Messages) > 0 && strings.Contains(request.Messages[0].Content, webSearchActionInstructions)
}

func researchActionCandidates(request modelgovernance.AgentRequest) []Source {
	var candidates []Source
	for _, message := range request.Messages {
		var state struct {
			Candidates []Source `json:"candidates"`
		}
		content := message.Content
		if start := strings.IndexByte(content, '{'); start >= 0 {
			content = content[start:]
		}
		if json.Unmarshal([]byte(content), &state) == nil && state.Candidates != nil {
			candidates = state.Candidates
		}
	}
	return candidates
}

func researchAnswerAction(answer string, evidence []webSearchEvidence, missing []string) string {
	if evidence == nil {
		evidence = []webSearchEvidence{}
	}
	if missing == nil {
		missing = []string{}
	}
	covered := []map[string]any{}
	for _, item := range evidence {
		covered = append(covered, map[string]any{"claim": "已核实的公开事实 " + item.SourceID, "source_ids": []string{item.SourceID}})
	}
	data, _ := json.Marshal(map[string]any{"action": "answer", "answer": answer, "covered": covered, "missing": missing, "evidence": evidence})
	return string(data)
}

// 简单用例沿用query作为完整公开问题；畸形脚本必须原样交给服务端，不能在夹具中修好重复或未知字段。
func researchFirstSearchAction(content string) string {
	var fields map[string]json.RawMessage
	if decodeStrictWebSearchObject(content, &fields) != nil || string(fields["action"]) != `"search"` || fields["question"] != nil {
		return content
	}
	for name := range fields {
		if name != "action" && name != "query" && name != "information_needed" {
			return content
		}
	}
	if fields["query"] == nil {
		return content
	}
	fields["question"] = fields["query"]
	data, _ := json.Marshal(fields)
	return string(data)
}

func researchSearchAction(query string) string {
	data, _ := json.Marshal(map[string]string{"action": "search", "query": query})
	return string(data)
}

// 脚本只决定模型返回的协议内容；真实工具、来源筛选、预算和取消仍由服务执行，不代表外部模型的判断质量。
type researchScriptModel struct {
	replies       []researchModelReply
	nextReply     int
	requests      []modelgovernance.AgentRequest
	answerRequest modelgovernance.AgentRequest
	answer        string
	reasoning     string
	answerErr     error
	streamCalls   int
	probeCalls    int
	onComplete    func(context.Context, int)
}

func (m *researchScriptModel) Complete(ctx context.Context, _ modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	m.requests = append(m.requests, request)
	if m.onComplete != nil {
		m.onComplete(ctx, len(m.requests)-1)
	}
	usage := modelgovernance.ManagedAPIProbeUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	if len(request.Tools) == 1 && request.Tools[0].Name == "check_tool_support" {
		m.probeCalls++
		marker := strings.TrimPrefix(request.Messages[1].Content, "marker=")
		arguments, _ := json.Marshal(map[string]string{"marker": marker})
		return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", ToolCalls: []modelgovernance.AgentToolCall{{ID: "research-probe", Name: "check_tool_support", Arguments: arguments}}}, Usage: usage}, nil
	}
	if m.nextReply >= len(m.replies) {
		return modelgovernance.AgentResponse{Usage: usage}, errors.New("unexpected research model call")
	}
	reply := m.replies[m.nextReply]
	m.nextReply++
	if reply.usage.TotalTokens != 0 || reply.usage.InputTokens != 0 || reply.usage.OutputTokens != 0 {
		usage = reply.usage
	}
	return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: reply.content, ToolCalls: reply.calls}, Usage: usage}, reply.err
}

func (m *researchScriptModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	if researchIsActionRequest(request) {
		m.streamCalls++
		m.answerRequest = request
		response, err := m.Complete(ctx, connection, request)
		if m.nextReply == 1 {
			response.Message.Content = researchFirstSearchAction(response.Message.Content)
		}
		response.FinishReason = "stop"
		if err == nil {
			err = emit(modelgovernance.AgentEvent{Type: "content", Delta: response.Message.Content})
		}
		return response, err
	}
	m.streamCalls++
	m.answerRequest = request
	answer := m.answer
	if answer == "" {
		answer = "本次尚未核实所问事实。"
	}
	response := modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: answer, ReasoningContent: m.reasoning}, FinishReason: "stop", Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25}}
	if m.answerErr != nil {
		return response, m.answerErr
	}
	if m.reasoning != "" {
		if err := emit(modelgovernance.AgentEvent{Type: "reasoning", Delta: m.reasoning}); err != nil {
			return response, err
		}
	}
	if err := emit(modelgovernance.AgentEvent{Type: "content", Delta: answer}); err != nil {
		return response, err
	}
	return response, nil
}

type researchSearchProvider struct {
	results  [][]Source
	queries  []string
	onSearch func(context.Context, int)
}

func (s *researchSearchProvider) Search(ctx context.Context, query string) ([]Source, error) {
	index := len(s.queries)
	s.queries = append(s.queries, query)
	if s.onSearch != nil {
		s.onSearch(ctx, index)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if index >= len(s.results) {
		return nil, errors.New("unexpected additional public search")
	}
	return s.results[index], nil
}

func researchSource(path, excerpt string) Source {
	return Source{ID: "provider-id", Kind: "web", Status: "available", Label: path, URL: "https://weather.example.com/" + path, Excerpt: excerpt, ReadAt: time.Now().UTC()}
}

func researchConversationInput(question string) ConversationRequest {
	input := conversationInput(question)
	input.WebSearch = true
	return input
}

func researchSourcesBeforeDone(t *testing.T, events []ConversationEvent) []Source {
	t.Helper()
	sourceIndex, doneIndex, count := -1, -1, 0
	var sources []Source
	for index, event := range events {
		if event.Type == "sources" {
			sourceIndex, sources = index, event.Sources
			count++
		}
		if event.Type == "done" {
			doneIndex = index
		}
	}
	if count != 1 || doneIndex < 1 || sourceIndex != doneIndex-1 {
		t.Fatalf("sources were exposed before final selection: %+v", events)
	}
	return sources
}

func assertResearchUsageSteps(t *testing.T, usage *conversationUsage, events []ConversationEvent, wantCalls, wantTokens int) {
	t.Helper()
	if len(usage.events) != wantCalls {
		t.Fatalf("wrong model call accounting: %+v", usage.events)
	}
	var total int64
	for index, event := range usage.events {
		total += event.TotalTokens
		if event.Metadata["step"] != index+1 || event.Metadata["turn_request_id"] != events[0].RequestID {
			t.Fatalf("model steps or request identity drifted: %+v", usage.events)
		}
	}
	last := events[len(events)-1]
	if total != int64(wantTokens) || last.Type != "done" || last.Completion.Usage.TotalTokens != int64(wantTokens) {
		t.Fatalf("completion lost research usage: total=%d last=%+v", total, last)
	}
}

func TestWebResearchDisplaysOnlySupportedSourcesActuallyCitedByAnswer(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{{
		researchSource("wrong-city", "上海2026年11月1日有雨。"),
		researchSource("old-date", "北京2025年11月1日晴。"),
		researchSource("current-a", "北京2026年11月1日预报有雨。未采用的旁支摘要A。"),
		researchSource("current-b", "北京2026年11月1日预报最高温12度。未采用的旁支摘要B。"),
	}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"北京 2026年11月1日 天气"}`},
		{content: researchAnswerAction("北京所问日期的预报最高温为12度。[4]", []webSearchEvidence{{SourceID: "candidate-3", Quote: "北京2026年11月1日预报有雨。"}, {SourceID: "candidate-4", Quote: "北京2026年11月1日预报最高温12度。"}}, nil)},
	}, answer: "北京所问日期的预报最高温为12度。[4]", reasoning: "未采用的推理引用[1]"}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
	input := researchConversationInput("告诉我北京天气结果")
	input.Messages = append([]modelgovernance.ManagedChatMessage{{Role: "user", Content: "仅用于指代的历史私有标记"}, {Role: "assistant", Content: "请问具体主题？"}}, input.Messages...)
	var events []ConversationEvent
	if err := service.Converse(toolTestContext(t, "school_admin"), input, func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	sources := researchSourcesBeforeDone(t, events)
	if len(sources) != 1 || sources[0].ID != "web-4" || !strings.HasSuffix(sources[0].URL, "/current-b") || sources[0].Excerpt != "北京2026年11月1日预报最高温12度。" {
		t.Fatalf("unsupported, unused or renumbered source displayed: %+v", sources)
	}
	assessmentRequest, _ := json.Marshal(model.requests[1])
	if len(model.requests[1].Messages) != 2 || strings.Contains(string(assessmentRequest), "历史私有标记") || len(model.requests[1].Tools) != 0 || model.requests[1].ToolChoice != "none" {
		t.Fatalf("assessment received chat history or tools: %s", assessmentRequest)
	}
	// 统一决策轮需要看候选才能筛选；用户可见来源仍只能包含答案实际引用的逐字证据。
	if strings.Contains(sources[0].Excerpt, "未采用的旁支摘要") {
		t.Fatal("unselected page text escaped into evidence")
	}
	assertResearchUsageSteps(t, usage, events, 2, 30)
}

func TestWebResearchRefinesOnceAndDeduplicatesURLsAcrossAttempts(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{
		{researchSource("same#first", "首次摘要，日期不足。")},
		{researchSource("same#second", "重复网页不能产生新候选。"), researchSource("updated", "北京2026年11月1日08时预报有雨。")},
	}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"北京 2026年11月1日 天气"}`},
		{content: researchSearchAction("北京 2026年11月1日 08时 官方天气预报")},
		{content: researchAnswerAction("北京所问时段预报有雨。[2]", []webSearchEvidence{{SourceID: "candidate-2", Quote: "北京2026年11月1日08时预报有雨。"}}, nil)},
	}, answer: "北京所问时段预报有雨。[2]"}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
	var events []ConversationEvent
	if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("北京当天天气如何？"), func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(provider.queries) != 2 || provider.queries[0] == provider.queries[1] {
		t.Fatalf("expected one specific refinement: %v", provider.queries)
	}
	var input struct {
		Query      string   `json:"question"`
		Candidates []Source `json:"candidates"`
	}
	stateContent := model.requests[2].Messages[1].Content
	stateContent = stateContent[strings.IndexByte(stateContent, '{'):]
	if err := json.Unmarshal([]byte(stateContent), &input); err != nil {
		t.Fatal(err)
	}
	if input.Query != provider.queries[0] || len(input.Candidates) != 2 || input.Candidates[0].ID != "candidate-1" || input.Candidates[1].ID != "candidate-2" || strings.Contains(input.Candidates[0].URL, "#") || input.Candidates[0].Excerpt != "首次摘要，日期不足。\n\n重复网页不能产生新候选。" {
		t.Fatalf("refinement changed original question or duplicate candidate identities: %+v", input)
	}
	sources := researchSourcesBeforeDone(t, events)
	if len(sources) != 1 || sources[0].ID != "web-2" || !strings.HasSuffix(sources[0].URL, "/updated") {
		t.Fatalf("refined source was not used: %+v", sources)
	}
	assertResearchUsageSteps(t, usage, events, 3, 45)
}

func TestWebResearchStopsAfterSecondInsufficientSearchWithoutSources(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{{researchSource("old", "这是旧日期的资料。")}, {}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"北京当天实时天气"}`},
		{content: researchSearchAction("北京当天逐小时预报")},
		{content: researchAnswerAction("这次摘要没有匹配日期的预报时间，尚未核实当前天气。", nil, []string{"尚无匹配日期的预报"})},
	}, answer: "这次摘要没有匹配日期的预报时间，尚未核实当前天气。"}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
	var events []ConversationEvent
	if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("北京实时天气"), func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(provider.queries) != 2 || model.streamCalls != 3 || len(researchSourcesBeforeDone(t, events)) != 0 {
		t.Fatalf("insufficient results caused extra searches or invented sources: queries=%v events=%+v", provider.queries, events)
	}
	transcript, _ := json.Marshal(model.answerRequest.Messages)
	if !strings.Contains(string(transcript), `\"allow_search\":false`) {
		t.Fatalf("exhausted search remained available: %s", transcript)
	}
	assertResearchUsageSteps(t, usage, events, 3, 45)
}

func TestWebResearchDoesNotRepeatEquivalentRefinementQuery(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{{researchSource("partial", "部分公开事实。")}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"Beijing weather"}`},
		{content: researchSearchAction(" BEIJING   weather ")},
		{content: researchAnswerAction("只核实到部分公开事实。[1]", []webSearchEvidence{{SourceID: "candidate-1", Quote: "部分公开事实。"}}, []string{"其他天气内容未核实"})},
	}, answer: "只核实到部分公开事实。[1]"}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider})
	if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("北京天气"), nil); err != nil {
		t.Fatal(err)
	}
	if len(provider.queries) != 1 || len(model.requests) != 3 {
		t.Fatalf("equivalent refinement repeated search: queries=%v requests=%d", provider.queries, len(model.requests))
	}
}

func TestWebResearchDirectAnswerAndClarificationWorkWithoutSearchConfigurationOrSources(t *testing.T) {
	for _, action := range []string{"answer", "clarify"} {
		t.Run(action, func(t *testing.T) {
			plan, _ := json.Marshal(map[string]string{"action": action, "answer": "请说明你想了解的主题。"})
			if action == "answer" {
				plan = []byte(researchAnswerAction("请说明你想了解的主题。", nil, nil))
			}
			model := &researchScriptModel{replies: []researchModelReply{{content: string(plan)}}, answer: "请说明你想了解的主题。"}
			usage := &conversationUsage{}
			service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Usage: usage})
			var events []ConversationEvent
			if err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("告诉我结果"), func(event ConversationEvent) error {
				events = append(events, event)
				if event.Type == "sources" && len(event.Sources) > 0 || strings.HasPrefix(event.Type, "tool_") {
					t.Errorf("direct reply emitted search provenance: %+v", event)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(model.requests) != 1 || model.streamCalls != 1 {
				t.Fatalf("direct reply unexpectedly entered research: %+v", model)
			}
			assertResearchUsageSteps(t, usage, events, 1, 15)
		})
	}
}

func TestWebResearchDirectCodeExplanationDoesNotTreatArrayIndexAsWebCitation(t *testing.T) {
	answer := "a[1] 表示读取数组中索引为 1 的元素。"
	model := &researchScriptModel{replies: []researchModelReply{{content: researchAnswerAction(answer, nil, nil)}}, answer: answer}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model})
	done := false
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("解释 a[1] 的含义"), func(event ConversationEvent) error {
		if event.Type == "sources" && len(event.Sources) > 0 {
			t.Error("direct explanation must not produce web sources")
		}
		if event.Type == "done" {
			done = event.Completion.Message.Content == answer
		}
		return nil
	})
	if err != nil || !done {
		t.Fatalf("valid array index was rejected as a citation: done=%v err=%v", done, err)
	}
}

func TestDecodeWebSearchActionRejectsMalformedOrUnprovenEvidence(t *testing.T) {
	candidates := []Source{{ID: "candidate-1", Label: "只有标题含有的事实", Excerpt: "摘要中真实存在的公开事实。"}}
	for index := 2; index <= 6; index++ {
		candidates = append(candidates, Source{ID: fmt.Sprintf("candidate-%d", index), Excerpt: "另一条实际摘要。"})
	}
	tooMany := []webSearchEvidence{}
	for index, source := range candidates {
		quote := "另一条实际摘要。"
		if index == 0 {
			quote = source.Excerpt
		}
		tooMany = append(tooMany, webSearchEvidence{SourceID: source.ID, Quote: quote})
	}
	valid := researchAnswerAction("已核实的事实。[1]", []webSearchEvidence{{SourceID: "candidate-1", Quote: "真实存在的公开事实"}}, nil)
	for _, test := range []struct{ name, content string }{
		{"missing evidence", `{"action":"answer","answer":"已核实","covered":[],"missing":[]}`},
		{"null evidence", `{"action":"answer","answer":"已核实","covered":[],"missing":[],"evidence":null}`},
		{"duplicate evidence field", strings.Replace(valid, `"source_id":"candidate-1"`, `"source_id":"candidate-2","source_id":"candidate-1"`, 1)},
		{"unknown nested field", strings.Replace(valid, `"quote":`, `"extra":1,"quote":`, 1)},
		{"case changed evidence field", strings.Replace(valid, `"source_id":`, `"Source_ID":`, 1)},
		{"invented source", researchAnswerAction("已核实", []webSearchEvidence{{SourceID: "candidate-99", Quote: "公开事实"}}, nil)},
		{"duplicate source", researchAnswerAction("已核实", []webSearchEvidence{{SourceID: "candidate-1", Quote: "公开事实"}, {SourceID: "candidate-1", Quote: "公开事实"}}, nil)},
		{"title only", researchAnswerAction("已核实", []webSearchEvidence{{SourceID: "candidate-1", Quote: "只有标题含有的事实"}}, nil)},
		{"rewritten quote", researchAnswerAction("已核实", []webSearchEvidence{{SourceID: "candidate-1", Quote: "模型改写的新事实"}}, nil)},
		{"empty quote", researchAnswerAction("已核实", []webSearchEvidence{{SourceID: "candidate-1", Quote: " "}}, nil)},
		{"too many sources", researchAnswerAction("已核实", tooMany, nil)},
		{"mixed search and answer", strings.Replace(valid, `"action":"answer"`, `"action":"answer","query":"不应继续搜"`, 1)},
		{"long query", researchSearchAction(strings.Repeat("中", webSearchMaxQueryRunes+1))},
		{"trailing object", valid + `{}`},
		{"markdown", "```json\n" + valid + "\n```"},
		{"array", `[]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: test.content}}, candidates, 3)
			requireToolError(t, err, "invalid_web_search_action")
		})
	}
	if action, err := decodeWebSearchAction(modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Content: valid}}, candidates, 3); err != nil || action.Action != "answer" || len(action.Evidence) != 1 {
		t.Fatalf("actual excerpt evidence rejected: %+v %v", action, err)
	}
}

func TestWebResearchInvalidActionEvidenceNeverRefinesOrEmitsSources(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{{researchSource("candidate", "实际摘要。")}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"公开问题"}`},
		{content: researchAnswerAction("不应成功的回答。", []webSearchEvidence{{SourceID: "invented", Quote: "实际摘要。"}}, nil)},
	}}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
	var events []ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("公开问题"), func(event ConversationEvent) error { events = append(events, event); return nil })
	requireToolError(t, err, "invalid_web_search_action")
	if len(provider.queries) != 1 || model.streamCalls != 2 || len(usage.events) != 2 {
		t.Fatalf("invalid evidence continued or lost incurred usage: queries=%v model=%+v usage=%+v", provider.queries, model, usage.events)
	}
	for _, event := range events {
		if event.Type == "sources" || event.Type == "done" {
			t.Fatalf("invalid evidence became visible provenance: %+v", event)
		}
	}
}

func TestWebResearchActionBudgetFailureAndCancellationStopNextCall(t *testing.T) {
	for _, name := range []string{"budget", "model error", "cancel assessment", "cancel second search"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(toolTestContext(t, "school_admin"))
			defer cancel()
			provider := &researchSearchProvider{results: [][]Source{{researchSource("candidate", "时间不足的摘要。")}}}
			model := &researchScriptModel{replies: []researchModelReply{
				{content: `{"action":"search","query":"原始公开问题"}`},
				{content: researchSearchAction("具体公开问题"), usage: modelgovernance.ManagedAPIProbeUsage{TotalTokens: 17}},
			}}
			wantErr := error(context.Canceled)
			switch name {
			case "budget":
				model.replies[1].usage.TotalTokens = conversationMaxTokens - 15
				wantErr = errConversationBudget
			case "model error":
				wantErr = errors.New("assessment unavailable")
				model.replies[1].err = wantErr
			case "cancel assessment":
				model.onComplete = func(_ context.Context, index int) {
					if index == 1 {
						cancel()
					}
				}
			}
			usage := &conversationUsage{}
			service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
			err := service.Converse(ctx, researchConversationInput("公开问题"), func(event ConversationEvent) error {
				if name == "cancel second search" && event.Type == "tool_started" && event.CallID == "web-search-2" {
					cancel()
				}
				if event.Type == "sources" || event.Type == "done" {
					t.Errorf("stopped research emitted final evidence: %+v", event)
				}
				return nil
			})
			if !errors.Is(err, wantErr) || len(provider.queries) != 1 || model.streamCalls != 2 || len(model.requests) != 2 || len(usage.events) != 2 || usage.events[0].Metadata["step"] != 1 || usage.events[1].Metadata["step"] != 2 || usage.events[1].TotalTokens != model.replies[1].usage.TotalTokens {
				t.Fatalf("research stop failed or lost usage: err=%v queries=%v usage=%+v", err, provider.queries, usage.events)
			}
			if name == "model error" && usage.events[1].Status != "failed" {
				t.Fatalf("failed assessment was recorded as success: %+v", usage.events[1])
			}
		})
	}
}

func TestWebResearchUnknownFinalCitationDoesNotPublishSourcesOrCompletion(t *testing.T) {
	provider := &researchSearchProvider{results: [][]Source{{researchSource("one", "实际支持片段。")}}}
	model := &researchScriptModel{replies: []researchModelReply{
		{content: `{"action":"search","query":"公开问题"}`},
		{content: researchAnswerAction("伪造来源编号。[99]", []webSearchEvidence{{SourceID: "candidate-1", Quote: "实际支持片段。"}}, nil)},
	}, answer: "伪造来源编号。[99]"}
	usage := &conversationUsage{}
	service := NewConversationService(NewService(Dependencies{}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider, Usage: usage})
	err := service.Converse(toolTestContext(t, "school_admin"), researchConversationInput("公开问题"), func(event ConversationEvent) error {
		if event.Type == "sources" || event.Type == "done" {
			t.Errorf("unknown citation acquired visible provenance: %+v", event)
		}
		return nil
	})
	requireToolError(t, err, "invalid_web_search_citation")
	if len(usage.events) != 2 || usage.events[1].Metadata["step"] != 2 || usage.events[1].TotalTokens != 15 {
		t.Fatalf("invalid final citation lost model usage: %+v", usage.events)
	}
}

func TestWebResearchBusinessUsesExistingReadOnlyToolsWithoutPublicSearch(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("configured=%t", configured), func(t *testing.T) {
			item := toolTestExamItem(toolTestExam, time.Now())
			reader := &toolTestExams{items: []exam.Exam{item}}
			workspace := &toolTestWorkspace{}
			tools := NewService(Dependencies{Exams: reader, Workspaces: workspace, Resources: toolTestResourceStore(reader.items)})
			provider := &researchSearchProvider{}
			var search SearchProvider
			if configured {
				search = provider
			}
			model := &researchScriptModel{replies: []researchModelReply{
				{content: `{"action":"business"}`},
				{calls: []modelgovernance.AgentToolCall{conversationCall("find-exam", ToolSearchExams, `{"subject":"数学"}`)}},
				{calls: []modelgovernance.AgentToolCall{conversationCall("exam-progress", ToolGetExamProgress, `{"exam_id":"`+toolTestExam+`"}`)}},
				{content: "依据本轮工作区解释进度。"},
			}, answer: "已读取本轮校内工作区，进度按实际资料解释。"}
			usage := &conversationUsage{}
			service := NewConversationService(tools, ConversationDependencies{Models: conversationStore(), Model: model, Search: search, Usage: usage})
			var events []ConversationEvent
			if err := service.Converse(toolTestContext(t, "school_admin", "exam:manage", "dashboard:read"), researchConversationInput("找数学月考并告诉我进度"), func(event ConversationEvent) error { events = append(events, event); return nil }); err != nil {
				t.Fatal(err)
			}
			if len(provider.queries) != 0 || reader.listCalls != 1 || reader.getCalls != 1 || workspace.calls != 1 || model.probeCalls != 1 || model.streamCalls != 2 {
				t.Fatalf("business route skipped real tools or called public search: reads=%+v workspace=%+v model=%+v queries=%v", reader, workspace, model, provider.queries)
			}
			markerSeen := false
			for _, event := range events {
				if event.ToolName == "web_search" {
					t.Fatalf("business branch exposed public search: %+v", event)
				}
				if event.ToolName == "school_business" && event.Type == "tool_started" {
					markerSeen = true
				}
				for _, source := range event.Sources {
					if source.Kind == "web" {
						t.Fatalf("school facts mixed with public provenance: %+v", source)
					}
				}
			}
			if !markerSeen || !strings.Contains(model.answerRequest.Messages[0].Content, "本轮只处理校内问题") || len(model.answerRequest.Tools) != 0 || model.answerRequest.ToolChoice != "none" {
				t.Fatalf("business route retained public instructions or lost marker: %+v", model.answerRequest)
			}
			assertResearchUsageSteps(t, usage, events, 6, 100)
		})
	}
}

func TestWebResearchBusinessRechecksForeignContextBeforeAnyToolOrPublicSearch(t *testing.T) {
	reader := &toolTestExams{}
	provider := &researchSearchProvider{}
	model := &researchScriptModel{replies: []researchModelReply{{content: `{"action":"business"}`}}}
	service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model, Search: provider})
	input := researchConversationInput("查当前考试进度")
	input.Context = &ConversationContext{ExamID: toolTestForeign}
	err := service.Converse(toolTestContext(t, "school_admin", "exam:manage", "dashboard:read"), input, nil)
	requireToolError(t, err, "exam_not_found")
	if len(provider.queries) != 0 || model.probeCalls != 0 || model.streamCalls != 1 || reader.getCalls != 1 || reader.listCalls != 0 {
		t.Fatalf("foreign business context bypassed authorization or reached public search: %+v %+v", reader, model)
	}
}

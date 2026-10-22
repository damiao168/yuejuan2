package schoolassistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

type conversationModelStore struct {
	modelgovernance.ManagedAPIConfigStore
	connection modelgovernance.ManagedAPIConnection
}

func (s *conversationModelStore) ListManagedAPIConfigs(_ context.Context, tenantID string) ([]modelgovernance.ManagedAPIConfig, error) {
	if tenantID != s.connection.Config.TenantID {
		return nil, errors.New("wrong tenant")
	}
	return []modelgovernance.ManagedAPIConfig{s.connection.Config}, nil
}
func (s *conversationModelStore) GetManagedAPIConnection(_ context.Context, tenantID, id string) (modelgovernance.ManagedAPIConnection, error) {
	if tenantID != s.connection.Config.TenantID || id != s.connection.Config.ID {
		return modelgovernance.ManagedAPIConnection{}, errors.New("wrong model scope")
	}
	return s.connection, nil
}

type conversationUsage struct {
	events []modelgovernance.ModelUsageEvent
}

func (u *conversationUsage) RecordModelUsage(_ context.Context, event modelgovernance.ModelUsageEvent) error {
	u.events = append(u.events, event)
	return nil
}

// 脚本模型只模拟协议，业务事实必须来自真实工具执行层；这些测试不代表外部模型回答质量。
type conversationScriptModel struct {
	plans         [][]modelgovernance.AgentToolCall
	requests      []modelgovernance.AgentRequest
	answer        string
	answerRequest modelgovernance.AgentRequest
	unsupported   bool
	probeCalls    int
	streamCalls   int
}

func (m *conversationScriptModel) Complete(_ context.Context, _ modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	usage := modelgovernance.ManagedAPIProbeUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	if len(request.Tools) == 1 && request.Tools[0].Name == "check_tool_support" {
		m.probeCalls++
		if m.unsupported {
			return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: "我只能输出文本"}, Usage: usage}, nil
		}
		marker := strings.TrimPrefix(request.Messages[1].Content, "marker=")
		args, _ := json.Marshal(map[string]string{"marker": marker})
		return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", ToolCalls: []modelgovernance.AgentToolCall{{ID: "probe", Name: "check_tool_support", Arguments: args}}}, Usage: usage}, nil
	}
	index := len(m.requests)
	m.requests = append(m.requests, request)
	message := modelgovernance.AgentMessage{Role: "assistant", Content: "依据已核实数据回答；不明确时追问。"}
	if index < len(m.plans) && len(m.plans[index]) > 0 {
		message.Content = ""
		message.ToolCalls = m.plans[index]
	}
	return modelgovernance.AgentResponse{Message: message, Usage: usage}, nil
}
func (m *conversationScriptModel) Stream(_ context.Context, _ modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	m.streamCalls++
	m.answerRequest = request
	answer := m.answer
	if answer == "" {
		answer = "来源已核实，请按提示处理。"
	}
	if err := emit(modelgovernance.AgentEvent{Type: "content", Delta: answer}); err != nil {
		return modelgovernance.AgentResponse{}, err
	}
	return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: answer}, FinishReason: "stop", Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25}}, nil
}

func conversationStore() *conversationModelStore {
	return &conversationModelStore{connection: modelgovernance.ManagedAPIConnection{Config: modelgovernance.ManagedAPIConfig{
		ID: "99999999-9999-4999-8999-999999999999", TenantID: toolTestTenant, IsDefault: true, Status: "active",
		ModelName: "test-model", DisplayName: "测试模型", AdapterType: "openai_compatible", LastTextStatus: "success",
		LastCapabilityStatus: "success", LastCapabilityVersion: modelgovernance.ManagedCapabilityProbeVersion,
	}}}
}
func conversationInput(question string) ConversationRequest {
	return ConversationRequest{Messages: []modelgovernance.ManagedChatMessage{{Role: "user", Content: question}}}
}
func conversationCall(id, name, args string) modelgovernance.AgentToolCall {
	return modelgovernance.AgentToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

func TestConversationRequiresIndependentTextEvidenceBeforeModelOrTools(t *testing.T) {
	for _, test := range []struct {
		name              string
		textStatus        string
		capabilityStatus  string
		capabilityVersion string
		wantAvailable     bool
	}{
		{name: "model list only", textStatus: "untested", capabilityStatus: "untested"},
		{name: "legacy structured success", textStatus: "untested", capabilityStatus: "success", capabilityVersion: "structured-json-v3"},
		{name: "text failed despite structured success", textStatus: "failed", capabilityStatus: "success", capabilityVersion: modelgovernance.ManagedCapabilityProbeVersion},
		{name: "text success despite structured failure", textStatus: "success", capabilityStatus: "failed", capabilityVersion: modelgovernance.ManagedCapabilityProbeVersion, wantAvailable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := conversationStore()
			store.connection.Config.LastTestStatus = "success"
			store.connection.Config.LastProbeMode = "quick"
			store.connection.Config.LastTextStatus = test.textStatus
			store.connection.Config.LastCapabilityStatus = test.capabilityStatus
			store.connection.Config.LastCapabilityVersion = test.capabilityVersion
			reader := &toolTestExams{}
			model := &conversationScriptModel{}
			service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: store, Model: model})
			err := service.Converse(toolTestContext(t, "school_admin"), conversationInput("起草考试通知"), nil)
			if test.wantAvailable {
				if err != nil || model.streamCalls != 1 {
					t.Fatalf("text-only general conversation was rejected: err=%v streams=%d", err, model.streamCalls)
				}
				return
			}
			requireToolError(t, err, "ai_chat_model_unavailable")
			if model.probeCalls != 0 || len(model.requests) != 0 || model.streamCalls != 0 || reader.listCalls != 0 || reader.getCalls != 0 {
				t.Fatalf("unverified text reached model or business tools: probe=%d plans=%d streams=%d reads=%+v", model.probeCalls, len(model.requests), model.streamCalls, reader)
			}
		})
	}
}

func TestConversationModelChoosesToolsAndReceivesRealEvidence(t *testing.T) {
	item := toolTestExamItem(toolTestExam, time.Now().UTC())
	reader := &toolTestExams{items: []exam.Exam{item}}
	tools := NewService(Dependencies{Exams: reader, Workspaces: &toolTestWorkspace{}, Resources: toolTestResourceStore(reader.items)})
	model := &conversationScriptModel{plans: [][]modelgovernance.AgentToolCall{
		{conversationCall("search", ToolSearchExams, `{"subject":"数学"}`)},
		{conversationCall("progress", ToolGetExamProgress, `{"exam_id":"`+toolTestExam+`"}`)},
	}}
	usage := &conversationUsage{}
	service := NewConversationService(tools, ConversationDependencies{Models: conversationStore(), Model: model, Usage: usage})
	var events []ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin", "exam:manage", "dashboard:read"), conversationInput("查找数学月考，再告诉我进度"), func(event ConversationEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if reader.listCalls != 1 || reader.getCalls != 1 || model.streamCalls != 1 {
		t.Fatalf("missing actual tool execution: lists=%d gets=%d streams=%d", reader.listCalls, reader.getCalls, model.streamCalls)
	}
	transcript, _ := json.Marshal(model.answerRequest.Messages)
	if !strings.Contains(string(transcript), item.Name) || !strings.Contains(string(transcript), `\"role\":\"tool\"`) && !strings.Contains(string(transcript), `"role":"tool"`) {
		t.Fatalf("model did not receive real tools: %s", transcript)
	}
	if model.answerRequest.ToolChoice != "none" {
		t.Fatal("final answer can issue new tools")
	}
	last := events[len(events)-1]
	if last.Type != "done" || last.Completion == nil || last.Completion.Usage.TotalTokens != 85 {
		t.Fatalf("missing full usage/completion: %+v", last)
	}
	assertConversationUsageAndEventIdentity(t, usage, events, last)
}

func TestConversationDoesNotExecuteUnknownOrForbiddenModelTools(t *testing.T) {
	reader := &toolTestExams{}
	model := &conversationScriptModel{plans: [][]modelgovernance.AgentToolCall{{conversationCall("unknown", "publish_scores", `{}`)}}}
	service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model})
	failed := false
	err := service.Converse(toolTestContext(t, "school_admin", "exam:manage"), conversationInput("发布考试"), func(event ConversationEvent) error {
		if event.Type == "tool_finished" && event.Status == "failed" {
			failed = true
		}
		return nil
	})
	if err != nil || !failed || reader.listCalls != 0 || reader.getCalls != 0 {
		t.Fatalf("unregistered tool reached business: %v %v %+v", err, failed, reader)
	}
	data, _ := json.Marshal(model.answerRequest.Messages)
	if !strings.Contains(string(data), "tool_unavailable") {
		t.Fatal("model was not informed tool is unavailable")
	}
}

func TestConversationVerifiesNativeToolSupportBeforeBusinessReads(t *testing.T) {
	reader := &toolTestExams{}
	model := &conversationScriptModel{unsupported: true}
	service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model})
	err := service.Converse(toolTestContext(t, "school_admin", "exam:manage"), conversationInput("查考试"), nil)
	if !errors.Is(err, modelgovernance.ErrAgentToolsUnsupported) || reader.listCalls != 0 || model.streamCalls != 0 {
		t.Fatalf("silently treated text-only model as agent: %v", err)
	}
	model.unsupported = false
	if err = service.Converse(toolTestContext(t, "school_admin", "exam:manage"), conversationInput("写通知"), nil); err != nil {
		t.Fatal(err)
	}
	if err = service.Converse(toolTestContext(t, "school_admin", "exam:manage"), conversationInput("写通知"), nil); err != nil {
		t.Fatal(err)
	}
	if model.probeCalls != 2 {
		t.Fatalf("failed probe cached, or success not cached: %d", model.probeCalls)
	}
}

func TestConversationCancelOnToolProgressStopsReadsAndAnswer(t *testing.T) {
	reader := &toolTestExams{}
	model := &conversationScriptModel{plans: [][]modelgovernance.AgentToolCall{{conversationCall("search", ToolSearchExams, `{}`)}}}
	service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model})
	ctx, cancel := context.WithCancel(toolTestContext(t, "school_admin", "exam:manage"))
	defer cancel()
	err := service.Converse(ctx, conversationInput("查考试"), func(event ConversationEvent) error {
		if event.Type == "tool_started" {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || reader.listCalls != 0 || model.streamCalls != 0 {
		t.Fatalf("canceled turn continued: %v lists=%d stream=%d", err, reader.listCalls, model.streamCalls)
	}
}

func TestConversationRejectsDuplicateBatchAndLimitsRepeatedPlans(t *testing.T) {
	for _, duplicate := range []bool{true, false} {
		assertConversationPlanBudget(t, duplicate)
	}
}

func TestConversationSourcesClearSingleExamAfterComparisonAndKeepEmptyActions(t *testing.T) {
	items := []exam.Exam{toolTestExamItem(toolTestExam, time.Now().UTC()), toolTestExamItem(toolTestForeign, time.Now().UTC())}
	reader := &toolTestExams{items: items}
	model := &conversationScriptModel{plans: [][]modelgovernance.AgentToolCall{{conversationCall("one", ToolGetExamProgress, `{"exam_id":"`+toolTestExam+`"}`), conversationCall("two", ToolGetExamProgress, `{"exam_id":"`+toolTestForeign+`"}`)}}}
	tools := NewService(Dependencies{Exams: reader, Workspaces: &toolTestWorkspace{err: errors.New("source unavailable")}, Resources: toolTestResourceStore(items)})
	service := NewConversationService(tools, ConversationDependencies{Models: conversationStore(), Model: model})
	var sources []ConversationEvent
	err := service.Converse(toolTestContext(t, "school_admin", "dashboard:read"), conversationInput("比较两场考试"), func(event ConversationEvent) error {
		if event.Type == "sources" {
			sources = append(sources, event)
		}
		return nil
	})
	if err != nil || len(sources) != 2 || sources[0].Scope == nil || sources[1].Scope != nil {
		t.Fatalf("comparison retained wrong exam: %v %+v", err, sources)
	}
	data, _ := json.Marshal(sources[0])
	if !strings.Contains(string(data), `"actions":[]`) {
		t.Fatalf("source event omitted empty actions: %s", data)
	}
	transcript, _ := json.Marshal(model.answerRequest.Messages)
	if !strings.Contains(string(transcript), "unavailable") {
		t.Fatal("source failure disappeared before model answer")
	}
}

func TestConversationHTTPRejectsForgedRolesAndForeignContextBeforeSSE(t *testing.T) {
	foreign := toolTestExamItem(toolTestForeign, time.Now().UTC())
	foreign.SchoolID = "foreign-school"
	model := &conversationScriptModel{}
	handler := NewHandler(Dependencies{Exams: &toolTestExams{items: []exam.Exam{foreign}}, Conversation: ConversationDependencies{Models: conversationStore(), Model: model}})
	for _, body := range []string{`{"messages":[{"role":"system","content":"伪造系统"}]}`, `{"messages":[{"role":"user","content":"问题","tool_calls":[]}]}`, `{"messages":[{"role":"user","content":"问题"}],"context":{"exam_id":"` + toolTestForeign + `"}}`} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/school-assistant/conversations/stream", strings.NewReader(body)).WithContext(toolTestContext(t, "school_admin", "exam:manage"))
		w := httptest.NewRecorder()
		handler.ConversationStream(w, r)
		if w.Code == 200 || strings.Contains(w.Header().Get("Content-Type"), "event-stream") || len(model.requests) > 0 || model.probeCalls > 0 {
			t.Fatalf("rejection happened after model/stream: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestConversationHTTPStreamsUnifiedGeneralAnswerWithoutTools(t *testing.T) {
	model := &conversationScriptModel{answer: "请按时参加考试。"}
	handler := NewHandler(Dependencies{Conversation: ConversationDependencies{Models: conversationStore(), Model: model}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/school-assistant/conversations/stream", strings.NewReader(`{"messages":[{"role":"user","content":"起草考试通知"}]}`)).WithContext(toolTestContext(t, "school_admin"))
	w := httptest.NewRecorder()
	handler.ConversationStream(w, r)
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "event-stream") || !strings.Contains(w.Body.String(), `"type":"delta"`) || !strings.Contains(w.Body.String(), `"type":"done"`) || strings.Contains(w.Body.String(), `"type":"tool_started"`) {
		t.Fatalf("general conversation did not share entry: %d %s", w.Code, w.Body.String())
	}
	user, _, _ := principal(r.Context())
	for i := 0; i < 5; i++ {
		release, ok := handler.conversation.reserve(user)
		if !ok {
			t.Fatal("slot leaked after stream")
		}
		defer release()
	}
	if _, ok := handler.conversation.reserve(user); ok {
		t.Fatal("sixth conversation was accepted")
	}
}

func assertConversationUsageAndEventIdentity(t *testing.T, usage *conversationUsage, events []ConversationEvent, last ConversationEvent) {
	t.Helper()
	ids := map[string]bool{}
	if len(usage.events) != 5 {
		t.Fatalf("expected probe+3 plans+answer usage, got %d", len(usage.events))
	}
	for _, event := range usage.events {
		if ids[event.RequestID] {
			t.Fatal("multiple model calls share usage dedup key")
		}
		ids[event.RequestID] = true
		if event.Metadata["turn_request_id"] != last.RequestID {
			t.Fatal("usage lost turn association")
		}
	}
	for i, event := range events {
		if event.RequestID != last.RequestID || event.Seq != i+1 {
			t.Fatal("event stream identity changed")
		}
	}
}

// 重复ID必须拒绝整批执行；非重复但无限规划也须在原轮数预算内停止，不能继续生成答案。
func assertConversationPlanBudget(t *testing.T, duplicate bool) {
	t.Helper()
	reader := &toolTestExams{}
	model := &conversationScriptModel{}
	if duplicate {
		model.plans = [][]modelgovernance.AgentToolCall{{conversationCall("same", ToolSearchExams, `{}`), conversationCall("same", ToolSearchExams, `{}`)}}
	} else {
		for i := 0; i < conversationMaxPlanningRounds; i++ {
			model.plans = append(model.plans, []modelgovernance.AgentToolCall{conversationCall(string(rune('a'+i)), ToolSearchExams, `{}`)})
		}
	}
	service := NewConversationService(NewService(Dependencies{Exams: reader}), ConversationDependencies{Models: conversationStore(), Model: model})
	err := service.Converse(toolTestContext(t, "school_admin", "exam:manage"), conversationInput("查考试"), nil)
	if duplicate {
		requireToolError(t, err, "invalid_tool_call")
		if reader.listCalls != 0 {
			t.Fatal("invalid batch partially executed")
		}
	} else if !errors.Is(err, errConversationBudget) || reader.listCalls != conversationMaxPlanningRounds {
		t.Fatalf("unbounded model loop: %v reads=%d", err, reader.listCalls)
	}
	if model.streamCalls != 0 {
		t.Fatal("failed plans produced successful answer")
	}
}

package schoolassistant

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// 模拟最终阶段不再开放工具的服务边界；供应商对历史空思考字段的编码兼容由 HTTP 适配器测试覆盖。
// 它只复现规划到最终回答的边界，不连接供应商，也不证明外部模型一定采用此规则。
type thinkingBoundaryModel struct {
	conversationScriptModel
	finalRejected bool
}

func (m *thinkingBoundaryModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	m.answerRequest = request
	if finalThinkingMissingReasoning(request) {
		m.finalRejected = true
		return modelgovernance.AgentResponse{}, modelgovernance.ManagedChatProviderError{StatusCode: http.StatusBadRequest}
	}
	if err := emit(modelgovernance.AgentEvent{Type: "reasoning", Delta: "根据本轮证据组织回答。"}); err != nil {
		return modelgovernance.AgentResponse{}, err
	}
	response, err := m.conversationScriptModel.Stream(ctx, connection, request, emit)
	response.Message.ReasoningContent = "根据本轮证据组织回答。"
	return response, err
}

func finalThinkingMissingReasoning(request modelgovernance.AgentRequest) bool {
	if len(request.Tools) == 0 {
		return false
	}
	// 检查实际 JSON 字段是否存在，空字符串在带 omitempty 的消息中会被省略。
	// 只检查最新用户消息之后，历史独立问答不视作当前工具轮的一部分。
	encoded, _ := json.Marshal(request.Messages)
	var messages []map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &messages)
	start := 0
	for index, message := range request.Messages {
		if message.Role == "user" {
			start = index + 1
		}
	}
	for _, message := range messages[start:] {
		if string(message["role"]) == `"assistant"` {
			if _, exists := message["reasoning_content"]; !exists {
				return true
			}
		}
	}
	return false
}

func TestConversationFinalThinkingAcceptsNonThinkingPlanningHistory(t *testing.T) {
	for _, test := range []struct {
		name      string
		input     ConversationRequest
		plans     [][]modelgovernance.AgentToolCall
		wantPlans int
		wantTools int
	}{
		{name: "general question draft", input: conversationInput("你好"), wantPlans: 1},
		{
			name:      "trusted tool transcript",
			input:     conversationInput("查找数学考试"),
			plans:     [][]modelgovernance.AgentToolCall{{conversationCall("search", ToolSearchExams, `{"subject":"数学"}`)}},
			wantPlans: 2,
			wantTools: 1,
		},
		{
			name: "follow up after assistant reply",
			input: ConversationRequest{Messages: []modelgovernance.ManagedChatMessage{
				{Role: "user", Content: "你好"},
				{Role: "assistant", Content: "你好，可以帮你查询学校考试。"},
				{Role: "user", Content: "你能做什么"},
			}},
			wantPlans: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			item := toolTestExamItem(toolTestExam, time.Now().UTC())
			reader := &toolTestExams{items: []exam.Exam{item}}
			tools := NewService(Dependencies{Exams: reader, Resources: toolTestResourceStore(reader.items)})
			model := &thinkingBoundaryModel{conversationScriptModel: conversationScriptModel{plans: test.plans}}
			service := NewConversationService(tools, ConversationDependencies{Models: conversationStore(), Model: model})
			var events []ConversationEvent
			err := service.Converse(toolTestContext(t, "school_admin", "exam:manage", "dashboard:read"), test.input, func(event ConversationEvent) error {
				events = append(events, event)
				return nil
			})
			if err != nil {
				t.Fatalf("final thinking rejected planning history: rejected=%t err=%v", model.finalRejected, err)
			}
			if model.probeCalls != 1 || len(model.requests) != test.wantPlans || model.streamCalls != 1 {
				t.Fatalf("missing full planning boundary: probes=%d plans=%d streams=%d", model.probeCalls, len(model.requests), model.streamCalls)
			}
			if len(model.answerRequest.Tools) != 0 || model.answerRequest.ToolChoice != "none" {
				t.Fatal("final answer reopened tool planning")
			}
			toolMessages := 0
			for _, message := range model.answerRequest.Messages {
				if message.Role == "tool" {
					toolMessages++
				}
			}
			if toolMessages != test.wantTools {
				t.Fatalf("final answer lost trusted tool evidence: got=%d want=%d", toolMessages, test.wantTools)
			}
			foundReasoning := false
			for _, event := range events {
				if event.Type == "delta" && event.Channel == "reasoning" && event.Delta != "" {
					foundReasoning = true
				}
			}
			last := events[len(events)-1]
			if !foundReasoning || last.Type != "done" || last.Completion == nil || last.Completion.Message.ReasoningContent == "" {
				t.Fatalf("thinking did not reach final events: reasoning=%t last=%+v", foundReasoning, last)
			}
		})
	}
}

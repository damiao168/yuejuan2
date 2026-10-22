package modelgovernance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 先让非思考规划生成草稿，再用真实 HTTP 编码发送最终轮；上游严格检查最终轮不再开放工具。
// 合成流只证明参数、历史和思考字段的传递，不代表外部供应商的持续可用性。
func TestHTTPAgentFinalThinkingWithoutToolDefinitionsPreservesPlanningHistory(t *testing.T) {
	for _, adapter := range []string{"openai_compatible", "dashscope_native"} {
		for _, history := range []string{"draft", "tool result", "follow up"} {
			t.Run(adapter+"/"+history, func(t *testing.T) {
				calls := 0
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					parameters, rawMessages, ok := readAgentRoundTripPayload(t, r, adapter)
					if !ok {
						return
					}
					calls++
					if calls == 1 {
						if len(parameters["tools"]) == 0 {
							t.Error("planning lost tool definitions")
						}
						if adapter == "dashscope_native" {
							if string(parameters["enable_thinking"]) != "false" {
								t.Error("native planning compatibility changed")
							}
							_, _ = io.WriteString(w, `{"output":{"choices":[{"message":{"content":"合成草稿"},"finish_reason":"stop"}]}}`)
						} else {
							if string(parameters["thinking"]) != `{"type":"disabled"}` {
								t.Error("compatible planning compatibility changed")
							}
							_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"合成草稿"},"finish_reason":"stop"}]}`)
						}
						return
					}
					if len(parameters["tools"]) != 0 || len(parameters["tool_choice"]) != 0 {
						// 最终轮不开放新工具，避免非思考草稿被按思考工具模式校验。
						w.WriteHeader(http.StatusBadRequest)
						_, _ = io.WriteString(w, `{"error":{"message":"reasoning_content is required when thinking with tools"}}`)
						return
					}
					if len(parameters["thinking"]) > 0 || len(parameters["enable_thinking"]) > 0 {
						t.Error("final thinking mode overridden")
					}
					if adapter == "openai_compatible" && history == "tool result" {
						var transcript []map[string]json.RawMessage
						_ = json.Unmarshal(rawMessages, &transcript)
						for _, message := range transcript {
							if string(message["role"]) == `"assistant"` && len(message["tool_calls"]) > 0 && string(message["reasoning_content"]) != `""` {
								w.WriteHeader(http.StatusBadRequest)
								_, _ = io.WriteString(w, `{"error":{"message":"reasoning_content must be a string for assistant tool calls"}}`)
								return
							}
						}
					}
					if !strings.Contains(string(rawMessages), "合成草稿") {
						t.Error("final request lost draft")
					}
					if history == "tool result" && (!strings.Contains(string(rawMessages), "synthetic-tool") || !strings.Contains(string(rawMessages), `\"remaining\":3`)) {
						t.Error("final request lost trusted tool result")
					}
					if history == "follow up" && !strings.Contains(string(rawMessages), "先前回复") {
						t.Error("final request lost conversation history")
					}
					w.Header().Set("Content-Type", "text/event-stream")
					if adapter == "dashscope_native" {
						_, _ = io.WriteString(w, "data: {\"output\":{\"choices\":[{\"message\":{\"reasoning_content\":\"合成思考\",\"content\":\"合成回复\"},\"finish_reason\":\"stop\"}]}}\n\n")
					} else {
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"合成思考\",\"content\":\"合成回复\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					}
				}))
				defer server.Close()
				connection := agentTestConnection(server, adapter)
				connection.Config.ProviderKey, connection.Config.ModelName = "deepseek", "deepseek-flash"
				if adapter == "dashscope_native" {
					connection.Config.ProviderKey, connection.Config.ModelName = "aliyun", "qwen3-max"
				}
				messages := []AgentMessage{{Role: "user", Content: "合成问题"}}
				if history == "follow up" {
					messages = append(messages, AgentMessage{Role: "assistant", Content: "先前回复"}, AgentMessage{Role: "user", Content: "继续解释"})
				}
				if history == "tool result" {
					messages = append(messages,
						AgentMessage{Role: "assistant", ToolCalls: []AgentToolCall{{ID: "synthetic-tool", Name: "get_exam_progress", Arguments: json.RawMessage(`{"exam_id":"synthetic"}`)}}},
						AgentMessage{Role: "tool", ToolCallID: "synthetic-tool", Name: "get_exam_progress", Content: `{"remaining":3}`})
				}
				model := agentTestModel(t, server)
				draft, err := model.Complete(context.Background(), connection, AgentRequest{Messages: messages, Tools: []AgentTool{agentTestTool()}, ToolChoice: "auto"})
				if err != nil {
					t.Fatal(err)
				}
				messages = append(messages, draft.Message, AgentMessage{Role: "system", Content: "生成最终回复，不再调用工具。"})
				var events []AgentEvent
				response, err := model.Stream(context.Background(), connection, AgentRequest{Messages: messages, ToolChoice: "none"}, func(event AgentEvent) error { events = append(events, event); return nil })
				if err != nil {
					t.Fatalf("final request rejected planning history: %v", err)
				}
				if calls != 2 || len(events) != 2 || events[0].Type != "reasoning" || response.Message.ReasoningContent != "合成思考" || response.Message.Content != "合成回复" {
					t.Fatalf("incomplete final response: calls=%d events=%v", calls, events)
				}
			})
		}
	}
}

func TestAgentEmptyReasoningFieldIsLimitedToDeepSeekFinalToolHistory(t *testing.T) {
	for _, test := range []struct {
		name, provider, reasoning string
		stream, tool, wantField   bool
	}{
		{name: "deepseek final tool", provider: "deepseek", stream: true, tool: true, wantField: true},
		{name: "deepseek planning tool", provider: "deepseek", tool: true},
		{name: "deepseek plain history", provider: "deepseek", stream: true},
		{name: "custom final tool", provider: "custom", stream: true, tool: true},
		{name: "qwen final tool", provider: "aliyun", stream: true, tool: true},
		{name: "existing reasoning", provider: "deepseek", reasoning: "已有思考", stream: true, tool: true, wantField: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			message := AgentMessage{Role: "assistant", Content: "历史正文", ReasoningContent: test.reasoning}
			if test.tool {
				message.ToolCalls = []AgentToolCall{{ID: "synthetic", Name: "get_exam_progress", Arguments: json.RawMessage(`{}`)}}
			}
			payload := agentProviderPayload(ManagedAPIConfig{ProviderKey: test.provider, ModelName: "deepseek-flash"}, AgentRequest{Messages: []AgentMessage{message}}, test.stream, false)
			encoded, err := json.Marshal(payload["messages"])
			if err != nil {
				t.Fatal(err)
			}
			var messages []map[string]json.RawMessage
			if err = json.Unmarshal(encoded, &messages); err != nil {
				t.Fatal(err)
			}
			raw, exists := messages[0]["reasoning_content"]
			if exists != test.wantField {
				t.Fatalf("reasoning field presence=%t want=%t", exists, test.wantField)
			}
			if exists {
				var reasoning string
				_ = json.Unmarshal(raw, &reasoning)
				if reasoning != test.reasoning {
					t.Fatal("existing reasoning changed")
				}
			}
		})
	}
}

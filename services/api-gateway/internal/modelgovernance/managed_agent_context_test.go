package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func smallAgentContextConfig() ManagedAPIConfig {
	return ManagedAPIConfig{ContextPolicy: AgentContextPolicy{ContextWindowTokens: 4_096, MaxInputTokens: 2_560, MaxOutputTokens: 1_024}}
}

func TestAgentContextBudgetTrimsWholeOlderToolTurnsAndKeepsSystem(t *testing.T) {
	request := AgentRequest{Messages: []AgentMessage{
		{Role: "system", Content: "稳定规则"},
		{Role: "user", Content: strings.Repeat("旧材料", 400)},
		{Role: "assistant", ToolCalls: []AgentToolCall{{ID: "old-call", Name: "read_exam", Arguments: json.RawMessage(`{}`)}}},
		{Role: "tool", ToolCallID: "old-call", Content: "旧结果"},
		{Role: "assistant", Content: "旧回答"},
		{Role: "system", Content: "本轮授权范围"},
		{Role: "user", Content: "当前问题"},
		{Role: "assistant", ToolCalls: []AgentToolCall{{ID: "current-call", Name: "read_exam", Arguments: json.RawMessage(`{}`)}}},
		{Role: "tool", ToolCallID: "current-call", Content: "当前结果"},
	}}
	before, _ := json.Marshal(request)
	prepared, usage, err := ApplyAgentContextBudget(smallAgentContextConfig(), request)
	if err != nil || usage.DroppedMessages != 4 || usage.EstimatedInputTokens > usage.InputBudgetTokens {
		t.Fatalf("older round was not bounded: usage=%+v err=%v", usage, err)
	}
	if err := validateAgentTranscript(prepared.Messages); err != nil || len(prepared.Messages) != 5 ||
		prepared.Messages[0].Content != "稳定规则" || prepared.Messages[1].Content != "本轮授权范围" || prepared.Messages[4].ToolCallID != "current-call" {
		t.Fatalf("current tool batch or system changed: messages=%+v err=%v", prepared.Messages, err)
	}
	after, _ := json.Marshal(request)
	if string(before) != string(after) {
		t.Fatal("preparing the model view mutated the retained transcript")
	}
	second, secondUsage, err := ApplyAgentContextBudget(smallAgentContextConfig(), prepared)
	if err != nil || !reflect.DeepEqual(prepared, second) || secondUsage.DroppedMessages != 0 {
		t.Fatal("HTTP fallback changed an already prepared transcript")
	}
}

func TestAgentContextBudgetRejectsCurrentEvidenceAndAccountsToolsImagesAndRemainingUsage(t *testing.T) {
	config := smallAgentContextConfig()
	for _, scenario := range []string{"current question", "current tool result", "tool schema", "remaining turn usage"} {
		t.Run(scenario, func(t *testing.T) {
			request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: "当前问题"}}}
			switch scenario {
			case "current question":
				request.Messages[0].Content = strings.Repeat("中文", 1_000)
			case "current tool result":
				request.Messages = append(request.Messages, AgentMessage{Role: "assistant", ToolCalls: []AgentToolCall{{ID: "current", Name: "read_exam", Arguments: json.RawMessage(`{}`)}}},
					AgentMessage{Role: "tool", ToolCallID: "current", Content: strings.Repeat("fact", 1_000)})
			case "tool schema":
				request.Tools = []AgentTool{{Name: "read_exam", Description: strings.Repeat("description", 500), Parameters: json.RawMessage(`{}`)}}
			case "remaining turn usage":
				request.TokenBudget = 1_050
			}
			_, _, err := ApplyAgentContextBudget(config, request)
			want := ErrAgentContextExceeded
			if scenario == "remaining turn usage" {
				want = ErrAgentTurnBudgetExceeded
			}
			if !errors.Is(err, want) {
				t.Fatalf("oversized current input was not rejected: %v", err)
			}
		})
	}
	request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: "看图"}}}
	_, plain, err := ApplyAgentContextBudget(config, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages[0].Images = []ManagedChatAttachment{chatTestImage(t)}
	_, illustrated, err := ApplyAgentContextBudget(config, request)
	if err != nil || illustrated.EstimatedInputTokens < plain.EstimatedInputTokens+1_024 {
		t.Fatalf("image reserve missing: plain=%+v image=%+v err=%v", plain, illustrated, err)
	}
}

func TestAgentContextBudgetProtectsProtocolBeforeDroppingHistory(t *testing.T) {
	request := AgentRequest{Messages: []AgentMessage{
		{Role: "user", Content: strings.Repeat("old", 2_000)},
		{Role: "assistant", ToolCalls: []AgentToolCall{{ID: "call", Name: "read_exam", Arguments: json.RawMessage(`{}`)}}},
		{Role: "user", Content: "cannot hide the unanswered tool call by trimming"},
	}}
	if _, _, err := ApplyAgentContextBudget(smallAgentContextConfig(), request); !errors.Is(err, ErrAgentInvalidRequest) {
		t.Fatalf("malformed tool history was accepted after compaction: %v", err)
	}
}

func TestAgentContextHTTPRejectsBeforeNetworkAndUsesConfiguredOutput(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.MaxTokens != 1_024 {
			t.Errorf("configured output reserve was not applied: %+v %v", body, err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()
	model := agentTestModel(t, server)
	connection := agentTestConnection(server, "openai_compatible")
	connection.Config.ContextPolicy = smallAgentContextConfig().ContextPolicy
	request := AgentRequest{Messages: []AgentMessage{{Role: "user", Content: strings.Repeat("x", 3_000)}}}
	if _, err := model.Complete(context.Background(), connection, request); !errors.Is(err, ErrAgentContextExceeded) || calls.Load() != 0 {
		t.Fatalf("oversized prompt reached the provider: calls=%d err=%v", calls.Load(), err)
	}
	if _, err := model.Stream(context.Background(), connection, request, func(AgentEvent) error { return nil }); !errors.Is(err, ErrAgentContextExceeded) || calls.Load() != 0 {
		t.Fatalf("oversized stream reached the provider: calls=%d err=%v", calls.Load(), err)
	}
	request.Messages[0].Content = "当前问题"
	if _, err := model.Complete(context.Background(), connection, request); err != nil || calls.Load() != 1 {
		t.Fatalf("bounded prompt was not sent: calls=%d err=%v", calls.Load(), err)
	}
}

func TestAgentContextPolicyDefaultsAndRejectsInconsistentLimits(t *testing.T) {
	policy, err := NormalizeAgentContextPolicy(AgentContextPolicy{})
	if err != nil || policy != (AgentContextPolicy{ContextWindowTokens: 32_768, MaxInputTokens: 27_648, MaxOutputTokens: 4_096, Tokenizer: "utf8_bytes"}) {
		t.Fatalf("unexpected product default: %+v %v", policy, err)
	}
	for _, invalid := range []AgentContextPolicy{
		{ContextWindowTokens: -1}, {ContextWindowTokens: 2_097_153},
		{MaxOutputTokens: 32_769}, {MaxInputTokens: 32_768},
		{MaxInputTokens: -1}, {MaxOutputTokens: -1},
	} {
		if _, err := NormalizeAgentContextPolicy(invalid); !errors.Is(err, ErrAgentContextPolicy) {
			t.Fatalf("inconsistent budget accepted: %+v %v", invalid, err)
		}
	}
	if _, err := ResolveAgentContextPolicy(ManagedAPIConfig{DataPolicy: map[string]any{"context_budget": map[string]any{"context_window_tokens": "128k"}}}); !errors.Is(err, ErrAgentContextPolicy) {
		t.Fatalf("malformed persisted policy silently changed to defaults: %v", err)
	}
}

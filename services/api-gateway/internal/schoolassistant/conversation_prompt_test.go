package schoolassistant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

func TestConversationPromptKeepsClockAndAuthorizedContextOutOfStaticPrefix(t *testing.T) {
	history := []modelgovernance.AgentMessage{{Role: "user", Content: "以前的问题"}, {Role: "assistant", Content: "以前的回答"}, {Role: "user", Content: "当前问题"}}
	first := conversationPromptMessages("稳定规则", history, "时间甲；已授权考试甲")
	second := conversationPromptMessages("稳定规则", history, "时间乙；已授权考试乙")
	for index := 0; index < 3; index++ {
		if first[index].Content != second[index].Content {
			t.Fatalf("dynamic request data changed the reusable prefix at %d", index)
		}
	}
	if first[3].Role != "system" || !strings.Contains(first[3].Content, "考试甲") || first[4].Content != "当前问题" {
		t.Fatalf("trusted dynamic context must precede the current user message: %+v", first)
	}
	if len(history) != 3 || history[0].Content != "以前的问题" {
		t.Fatal("assembling a request changed the retained history")
	}
}

func TestPublicSearchActionKeepsClockOutOfStableInstructions(t *testing.T) {
	messages, err := publicSearchActionMessages(preparedConversation{}, publicSearchLoopState{Question: "公开问题"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messages[0].Content, "当前服务器时间") || !strings.Contains(messages[1].Content, "当前服务器时间") || messages[len(messages)-1].Role != "user" {
		t.Fatal("public search must preserve a stable instruction prefix and a separate dynamic clock")
	}
}

func TestConversationContextOverflowDoesNotCallInjectedModel(t *testing.T) {
	model := &conversationScriptModel{}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	p := preparedConversation{connection: modelgovernance.ManagedAPIConnection{Config: modelgovernance.ManagedAPIConfig{ContextPolicy: modelgovernance.AgentContextPolicy{ContextWindowTokens: 2_048}}}}
	_, err := service.completeConversationModel(context.Background(), p, modelgovernance.AgentRequest{Messages: []modelgovernance.AgentMessage{{Role: "user", Content: strings.Repeat("长", 2_000)}}}, "planning", 1, time.Second)
	code, _ := conversationError(err)
	if code != "ai_chat_context_exceeded" || len(model.requests) != 0 {
		t.Fatalf("oversized current input reached model: code=%s requests=%d err=%v", code, len(model.requests), err)
	}
}

func TestConversationChecksSpentTurnBudgetBeforePlanningAndAnswer(t *testing.T) {
	model := &conversationScriptModel{}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	total := modelgovernance.ManagedAPIProbeUsage{TotalTokens: conversationMaxTokens}
	count := 0
	_, err := service.requestConversationPlan(context.Background(), preparedConversation{}, &count, &total)
	if !errors.Is(err, errConversationBudget) || count != 0 {
		t.Fatalf("exhausted planning budget made another request: count=%d err=%v", count, err)
	}
	if err := service.answer(context.Background(), preparedConversation{}, 1, total, func(ConversationEvent) error { return nil }); !errors.Is(err, errConversationBudget) {
		t.Fatalf("exhausted answer budget was accepted: %v", err)
	}
	if len(model.requests) != 0 || model.streamCalls != 0 {
		t.Fatal("an exhausted turn invoked the provider")
	}
}

func TestConversationPlanningUsesRemainingTurnBudget(t *testing.T) {
	model := &conversationScriptModel{}
	service := NewConversationService(nil, ConversationDependencies{Model: model})
	total := modelgovernance.ManagedAPIProbeUsage{TotalTokens: 53_000}
	count := 0
	p := preparedConversation{messages: []modelgovernance.AgentMessage{{Role: "user", Content: "请解释规则"}}}
	if _, err := service.requestConversationPlan(context.Background(), p, &count, &total); err != nil {
		t.Fatal(err)
	}
	reserve, reserveErr := conversationFinalReserve(p)
	if reserveErr != nil || len(model.requests) != 1 || model.requests[0].TokenBudget != 7_000-reserve || model.requests[0].MaxOutputTokens != conversationPlanningOutputTokens || total.TotalTokens != 53_015 {
		t.Fatal("planning did not carry the remaining budget or account for returned usage")
	}
}

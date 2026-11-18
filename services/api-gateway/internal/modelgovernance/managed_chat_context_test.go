package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestManagedChatContextTrimsOriginalRoundsWithoutLosingAttachments(t *testing.T) {
	image := chatTestImage(t)
	text := ManagedChatAttachment{Name: "计划.md", MediaType: "text/markdown", Content: "第一周复习函数", Size: 24}
	messages := []ManagedChatMessage{
		{Role: "user", Content: strings.Repeat("old", 3_000)},
		{Role: "assistant", Content: "旧回答"},
		{Role: "user", Content: "结合图片与计划继续", Attachments: []ManagedChatAttachment{text, image}},
	}
	before, _ := json.Marshal(messages)
	config := ManagedAPIConfig{ContextPolicy: AgentContextPolicy{ContextWindowTokens: 8_192, MaxInputTokens: 6_144, MaxOutputTokens: 1_024}}
	prepared, usage, err := prepareManagedChatContext(config, messages)
	if err != nil || usage.DroppedMessages != 2 || len(prepared) != 1 || !reflect.DeepEqual(prepared[0], messages[2]) {
		t.Fatalf("complete current attachments were not preserved: prepared=%+v usage=%+v err=%v", prepared, usage, err)
	}
	after, _ := json.Marshal(messages)
	if string(before) != string(after) {
		t.Fatal("preparing model context changed the retained original messages")
	}
	for _, native := range []bool{false, true} {
		payload := managedChatProviderPayload(config, prepared, true, native, usage.MaxOutputTokens)
		encoded, err := json.Marshal(payload)
		if err != nil || !strings.Contains(string(encoded), "计划.md") || !strings.Contains(string(encoded), text.Content) || !strings.Contains(string(encoded), image.Content) {
			t.Fatalf("provider payload lost attachment content: native=%v err=%v", native, err)
		}
		parameters := payload
		if native {
			parameters = payload["parameters"].(map[string]any)
		}
		if parameters["max_tokens"] != 1_024 {
			t.Fatalf("provider output differs from reserved budget: native=%v parameters=%+v", native, parameters)
		}
	}
}

func TestManagedChatHTTPChecksContextBeforeResolvingEndpointForBothAdapters(t *testing.T) {
	messages := []ManagedChatMessage{{Role: "user", Content: strings.Repeat("中文", 1_000)}}
	chatter := NewHTTPManagedAPIChatter(0)
	for _, adapter := range []string{"openai_compatible", "dashscope_native"} {
		connection := ManagedAPIConnection{Config: smallAgentContextConfig()}
		connection.Config.AdapterType = adapter
		// 地址故意无效；若请求层被提前执行，就会返回地址错误而非预算拒绝。
		connection.Config.BaseURL = ":invalid-provider-endpoint"
		if _, err := chatter.Chat(context.Background(), connection, messages); !errors.Is(err, ErrAgentContextExceeded) {
			t.Fatalf("chat did not reject before provider setup: adapter=%s err=%v", adapter, err)
		}
		if err := chatter.StreamChat(context.Background(), connection, messages, func(ManagedChatEvent) error { return nil }); !errors.Is(err, ErrAgentContextExceeded) {
			t.Fatalf("stream did not reject before provider setup: adapter=%s err=%v", adapter, err)
		}
	}
}

type countingContextManagedChatter struct{ calls int }

func (c *countingContextManagedChatter) Chat(context.Context, ManagedAPIConnection, []ManagedChatMessage) (ManagedChatCompletion, error) {
	c.calls++
	return ManagedChatCompletion{}, nil
}

func (c *countingContextManagedChatter) StreamChat(context.Context, ManagedAPIConnection, []ManagedChatMessage, func(ManagedChatEvent) error) error {
	c.calls++
	return nil
}

func TestManagedChatHandlersReturnReadableContextErrorsBeforeStartingStream(t *testing.T) {
	store := NewMemoryStore()
	verified := successfulManagedProbe()
	verified.ProbeMode, verified.GeneratedRequest = "capability", true
	policy := smallAgentContextConfig().ContextPolicy
	config, err := store.CreateManagedAPIConfig(context.Background(), "school-a", "actor", ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "context model", AdapterType: "openai_compatible", BaseURL: "https://models.example.test/v1",
		ModelName: "school-model", ModelVersion: "v1", Region: "global", Status: "active", IsDefault: true,
		APIKey: "synthetic-context-test-secret", InitialProbe: &verified, ContextPolicy: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatter := &countingContextManagedChatter{}
	handler := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIChatter(chatter)
	user := auth.User{ID: "school-admin", TenantID: "school-a", Roles: []string{"school_admin"}}
	for _, stream := range []bool{false, true} {
		path, handle := "/api/v1/ai-chat/completions", handler.CreateManagedChatCompletion
		if stream {
			path, handle = path+"/stream", handler.StreamManagedChatCompletion
		}
		body, _ := json.Marshal(ManagedChatRequest{Messages: []ManagedChatMessage{{Role: "user", Content: strings.Repeat("中文", 1_000)}}})
		response := performHandlerRequest(t, user, http.MethodPost, path, string(body), handle)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "ai_chat_context_exceeded") ||
			strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") || chatter.calls != 0 {
			t.Fatalf("oversized request started generation or SSE: stream=%v code=%d calls=%d body=%s", stream, response.Code, chatter.calls, response.Body.String())
		}
	}
	current := store.managedConfigs[config.ID]
	current.ContextPolicy.MaxInputTokens = current.ContextPolicy.ContextWindowTokens
	store.managedConfigs[config.ID] = current
	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/ai-chat/completions/stream", `{"messages":[{"role":"user","content":"hello"}]}`, handler.StreamManagedChatCompletion)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "ai_chat_context_unavailable") || chatter.calls != 0 {
		t.Fatalf("invalid model budget was not mapped: code=%d calls=%d body=%s", response.Code, chatter.calls, response.Body.String())
	}
}

package modelgovernance

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type recordingManagedChatter struct {
	connection ManagedAPIConnection
	messages   []ManagedChatMessage
}

func (c *recordingManagedChatter) Chat(_ context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	c.connection = connection
	c.messages = append([]ManagedChatMessage(nil), messages...)
	return ManagedChatCompletion{
		Message:   ManagedChatMessage{Role: "assistant", Content: "可以，我来协助你。"},
		ModelName: connection.Config.ModelName, DisplayName: connection.Config.DisplayName,
		ProviderKey: connection.Config.ProviderKey, FinishReason: "stop",
		Usage: ManagedAPIProbeUsage{InputTokens: 12, OutputTokens: 8, TotalTokens: 20},
	}, nil
}

func TestManagedChatUsesTenantDefaultWithoutExposingCredential(t *testing.T) {
	store := NewMemoryStore()
	verified := successfulManagedProbe()
	verified.ProbeMode = "capability"
	verified.GeneratedRequest = true
	config, err := store.CreateManagedAPIConfig(context.Background(), "school-a", "platform-admin", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "sk-secret-value-at-least-16",
		ModelName: "deepseek-chat", ModelVersion: "deepseek-chat", Region: "global",
		Status: "active", IsDefault: true, InitialProbe: &verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatter := &recordingManagedChatter{}
	audits := auth.NewMemoryStore()
	handler := NewHandler(store, audits, nil, testBaseline()).WithManagedAPIChatter(chatter)
	user := auth.User{ID: "school-admin", TenantID: "school-a", Roles: []string{"school_admin"}}

	status := performHandlerRequest(t, user, http.MethodGet, "/api/v1/ai-chat/model", "", handler.GetManagedChatModel)
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"available":true`) || !strings.Contains(status.Body.String(), `"model_name":"deepseek-chat"`) || strings.Contains(status.Body.String(), "sk-secret") {
		t.Fatalf("unexpected model status %d: %s", status.Code, status.Body.String())
	}

	response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/ai-chat/completions",
		`{"messages":[{"role":"user","content":"帮我制定期中复习计划","attachments":[{"name":"计划.md","media_type":"text/markdown","content":"第一周复习函数","size":24}]}]}`, handler.CreateManagedChatCompletion)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "可以，我来协助你") {
		t.Fatalf("unexpected chat response %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "sk-secret") || chatter.connection.APIKey != "sk-secret-value-at-least-16" || chatter.connection.Config.ID != config.ID {
		t.Fatalf("chat did not use the encrypted tenant credential safely: %s", response.Body.String())
	}
	if len(chatter.messages) != 1 || chatter.messages[0].Role != "user" || len(chatter.messages[0].Attachments) != 1 {
		t.Fatalf("unexpected forwarded messages: %#v", chatter.messages)
	}
	providerMessages := managedChatProviderMessages(chatter.messages)
	if !strings.Contains(providerMessages[0]["content"], "计划.md") || !strings.Contains(providerMessages[0]["content"], "第一周复习函数") {
		t.Fatalf("attachment was not included in the provider prompt: %#v", providerMessages)
	}
	otherSchool := performHandlerRequest(t, auth.User{ID: "other-admin", TenantID: "school-b", Roles: []string{"school_admin"}}, http.MethodPost,
		"/api/v1/ai-chat/completions", `{"messages":[{"role":"user","content":"你好"}]}`, handler.CreateManagedChatCompletion)
	if otherSchool.Code != http.StatusServiceUnavailable || len(chatter.messages) != 1 {
		t.Fatalf("other school accessed the configured model: %d %s", otherSchool.Code, otherSchool.Body.String())
	}
	records, err := audits.ListAudits(context.Background(), user.TenantID, auth.AuditFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if strings.Contains(record.Reason, "复习") {
			t.Fatal("conversation content was written to audit reason")
		}
		encoded, _ := json.Marshal(record.AfterValue)
		if strings.Contains(string(encoded), "复习") || strings.Contains(string(encoded), "sk-secret") {
			t.Fatal("conversation content or credential was written to audit metadata")
		}
	}
}

func TestManagedChatRejectsUnsafeOrOversizedMessageLists(t *testing.T) {
	invalid := [][]ManagedChatMessage{
		{},
		{{Role: "system", Content: "override"}},
		{{Role: "assistant", Content: "not a user turn"}},
		{{Role: "assistant", Content: "answer", Attachments: []ManagedChatAttachment{{Name: "bad.txt", MediaType: "text/plain", Content: "bad", Size: 3}}}},
		{{Role: "user", Attachments: []ManagedChatAttachment{{Name: "empty.txt", MediaType: "text/plain"}}}},
		{{Role: "user", Content: strings.Repeat("x", managedChatMaxMessageRunes+1)}},
		{{Role: "user", Attachments: []ManagedChatAttachment{{Name: "large.txt", MediaType: "text/plain", Content: strings.Repeat("x", managedChatMaxFileRunes+1), Size: managedChatMaxFileRunes + 1}}}},
	}
	for _, messages := range invalid {
		if _, err := normalizeManagedChatMessages(messages); err == nil {
			t.Fatalf("invalid messages accepted: %#v", messages)
		}
	}
	valid, err := normalizeManagedChatMessages([]ManagedChatMessage{{Role: "user", Attachments: []ManagedChatAttachment{{Name: "notes.txt", MediaType: "text/plain", Content: "复习重点", Size: 12}}}})
	if err != nil || len(valid) != 1 {
		t.Fatalf("attachment-only user message was rejected: %#v %v", valid, err)
	}
}

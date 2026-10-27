package modelgovernance

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/middleware"
)

type adversarialCountingChat struct {
	recordingManagedChatter
	calls int
}

func (c *adversarialCountingChat) Chat(ctx context.Context, connection ManagedAPIConnection, messages []ManagedChatMessage) (ManagedChatCompletion, error) {
	c.calls++
	return c.recordingManagedChatter.Chat(ctx, connection, messages)
}

// 两个不同对话只复用相关ID，不使用幂等收据、不访问HTTP服务器或真实供应商。
// 不同生成必须独立计量，追踪ID重复不能吞掉用量事件。
func TestDistinctChatGenerationsWithRepeatedRequestIDRetainUsage(t *testing.T) {
	store := newRecordingUsageStore()
	verified := managedTextProbeFixture(successfulManagedProbe())
	_, err := store.CreateManagedAPIConfig(context.Background(), "synthetic-school", "synthetic-configurer", ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "synthetic chat", AdapterType: "openai_compatible",
		BaseURL: "https://synthetic.invalid/v1", APIKey: "synthetic-key-at-least-16",
		ModelName: "synthetic-chat", ModelVersion: "v1", Region: "global", Status: "active", IsDefault: true, InitialProbe: &verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatter := &adversarialCountingChat{}
	h := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIChatter(chatter)
	handle := middleware.RequestID()(http.HandlerFunc(h.CreateManagedChatCompletion))
	for index := 0; index < 2; index++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/ai-chat/completions", strings.NewReader(fmt.Sprintf(`{"messages":[{"role":"user","content":"synthetic distinct turn %d"}]}`, index)))
		r.Header.Set("X-Request-ID", "01ARZ3NDEKTSV4RRFFQ69G5FAV")
		r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "synthetic-user", TenantID: "synthetic-school", Roles: []string{"school_admin"}}))
		w := httptest.NewRecorder()
		handle.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("X-Request-ID") != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
			t.Fatalf("synthetic request failed: status=%d", w.Code)
		}
	}
	events := store.recorded()
	if chatter.calls != 2 || len(events) != 2 || events[0].TotalTokens != 20 || events[0].RequestCount != 1 {
		t.Fatalf("distinct generation count changed: adapter calls=%d ledger events=%d", chatter.calls, len(events))
	}
	if events[0].RequestID == events[1].RequestID {
		t.Fatal("generation IDs reused")
	}
	for _, event := range events {
		if event.Metadata["trace_request_id"] != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
			t.Fatal("trace correlation lost")
		}
	}
}

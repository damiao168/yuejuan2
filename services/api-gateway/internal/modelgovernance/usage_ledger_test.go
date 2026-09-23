package modelgovernance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

type recordingUsageStore struct {
	*MemoryStore
	mu     sync.Mutex
	events []ModelUsageEvent
	keys   map[string]struct{}
}

func newRecordingUsageStore() *recordingUsageStore {
	return &recordingUsageStore{MemoryStore: NewMemoryStore(), keys: map[string]struct{}{}}
}

func (s *recordingUsageStore) RecordModelUsage(_ context.Context, event ModelUsageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := event.TenantID + "\x00" + event.RequestID + "\x00" + event.Feature
	if _, exists := s.keys[key]; exists {
		return nil
	}
	s.keys[key] = struct{}{}
	s.events = append(s.events, event)
	return nil
}

func (s *recordingUsageStore) recorded() []ModelUsageEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ModelUsageEvent(nil), s.events...)
}

func TestManagedChatRecordsProviderUsageOncePerRequest(t *testing.T) {
	store := newRecordingUsageStore()
	verified := successfulManagedProbe()
	verified.ProbeMode, verified.GeneratedRequest = "capability", true
	_, err := store.CreateManagedAPIConfig(context.Background(), "school-a", "platform-admin", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "sk-secret-value-at-least-16",
		ModelName: "deepseek-chat", ModelVersion: "deepseek-chat", Region: "global",
		Status: "active", IsDefault: true, InitialProbe: &verified,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIChatter(&recordingManagedChatter{})
	user := auth.User{ID: "school-admin", TenantID: "school-a", Roles: []string{"school_admin"}}

	for range 2 {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/ai-chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"制定复习计划"}]}`))
		request = request.WithContext(auth.WithUser(logger.WithRequestID(request.Context(), "chat-usage-1"), user))
		response := httptest.NewRecorder()
		handler.CreateManagedChatCompletion(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("chat returned %d: %s", response.Code, response.Body.String())
		}
	}

	events := store.recorded()
	if len(events) != 1 {
		t.Fatalf("idempotent chat request produced %d usage events: %#v", len(events), events)
	}
	event := events[0]
	if event.Feature != "school_ai_chat" || event.ProviderKey != "deepseek" || event.ModelName != "deepseek-chat" ||
		event.RequestCount != 1 || event.InputTokens != 12 || event.OutputTokens != 8 || event.TotalTokens != 20 || event.Status != "succeeded" {
		t.Fatalf("unexpected chat usage event: %#v", event)
	}
}

func TestCapabilityProbeRecordsGeneratedUsage(t *testing.T) {
	store := newRecordingUsageStore()
	const tenantID = "00000000-0000-0000-0000-000000000020"
	item, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "deepseek", DisplayName: "DeepSeek", AdapterType: "openai_compatible",
		BaseURL: "https://api.deepseek.com", APIKey: "secret-value-at-least-16", ModelName: "deepseek-chat",
		ModelVersion: "deepseek-chat", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilityCalls := 0
	quickCalls := 0
	handler := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIProber(splitManagedProber{quickCalls: &quickCalls, capabilityCalls: &capabilityCalls})
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenantID+"&mode=capability&force=true", nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(logger.WithRequestID(request.Context(), "probe-usage-1"), user))
	response := httptest.NewRecorder()

	handler.ProbeManagedAPIConfig(response, request)

	if response.Code != http.StatusOK || capabilityCalls != 1 {
		t.Fatalf("capability probe returned %d calls=%d: %s", response.Code, capabilityCalls, response.Body.String())
	}
	events := store.recorded()
	if len(events) != 1 {
		t.Fatalf("expected one probe usage event, got %#v", events)
	}
	event := events[0]
	if event.Feature != "model_probe" || event.RequestCount != 1 || event.InputTokens != 8 || event.OutputTokens != 5 || event.TotalTokens != 13 || event.Status != "succeeded" || event.Metadata["operation"] != "manual" {
		t.Fatalf("unexpected probe usage event: %#v", event)
	}
}

package schoolassistant

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchCapabilityIndicatesConfigurationWithoutNetworkProbe(t *testing.T) {
	provider := newSearchFixture(t, func(_ http.ResponseWriter, _ *http.Request) { t.Error("capabilities made an upstream request") })
	for _, search := range []SearchProvider{nil, provider} {
		capabilities, err := NewService(Dependencies{Conversation: ConversationDependencies{Search: search}}).Capabilities(toolTestContext(t, "school_admin"))
		if err != nil || capabilities.WebSearch == nil || capabilities.WebSearch.Available != (search != nil) || capabilities.WebSearch.Message == "" {
			t.Fatalf("incorrect search configuration capability: %+v %v", capabilities, err)
		}
	}
}

func TestConversationWebSearchHTTPAcceptsFlagAndStreamsActualSources(t *testing.T) {
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[{"title":"公开资料","url":"https://www.moe.gov.cn/notice","content":"真实搜索摘要"}]}`)
	})
	for _, search := range []SearchProvider{nil, provider} {
		model := &webSearchScriptModel{}
		handler := NewHandler(Dependencies{Conversation: ConversationDependencies{Models: conversationStore(), Model: model, Search: search}})
		input := conversationInput("教育公告")
		input.WebSearch = true
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/school-assistant/conversations/stream", bytes.NewReader(body)).WithContext(toolTestContext(t, "school_admin"))
		response := httptest.NewRecorder()
		handler.ConversationStream(response, request)
		if search == nil {
			if response.Code != 200 || !strings.Contains(response.Body.String(), "web_search_unavailable") || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/event-stream") || strings.Contains(response.Body.String(), `"type":"done"`) {
				t.Fatalf("unconfigured search failed outside routed stream: %d %s", response.Code, response.Body.String())
			}
			continue
		}
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"url":"https://www.moe.gov.cn/notice"`) || !strings.Contains(response.Body.String(), `"type":"done"`) || model.streamCalls != 2 {
			t.Fatalf("HTTP web search flag or source metadata lost: %d %s", response.Code, response.Body.String())
		}
	}
}

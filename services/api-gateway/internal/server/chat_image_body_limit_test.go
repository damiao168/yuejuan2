package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestImageChatBodyLimitDoesNotWidenOtherRoutes(t *testing.T) {
	for _, item := range []struct {
		method, path string
		imageChat    bool
	}{
		{http.MethodPost, "/api/v1/school-assistant/conversations/stream", true},
		{http.MethodPost, "/api/v1/ai-chat/completions", true},
		{http.MethodPost, "/api/v1/ai-chat/completions/stream", true},
		{http.MethodGet, "/api/v1/school-assistant/conversations/stream", false},
		{http.MethodPost, "/api/v1/school-assistant/query", false},
		{http.MethodPost, "/api/v1/ai-chat/completions/other", false},
	} {
		request := httptest.NewRequest(item.method, item.path, nil)
		if skipNonImageChat(request) == item.imageChat || skipGlobalBodyLimit(request) != item.imageChat {
			t.Fatalf("image body limit applied to wrong route: %s %s", item.method, item.path)
		}
	}
	cfg := testConfig()
	router := NewRouter(cfg, logger.New(io.Discard, "error"), nil, testAuthStore(t), org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore())
	for _, item := range []struct {
		path     string
		bytes    int
		expected int
	}{
		{"/api/v1/school-assistant/conversations/stream", 3 << 20, http.StatusUnauthorized},
		{"/api/v1/school-assistant/conversations/stream", modelgovernance.ChatRequestBodyLimit + 1, http.StatusRequestEntityTooLarge},
		{"/api/v1/exams", 3 << 20, http.StatusRequestEntityTooLarge},
	} {
		// 只声明 Content-Length，无需分配大请求体；验证在进入业务处理前已有独立上限。
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader("{}"))
		request.ContentLength = int64(item.bytes)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != item.expected {
			t.Fatalf("%s (%d): want %d, got %d", item.path, item.bytes, item.expected, response.Code)
		}
	}
}

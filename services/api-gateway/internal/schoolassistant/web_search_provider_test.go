package schoolassistant

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newSearchFixture(t *testing.T, handler http.HandlerFunc) SearchProvider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	provider, err := NewSearXNGSearch(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// 只模拟模型的公开动作协议；下面检查查询边界与事件，不声称覆盖外部模型的语义理解质量。
type webSearchScriptModel struct {
	conversationScriptModel
	plan          string
	planErr       error
	planUsage     modelgovernance.ManagedAPIProbeUsage
	planToolCalls []modelgovernance.AgentToolCall
	onPlan        func(context.Context)
	streamErr     error
}

func (m *webSearchScriptModel) Complete(ctx context.Context, _ modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest) (modelgovernance.AgentResponse, error) {
	m.requests = append(m.requests, request)
	if m.onPlan != nil {
		m.onPlan(ctx)
	}
	plan := m.plan
	if plan == "" {
		plan = `{"action":"search","query":"公开资料"}`
	}
	usage := m.planUsage
	if usage.TotalTokens == 0 {
		usage = modelgovernance.ManagedAPIProbeUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	}
	err := m.planErr
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: plan, ToolCalls: m.planToolCalls}, Usage: usage}, err
}

func (m *webSearchScriptModel) Stream(ctx context.Context, connection modelgovernance.ManagedAPIConnection, request modelgovernance.AgentRequest, emit func(modelgovernance.AgentEvent) error) (modelgovernance.AgentResponse, error) {
	if researchIsActionRequest(request) {
		m.streamCalls++
		var response modelgovernance.AgentResponse
		var err error
		if len(m.requests) == 0 {
			response, err = m.Complete(ctx, connection, request)
			response.Message.Content = researchFirstSearchAction(response.Message.Content)
			if strings.Contains(response.Message.Content, `"action":"answer"`) && m.streamErr != nil {
				err = m.streamErr
			}
		} else {
			m.requests = append(m.requests, request)
			m.answerRequest = request
			candidates := researchActionCandidates(request)
			evidence := []webSearchEvidence{}
			for _, source := range candidates {
				if source.Excerpt != "" && len(evidence) < webSearchMaxResults {
					evidence = append(evidence, webSearchEvidence{SourceID: source.ID, Quote: source.Excerpt})
				}
			}
			answer := m.answer
			if answer == "" {
				answer = "本次尚未核实所问事实。"
				if len(evidence) > 0 {
					answer = "依据本轮真实资料回答。[1]"
				}
			}
			response = modelgovernance.AgentResponse{Message: modelgovernance.AgentMessage{Role: "assistant", Content: researchAnswerAction(answer, evidence, nil)}, Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25}}
			err = m.streamErr
		}
		response.FinishReason = "stop"
		if err == nil {
			err = emit(modelgovernance.AgentEvent{Type: "content", Delta: response.Message.Content})
		}
		return response, err
	}
	if m.streamErr != nil {
		m.streamCalls++
		m.answerRequest = request
		return modelgovernance.AgentResponse{Usage: modelgovernance.ManagedAPIProbeUsage{InputTokens: 20, OutputTokens: 5, TotalTokens: 25}}, m.streamErr
	}
	last := request.Messages[len(request.Messages)-1].Content
	if m.answer == "" && strings.Contains(last, "本轮支持片段") && !strings.HasSuffix(last, "[]") {
		m.answer = "依据本轮支持片段回答。[1]"
	}
	return m.conversationScriptModel.Stream(ctx, connection, request, emit)
}

func TestSearXNGSearchSendsOnlyExplicitQueryAndValidatesSources(t *testing.T) {
	queries := make(chan string, 1)
	provider := newSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/search" || r.URL.RawQuery != "" {
			t.Errorf("unexpected search request: %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if len(r.PostForm) != 6 || r.PostForm.Get("format") != "json" || r.PostForm.Get("language") != "zh-CN" || r.PostForm.Get("engines") != "duckduckgo,brave,google cse" || r.PostForm.Get("timeout_limit") != "2.5" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected search payload: %v", r.PostForm)
		}
		queries <- r.PostForm.Get("q")
		_, _ = io.WriteString(w, `{"results":[
		{"title":"教育部 <b>公告</b>","url":"https://www.moe.gov.cn/notice#first","content":"<span>公告</span> &amp; 摘要"},
		{"title":"重复结果","url":"https://www.moe.gov.cn/notice#second","content":"重复"},
		{"title":"脚本","url":"javascript:alert(1)","content":"异常"},
		{"title":"本机","url":"http://127.0.0.1/admin","content":"异常"},
		{"title":"内网","url":"http://192.168.1.1/","content":"异常"},
		{"title":"缩写本机","url":"http://127.1/","content":"异常"},
		{"title":"八进制本机","url":"http://0177.0.0.1/","content":"异常"},
		{"title":"凭据","url":"https://user:pass@example.com/","content":"异常"},
		{"title":"另一个来源","url":"https://www.gov.cn/policy","content":"公开资料"}]}`)
	})
	sources, err := provider.Search(context.Background(), "教育政策")
	if err != nil || len(sources) != 2 {
		t.Fatalf("search returned unexpected sources: %v %+v", err, sources)
	}
	if query := <-queries; query != "教育政策" {
		t.Fatalf("wrong query: %q", query)
	}
	if sources[0].ID != "web-1" || sources[0].Kind != "web" || sources[0].URL != "https://www.moe.gov.cn/notice" || sources[0].Label != "教育部 公告" || sources[0].Excerpt != "公告 & 摘要" || sources[0].ReadAt.IsZero() {
		t.Fatalf("source not normalized: %+v", sources[0])
	}
}

func TestSearXNGSearchLimitsResultsAndText(t *testing.T) {
	provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		results := make([]map[string]string, 20)
		for i := range results {
			results[i] = map[string]string{"title": strings.Repeat("中", 400), "content": strings.Repeat("文", 2000), "url": "https://example.com/" + strings.Repeat("x", i+1)}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	})
	sources, err := provider.Search(context.Background(), "公开资料")
	if err != nil || len(sources) != webSearchMaxResults || len([]rune(sources[0].Label)) != 201 || len([]rune(sources[0].Excerpt)) != 1001 {
		t.Fatalf("source limits not applied: err=%v sources=%+v", err, sources)
	}
}

func TestSearXNGPartialFailureDoesNotBecomeFalseNoResults(t *testing.T) {
	for _, test := range []struct {
		name, body string
		wantError  bool
		count      int
	}{
		{"missing results", `{"unresponsive_engines":[]}`, true, 0},
		{"null results", `{"results":null}`, true, 0},
		{"all failed", `{"results":[],"unresponsive_engines":[["engine","timeout"]]}`, true, 0},
		{"genuine empty", `{"results":[],"unresponsive_engines":[]}`, false, 0},
		{"partial valid", `{"results":[{"title":"公开资料","url":"https://example.com/notice","content":"公开内容"}],"unresponsive_engines":[["engine","timeout"]]}`, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, test.body) })
			sources, err := provider.Search(context.Background(), "公开资料")
			if (err != nil) != test.wantError || len(sources) != test.count {
				t.Fatalf("wrong partial-result semantics: err=%v sources=%d", err, len(sources))
			}
		})
	}
}

func TestSearXNGSearchRejectsInvalidEndpointAndKeepsDisabledEmpty(t *testing.T) {
	provider, err := NewSearXNGSearch("  ")
	if err != nil || provider != nil {
		t.Fatalf("blank endpoint should be disabled: %v %v", provider, err)
	}
	for _, endpoint := range []string{"file:///etc/passwd", "https://", "https://user:pass@example.com", "https://example.com?q=x", "https://example.com?", "https://example.com/#fragment"} {
		if _, err := NewSearXNGSearch(endpoint); err == nil {
			t.Errorf("accepted invalid endpoint %q", endpoint)
		}
	}
	for _, endpoint := range []string{"http://searxng:8080", "https://search.example.com/search/"} {
		if _, err := NewSearXNGSearch(endpoint); err != nil {
			t.Errorf("rejected configured endpoint %q: %v", endpoint, err)
		}
	}
}

func TestSearXNGSearchFailsWithoutExposingUpstreamAndDoesNotFollowRedirect(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"json disabled", 403, "upstream-secret"},
		{"limited", 429, "upstream-secret"},
		{"bad json", 200, "upstream-secret"},
		{"oversized", 200, strings.Repeat("x", webSearchMaxBodyBytes+1)},
		{"redirect", 302, "upstream-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := newSearchFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/must-not-follow")
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			})
			_, err := provider.Search(context.Background(), "公开资料")
			requireToolError(t, err, "web_search_failed")
			if strings.Contains(err.Error(), "upstream-secret") {
				t.Fatal("upstream diagnostics leaked")
			}
		})
	}
}

func TestSearXNGSearchCancellationReachesHTTPRequest(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	provider := newSearchFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := provider.Search(ctx, "公开资料"); finished <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP search did not start")
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request did not cancel")
	}
}

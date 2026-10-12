package modelgovernance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 本地 TLS 测试只替换传输层，保留两个生产客户端各自的超时和重定向限制。
func newTestHTTPManagedAPIProber(client *http.Client) *HTTPManagedAPIProber {
	prober := NewHTTPManagedAPIProber(time.Second)
	for _, probeClient := range []*http.Client{prober.httpClient, prober.capabilityHTTPClient} {
		probeClient.Transport = client.Transport
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport = transport.Clone()
			transport.ResponseHeaderTimeout = probeClient.Timeout
			probeClient.Transport = transport
		}
	}
	return prober
}

func TestHTTPManagedAPIProberSeparatesQuickAndCapabilityTimeouts(t *testing.T) {
	for _, test := range []struct {
		name         string
		input, quick time.Duration
	}{
		{name: "default", quick: 8 * time.Second},
		{name: "custom quick", input: time.Second, quick: time.Second},
		{name: "invalid negative", input: -time.Second, quick: 8 * time.Second},
		{name: "invalid excessive", input: 16 * time.Second, quick: 8 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			prober := NewHTTPManagedAPIProber(test.input)
			if prober.httpClient.Timeout != test.quick || prober.capabilityHTTPClient.Timeout != 60*time.Second {
				t.Fatalf("unexpected client budgets: quick=%s capability=%s", prober.httpClient.Timeout, prober.capabilityHTTPClient.Timeout)
			}
			for _, client := range []*http.Client{prober.httpClient, prober.capabilityHTTPClient} {
				if transport := client.Transport.(*http.Transport); transport.ResponseHeaderTimeout != client.Timeout {
					t.Fatalf("header timeout %s would cut client budget %s short", transport.ResponseHeaderTimeout, client.Timeout)
				}
			}
		})
	}
}

func TestHTTPManagedAPIProberAllowsGenerationBeyondQuickTimeout(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		name := "delayed headers"
		if flushHeaders {
			name = "delayed body"
		}
		t.Run(name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					_, _ = io.WriteString(w, `{"data":[{"id":"deepseek-model"}]}`)
					return
				}
				posts.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if flushHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				// 缩短测试时间预算，模拟生成等待超过快速检查上限，覆盖首包和正文两个阶段。
				timer := time.NewTimer(300 * time.Millisecond)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"question_no\":\"1\",\"max_score\":2}"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			prober := newTestHTTPManagedAPIProber(server.Client())
			prober.httpClient.Timeout = 100 * time.Millisecond
			prober.capabilityHTTPClient.Timeout = 2 * time.Second
			result := prober.Probe(context.Background(), ManagedAPIConnection{
				Config: ManagedAPIConfig{ProviderKey: "deepseek", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "deepseek-model"},
				APIKey: "test-probe-key",
			})
			if !result.OK || !result.CredentialCheck.OK || !result.ModelCheck.OK || !result.CapabilityCheck.OK || posts.Load() != 1 {
				t.Fatalf("slow generation was rejected or retried: result=%#v POSTs=%d", result, posts.Load())
			}
		})
	}
}

// 同一能力预算分别在等待响应头和等待正文时耗尽；超时不能重试生成，也不能耗尽调用方预算。
func exerciseManagedGenerationTimeout(t *testing.T, flushHeaders bool) {
	t.Helper()
	var posts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
			return
		}
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		if flushHeaders {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
		}
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	prober.capabilityHTTPClient.Timeout = 100 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := prober.Probe(ctx, ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "test-probe-key",
	})
	if result.OK || result.ErrorCode != "provider_timeout" || result.CapabilityCheck.Code != "provider_timeout" ||
		!result.CredentialCheck.OK || !result.ModelCheck.OK || !result.GeneratedRequest || posts.Load() != 1 || ctx.Err() != nil {
		t.Fatalf("generation timeout was misclassified, retried, or unbounded: result=%#v POSTs=%d context=%v", result, posts.Load(), ctx.Err())
	}
	if flushHeaders && result.ConnectionDiagnostic.FailureStage != "body" {
		t.Fatalf("body timeout lost its failure stage: %#v", result.ConnectionDiagnostic)
	}
}

func TestHTTPManagedAPIProberBoundsGenerationWithoutRetry(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		name := "waiting for headers"
		if flushHeaders {
			name = "waiting for body"
		}
		t.Run(name, func(t *testing.T) {
			exerciseManagedGenerationTimeout(t, flushHeaders)
		})
	}
}

func TestHTTPManagedAPIQuickProbeKeepsShortTimeout(t *testing.T) {
	var gets, posts atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
			return
		}
		gets.Add(1)
		<-r.Context().Done()
		// 取消后正常返回会补写空的 200 响应，并与客户端超时竞争；中断响应以避免空响应绕过响应头失败的重试路径。
		panic(http.ErrAbortHandler)
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	prober.timeout = 100 * time.Millisecond
	prober.httpClient.Timeout = prober.timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := prober.ProbeQuick(ctx, ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "test-probe-key",
	})
	if result.OK || result.ErrorCode != "provider_timeout" || result.GeneratedRequest || gets.Load() != 2 || posts.Load() != 0 || ctx.Err() != nil {
		t.Fatalf("quick probe lost its short timeout or retry bound: result=%#v GETs=%d POSTs=%d context=%v", result, gets.Load(), posts.Load(), ctx.Err())
	}
}

func TestHTTPManagedAPIQuickProbeReadsBodyBeforeCancelingBudget(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// 模型列表分两步返回，暴露“收到响应头就取消请求上下文”的读取中断。
		timer := time.NewTimer(50 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.ProbeQuick(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "test-probe-key",
	})
	if !result.OK || !result.CredentialCheck.OK || !result.ModelCheck.OK || result.GeneratedRequest || result.ConnectionDiagnostic.Attempts != 1 {
		t.Fatalf("model body was canceled before it could be read: %#v", result)
	}
}

func TestHTTPManagedAPIProberCancelsGenerationWithCaller(t *testing.T) {
	var posts atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
			return
		}
		posts.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
		case <-timer.C:
		}
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.Probe(ctx, ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "test-probe-key",
	})
	if result.OK || !result.GeneratedRequest || posts.Load() != 1 || ctx.Err() != context.Canceled {
		t.Fatalf("caller cancellation was ignored or retried: result=%#v POSTs=%d context=%v", result, posts.Load(), ctx.Err())
	}
}

// 先发现可用模型再发送有界JSON能力请求，两个阶段分别计数且使用同一测试凭证。
func writeManagedCompatibleCapability(t *testing.T, w http.ResponseWriter, r *http.Request, modelRequests, completionRequests *int) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer school-probe-secret" {
		t.Errorf("unexpected authorization header: %q", got)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
		(*modelRequests)++
		_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
		(*completionRequests)++
		if got := r.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
			t.Errorf("completion request has content type %q", got)
		}
		var payload struct {
			Model          string            `json:"model"`
			MaxTokens      int               `json:"max_tokens"`
			Stream         bool              `json:"stream"`
			ResponseFormat map[string]string `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode completion request: %v", err)
		}
		if payload.Model != "school-model" || payload.MaxTokens != managedCapabilityMaxTokens || payload.Stream || payload.ResponseFormat["type"] != "json_object" {
			t.Errorf("capability request was not bounded JSON mode: %#v", payload)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"question_no\":\"1\",\"max_score\":2}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19,"prompt_tokens_details":{"cached_tokens":3},"completion_tokens_details":{"reasoning_tokens":2}}}`)
	default:
		t.Errorf("unexpected probe request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func TestHTTPManagedAPIProberChecksOpenAICompatibleModelAndStructuredOutput(t *testing.T) {
	var modelRequests, completionRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeManagedCompatibleCapability(t, w, r, &modelRequests, &completionRequests)
	}))
	defer server.Close()

	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.Probe(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-probe-secret",
	})

	if !result.OK || result.ProbeMode != "capability" || !result.GeneratedRequest || result.ErrorCode != "" {
		t.Fatalf("capability probe failed: %#v", result)
	}
	if !result.CredentialCheck.OK || !result.ModelCheck.OK || !result.CapabilityCheck.OK {
		t.Fatalf("probe did not record each successful check: %#v", result)
	}
	if result.Usage.InputTokens != 12 || result.Usage.CachedInputTokens != 3 || result.Usage.OutputTokens != 7 ||
		result.Usage.ReasoningTokens != 2 || result.Usage.TotalTokens != 19 {
		t.Fatalf("provider usage was not preserved: %#v", result.Usage)
	}
	if result.Diagnostic.ResponseFormat != "json_object" || result.Diagnostic.FinishReason != "stop" || result.Diagnostic.ContentPreview == "" {
		t.Fatalf("structured-output evidence missing: %#v", result.Diagnostic)
	}
	if modelRequests != 1 || completionRequests != 1 || result.ConnectionDiagnostic.Attempts != 2 {
		t.Fatalf("expected one model check and one generated request, models=%d completions=%d diagnostics=%#v", modelRequests, completionRequests, result.ConnectionDiagnostic)
	}
}

func TestHTTPManagedAPIQuickProbeListsModelsWithoutGenerating(t *testing.T) {
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("quick probe sent a generated request to %s", r.URL.Path)
			http.Error(w, "unexpected generation", http.StatusBadRequest)
			return
		}
		gets++
		_, _ = io.WriteString(w, `{"data":[{"id":"z-model"},{"id":"school-model"},{"id":"z-model"},{"id":""}]}`)
	}))
	defer server.Close()

	prober := newTestHTTPManagedAPIProber(server.Client())
	connection := ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-probe-secret",
	}
	list, listed := prober.ListModels(context.Background(), connection)
	if !listed.OK || !reflect.DeepEqual(list.Models, []string{"school-model", "z-model"}) {
		t.Fatalf("model discovery did not return sorted unique models: %#v %#v", list, listed)
	}

	connection.Config.LastCapabilityStatus = "success"
	connection.Config.LastCapabilityVersion = ManagedCapabilityProbeVersion
	quick := prober.ProbeQuick(context.Background(), connection)
	if !quick.OK || quick.ProbeMode != "quick" || quick.GeneratedRequest || quick.Usage.TotalTokens != 0 ||
		!quick.CapabilityCheck.OK || quick.CapabilityCheck.Code != "reused" {
		t.Fatalf("quick probe did not reuse verified capability evidence: %#v", quick)
	}

	connection.Config.ModelName = "missing-model"
	missing := prober.ProbeQuick(context.Background(), connection)
	if missing.OK || missing.ErrorCode != "model_not_found" || missing.ModelCheck.Code != "model_not_found" {
		t.Fatalf("quick probe accepted an unlisted model: %#v", missing)
	}
	if gets != 3 || posts != 0 {
		t.Fatalf("quick checks should only list models, GETs=%d POSTs=%d", gets, posts)
	}
}

type managedCredentialFailureCase struct {
	name               string
	status             int
	modelsBody         string
	wantErrorCode      string
	wantCredentialCode string
	wantModelCode      string
}

// 凭证或模型发现失败必须在计费生成前退出，同时保留首次检查的错误归因。
func exerciseManagedCredentialFailure(t *testing.T, test managedCredentialFailureCase) {
	t.Helper()
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("probe generated before credentials and model passed: %s", r.URL.Path)
			http.Error(w, "unexpected generation", http.StatusBadRequest)
			return
		}
		gets++
		if test.status != http.StatusOK {
			w.WriteHeader(test.status)
			return
		}
		_, _ = io.WriteString(w, test.modelsBody)
	}))
	defer server.Close()

	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.Probe(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-probe-secret",
	})
	if result.OK || result.ErrorCode != test.wantErrorCode || result.GeneratedRequest || gets != 1 || posts != 0 {
		t.Fatalf("unexpected early failure: result=%#v GETs=%d POSTs=%d", result, gets, posts)
	}
	if result.CredentialCheck.Code != test.wantCredentialCode || result.ModelCheck.Code != test.wantModelCode {
		t.Fatalf("failure was attributed to the wrong check: %#v", result)
	}
}

func TestHTTPManagedAPIProberRejectsCredentialAndUnknownModelBeforeGeneration(t *testing.T) {
	tests := []managedCredentialFailureCase{
		{name: "unauthorized key", status: http.StatusUnauthorized, wantErrorCode: "credential_invalid", wantCredentialCode: "credential_invalid"},
		{name: "forbidden key", status: http.StatusForbidden, wantErrorCode: "model_permission_denied", wantCredentialCode: "model_permission_denied"},
		{name: "model missing", status: http.StatusOK, modelsBody: `{"data":[{"id":"another-model"}]}`, wantErrorCode: "model_not_found", wantModelCode: "model_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exerciseManagedCredentialFailure(t, test)
		})
	}
}

func TestHTTPManagedAPIProberFailsClosedOnInvalidStructuredCapability(t *testing.T) {
	for _, test := range []struct {
		name         string
		content      string
		finishReason string
		wantCode     string
	}{
		{name: "invalid JSON", content: "not json", finishReason: "stop", wantCode: "invalid_json"},
		{name: "schema mismatch", content: `{"question_no":1,"max_score":2}`, finishReason: "stop", wantCode: "schema_mismatch"},
		{name: "truncated response", content: `{"question_no":"1",`, finishReason: "length", wantCode: "output_truncated"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var gets, posts int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/models":
					gets++
					_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
				case "/v1/chat/completions":
					posts++
					content, _ := json.Marshal(test.content)
					_, _ = io.WriteString(w, `{"choices":[{"message":{"content":`+string(content)+`},"finish_reason":"`+test.finishReason+`"}]}`)
				default:
					t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			prober := newTestHTTPManagedAPIProber(server.Client())
			result := prober.Probe(context.Background(), ManagedAPIConnection{
				Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
				APIKey: "school-probe-secret",
			})
			if result.OK || result.ErrorCode != test.wantCode || !result.GeneratedRequest || gets != 1 || posts != 1 {
				t.Fatalf("capability failure did not fail closed: result=%#v GETs=%d POSTs=%d", result, gets, posts)
			}
			if !result.CredentialCheck.OK || !result.ModelCheck.OK || result.CapabilityCheck.OK || result.CapabilityCheck.Code != test.wantCode {
				t.Fatalf("capability failure was attributed incorrectly: %#v", result)
			}
		})
	}
}

func TestHTTPManagedAPIProberClassifiesUnsupportedJSONMode(t *testing.T) {
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			gets++
			_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
			return
		}
		posts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"response_format json_object is not supported for this model"}}`)
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.Probe(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-probe-secret",
	})
	if result.OK || result.StatusCode != http.StatusBadRequest || result.ErrorCode != "response_format_unsupported" ||
		!result.GeneratedRequest || !result.CredentialCheck.OK || !result.ModelCheck.OK ||
		result.CapabilityCheck.Code != "response_format_unsupported" || gets != 1 || posts != 1 {
		t.Fatalf("JSON mode rejection was not classified as a capability failure: result=%#v GETs=%d POSTs=%d", result, gets, posts)
	}
}

func TestHTTPManagedAPIProberRejectsMalformedOrEmptyCapabilityEnvelope(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "malformed envelope", body: `not-json`, wantCode: "provider_invalid_response"},
		{name: "empty choices", body: `{"choices":[]}`, wantCode: "no_choices"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var posts int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
					return
				}
				posts++
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			prober := newTestHTTPManagedAPIProber(server.Client())
			result := prober.Probe(context.Background(), ManagedAPIConnection{
				Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
				APIKey: "school-probe-secret",
			})
			if result.OK || result.ErrorCode != test.wantCode || !result.GeneratedRequest ||
				!result.CredentialCheck.OK || !result.ModelCheck.OK || result.CapabilityCheck.Code != test.wantCode || posts != 1 {
				t.Fatalf("malformed capability envelope was accepted or misclassified: result=%#v POSTs=%d", result, posts)
			}
		})
	}
}

func TestHTTPManagedAPIProberDoesNotReuseOlderCapabilityVersion(t *testing.T) {
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("quick probe generated instead of reporting the older evidence as not run")
			http.Error(w, "unexpected generation", http.StatusBadRequest)
			return
		}
		gets++
		_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.ProbeQuick(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{
			ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model",
			LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v2",
		},
		APIKey: "school-probe-secret",
	})
	if !result.OK || result.ProbeMode != "quick" || result.GeneratedRequest || result.CapabilityCheck.OK ||
		result.CapabilityCheck.Code != "not_run" || gets != 1 || posts != 0 {
		t.Fatalf("quick probe reused stale capability evidence: result=%#v GETs=%d POSTs=%d", result, gets, posts)
	}
}

func TestHTTPManagedAPIProberRejectsOversizedModelListBeforeGeneration(t *testing.T) {
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			t.Errorf("probe generated after receiving an invalid oversized model list")
			http.Error(w, "unexpected generation", http.StatusBadRequest)
			return
		}
		gets++
		_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}],"padding":"`+strings.Repeat("x", managedProbeResponseLimit)+`"}`)
	}))
	defer server.Close()
	prober := newTestHTTPManagedAPIProber(server.Client())
	result := prober.Probe(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-probe-secret",
	})
	if result.OK || result.ErrorCode != "provider_invalid_response" || result.GeneratedRequest || gets != 1 || posts != 0 {
		t.Fatalf("oversized model list was accepted or caused generation: result=%#v GETs=%d POSTs=%d", result, gets, posts)
	}
}

type managedProbeRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip managedProbeRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestHTTPManagedAPIProberUsesBoundedDashScopeNativeCapabilityRequest(t *testing.T) {
	var requestSeen bool
	client := &http.Client{Transport: managedProbeRoundTripper(func(r *http.Request) (*http.Response, error) {
		requestSeen = true
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) < 59*time.Second || time.Until(deadline) > 60*time.Second {
			t.Errorf("DashScope generation did not receive the independent capability budget: deadline=%v exists=%t", deadline, ok)
		}
		if r.Method != http.MethodPost || r.URL.String() != "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation" {
			t.Errorf("unexpected DashScope request: %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer dashscope-secret" {
			t.Errorf("DashScope API key was not sent as a bearer token")
		}
		var payload struct {
			Model string `json:"model"`
			Input struct {
				Messages []map[string]string `json:"messages"`
			} `json:"input"`
			Parameters map[string]any `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode DashScope request: %v", err)
		}
		if payload.Model != "qwen3.8-flash" || len(payload.Input.Messages) == 0 ||
			payload.Parameters["max_tokens"] != float64(managedCapabilityMaxTokens) ||
			payload.Parameters["result_format"] != "message" || payload.Parameters["enable_thinking"] != false {
			t.Errorf("DashScope request was not bounded or disabled thinking: %#v", payload)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
				`{"output":{"choices":[{"message":{"content":"{\"question_no\":\"1\",\"max_score\":2}"},"finish_reason":"stop"}]},"usage":{"input_tokens":4,"output_tokens":6}}`,
			)), Request: r,
		}, nil
	})}
	prober := newTestHTTPManagedAPIProber(client)
	result := prober.Probe(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "aliyun", AdapterType: "dashscope_native", BaseURL: "https://dashscope.aliyuncs.com/api/v1", ModelName: "qwen3.8-flash"},
		APIKey: "dashscope-secret",
	})
	if !requestSeen || !result.OK || result.ProbeMode != "capability" || !result.GeneratedRequest ||
		!result.CredentialCheck.OK || !result.ModelCheck.OK || !result.CapabilityCheck.OK {
		t.Fatalf("DashScope capability probe failed: seen=%t result=%#v", requestSeen, result)
	}
	if result.Usage.InputTokens != 4 || result.Usage.OutputTokens != 6 || result.Usage.TotalTokens != 10 {
		t.Fatalf("DashScope token usage was not normalized: %#v", result.Usage)
	}
}

func TestManagedAPIConfigCannotPromoteDisabledUnverifiedModelToDefault(t *testing.T) {
	store := NewMemoryStore()
	const tenantID = "00000000-0000-0000-0000-000000000020"
	item, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "school-provider", DisplayName: "School model", AdapterType: "openai_compatible",
		BaseURL: "https://models.example.test/v1", APIKey: "school-secret-value-123456", ModelName: "school-model",
		ModelVersion: "school-model-v1", Region: "global", Status: "disabled",
	})
	if err != nil {
		t.Fatal(err)
	}
	prober := &managedConfigSafetyCountingProber{}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	body := `{"display_name":"School model","adapter_type":"openai_compatible","base_url":"https://models.example.test/v1",` +
		`"model_name":"school-model","model_version":"school-model-v1","region":"global","status":"active","is_default":true}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+item.ID+"?tenant_id="+tenantID, strings.NewReader(body))
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "managed_model_capability_required") || prober.calls != 0 {
		t.Fatalf("unverified disabled model was promoted: status=%d probeCalls=%d body=%s", response.Code, prober.calls, response.Body.String())
	}
	items, err := store.ListManagedAPIConfigs(context.Background(), tenantID)
	if err != nil || len(items) != 1 || items[0].Status != "disabled" || items[0].IsDefault {
		t.Fatalf("rejected promotion changed school configuration: %#v err=%v", items, err)
	}
}

type managedConfigSafetyCountingProber struct{ calls int }

func (p *managedConfigSafetyCountingProber) Probe(context.Context, ManagedAPIConnection) ManagedAPIProbeResult {
	p.calls++
	return ManagedAPIProbeResult{OK: true}
}

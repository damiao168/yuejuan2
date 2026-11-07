package modelgovernance

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
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

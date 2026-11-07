package modelgovernance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

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

func TestHTTPManagedAPIModelDiscoveryEmptyListSerializesAsArray(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "empty data", body: `{"data":[]}`},
		{name: "null data", body: `{"data":null}`},
		{name: "blank model ids", body: `{"data":[{"id":""},{"id":"  "}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Errorf("model discovery sent unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			prober := newTestHTTPManagedAPIProber(server.Client())
			list, result := prober.ListModels(context.Background(), ManagedAPIConnection{
				Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1"},
				APIKey: "school-probe-secret",
			})
			if !result.OK || !result.CredentialCheck.OK || list.Models == nil || len(list.Models) != 0 {
				t.Fatalf("empty discovery result must be a successful non-nil list: %#v %#v", list, result)
			}

			// handler 直接序列化 Models；确认空结果输出 []，避免 HTTP 成功后触发前端渲染异常。
			body, err := json.Marshal(list)
			if err != nil || !strings.Contains(string(body), `"models":[]`) {
				t.Fatalf("empty models must serialize as an array: %s, error=%v", body, err)
			}
		})
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

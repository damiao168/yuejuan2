package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestManagedProbeMessagePreservesUTF8WithinPersistenceLimit(t *testing.T) {
	message := boundedManagedProbeMessage("连接正常，生成检测未通过：" + strings.Repeat("模型不可用", 100))
	if len(message) > 256 || !utf8.ValidString(message) || !strings.HasPrefix(message, "连接正常") {
		t.Fatalf("probe message cannot be stored as bounded UTF-8: %q", message)
	}
}

func TestHTTPManagedAPITextProbeAcceptsNaturalLanguageWithoutJSONMode(t *testing.T) {
	var gets, posts int
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			gets++
			_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			posts++
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode text request: %v", err)
			}
			if _, exists := payload["response_format"]; exists {
				t.Errorf("text generation imposed structured output: %#v", payload)
			}
			maxTokens, _ := payload["max_tokens"].(float64)
			if payload["model"] != "school-model" || payload["stream"] != false || maxTokens <= 0 || maxTokens > managedCapabilityMaxTokens {
				t.Errorf("text generation request was not bounded: %#v", payload)
			}
			// 模型没有照抄 OK 也已证明能生成文本，不能把提示词服从性当成连接要求。
			_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"模型可以正常回答。"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`)
		default:
			t.Errorf("unexpected text request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	result := newTestHTTPManagedAPIProber(server.Client()).ProbeText(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
		APIKey: "school-text-secret",
	})
	if !result.OK || result.ProbeMode != "text" || !result.GeneratedRequest || !result.TextCheck.OK ||
		result.CapabilityCheck.OK || result.CapabilityCheck.Code != "not_run" || gets != 1 || posts != 1 {
		t.Fatalf("text response did not remain separate from structured capability: %#v GETs=%d POSTs=%d", result, gets, posts)
	}
	if result.Usage.TotalTokens != 12 || result.Diagnostic.ResponseFormat != "text" {
		t.Fatalf("text result lost usage or output format: %#v", result)
	}
}

func TestHTTPManagedAPITextProbeRequiresGenerationAfterModelDiscovery(t *testing.T) {
	for _, test := range []struct {
		name, body, wantCode string
		status               int
	}{
		{name: "quota or rate limit", status: http.StatusTooManyRequests, wantCode: "provider_rate_limited"},
		{name: "empty text", status: http.StatusOK, body: `{"choices":[{"message":{"content":"  "},"finish_reason":"stop"}]}`, wantCode: "empty_content"},
		{name: "no choices", status: http.StatusOK, body: `{"choices":[]}`, wantCode: "no_choices"},
		{name: "invalid envelope", status: http.StatusOK, body: `not-json`, wantCode: "provider_invalid_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var posts int
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					_, _ = io.WriteString(w, `{"data":[{"id":"school-model"}]}`)
					return
				}
				posts++
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			result := newTestHTTPManagedAPIProber(server.Client()).ProbeText(context.Background(), ManagedAPIConnection{
				Config: ManagedAPIConfig{ProviderKey: "school-provider", AdapterType: "openai_compatible", BaseURL: server.URL + "/v1", ModelName: "school-model"},
				APIKey: "school-text-secret",
			})
			if result.OK || result.TextCheck.OK || result.ErrorCode != test.wantCode || result.TextCheck.Code != test.wantCode ||
				!result.CredentialCheck.OK || !result.ModelCheck.OK || !result.GeneratedRequest || posts != 1 {
				t.Fatalf("model discovery was mistaken for text generation: %#v POSTs=%d", result, posts)
			}
		})
	}
}

func TestHTTPManagedAPIDashScopeTextProbeDoesNotImposeJSON(t *testing.T) {
	var posts int
	client := &http.Client{Transport: managedProbeRoundTripper(func(r *http.Request) (*http.Response, error) {
		posts++
		var payload struct {
			Model      string         `json:"model"`
			Parameters map[string]any `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode native text request: %v", err)
		}
		if _, exists := payload.Parameters["response_format"]; exists {
			t.Errorf("native text generation imposed JSON mode: %#v", payload.Parameters)
		}
		maxTokens, _ := payload.Parameters["max_tokens"].(float64)
		if payload.Model != "qwen3.8-flash" || maxTokens <= 0 || maxTokens > managedCapabilityMaxTokens || payload.Parameters["result_format"] != "message" || payload.Parameters["enable_thinking"] != false {
			t.Errorf("native text request was not bounded message output: %#v", payload)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Request: r,
			Body: io.NopCloser(strings.NewReader(`{"output":{"choices":[{"message":{"content":"可以正常回答。"},"finish_reason":"stop"}]},"usage":{"input_tokens":4,"output_tokens":6}}`))}, nil
	})}
	result := newTestHTTPManagedAPIProber(client).ProbeText(context.Background(), ManagedAPIConnection{
		Config: ManagedAPIConfig{ProviderKey: "aliyun", AdapterType: "dashscope_native", BaseURL: "https://dashscope.aliyuncs.com/api/v1", ModelName: "qwen3.8-flash"},
		APIKey: "native-text-secret",
	})
	if !result.OK || result.ProbeMode != "text" || !result.TextCheck.OK || result.CapabilityCheck.OK || posts != 1 || result.Usage.TotalTokens != 10 {
		t.Fatalf("native text generation failed or claimed structured capability: %#v POSTs=%d", result, posts)
	}
}

func verifiedManagedProbeEvidence() ManagedAPIConfig {
	testedAt := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)
	return ManagedAPIConfig{
		LastTestStatus: "success", LastSuccessfulTestedAt: &testedAt,
		LastTextStatus: "success", LastTextMessage: "text verified", LastTextTestedAt: &testedAt,
		LastTextUsage:        ManagedAPIProbeUsage{TotalTokens: 9},
		LastTextDiagnostic:   ManagedAPIProbeDiagnostic{ResponseFormat: "text", ContentSHA256: "text-hash"},
		LastCapabilityStatus: "success", LastCapabilityMessage: "structured verified", LastCapabilityTestedAt: &testedAt,
		LastCapabilityVersion: "structured-json-v4", LastCapabilityUsage: ManagedAPIProbeUsage{TotalTokens: 14},
		LastCapabilityDiagnostic: ManagedAPIProbeDiagnostic{ResponseFormat: "json_object", ContentSHA256: "structured-hash"},
	}
}

func TestManagedTemporaryHTTPFailurePreservesIndependentGenerationEvidence(t *testing.T) {
	for _, mode := range []string{"quick", "text", "structured", "capability"} {
		for _, status := range []int{http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests, 500, 502, 503, 599} {
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				before := verifiedManagedProbeEvidence()
				item := before
				result := statusManagedProbeFailure(ManagedAPIProbeResult{ProbeMode: mode, GeneratedRequest: mode != "quick"}, status, "capability")
				applyManagedProbe(&item, result, time.Now().UTC())
				if item.LastTestStatus != "temporary_unavailable" || item.LastTextStatus != before.LastTextStatus ||
					item.LastTextMessage != before.LastTextMessage || item.LastTextTestedAt != before.LastTextTestedAt ||
					item.LastTextUsage != before.LastTextUsage || item.LastTextDiagnostic != before.LastTextDiagnostic ||
					item.LastCapabilityStatus != before.LastCapabilityStatus || item.LastCapabilityMessage != before.LastCapabilityMessage ||
					item.LastCapabilityTestedAt != before.LastCapabilityTestedAt || item.LastCapabilityVersion != before.LastCapabilityVersion ||
					item.LastCapabilityUsage != before.LastCapabilityUsage || item.LastCapabilityDiagnostic != before.LastCapabilityDiagnostic {
					t.Fatalf("temporary failure overwrote generation evidence: before=%#v after=%#v", before, item)
				}
				values := managedProbePersistence(&result)
				if values.Status != "temporary_unavailable" || values.UpdateCapability || values.UpdateText {
					t.Fatalf("Postgres persistence would overwrite generation evidence: %#v", values)
				}
			})
		}
	}
}

func TestManagedQuickProbeCannotCreateOrOverwriteGenerationEvidence(t *testing.T) {
	for _, initial := range []ManagedAPIConfig{{}, verifiedManagedProbeEvidence()} {
		item := initial
		result := ManagedAPIProbeResult{OK: true, ProbeMode: "quick", Message: "model listed",
			CapabilityCheck: ManagedAPICheckResult{OK: true, Code: "reused"}}
		applyManagedProbe(&item, result, time.Now().UTC())
		if item.LastTextStatus != initial.LastTextStatus || item.LastTextTestedAt != initial.LastTextTestedAt ||
			item.LastTextUsage != initial.LastTextUsage || item.LastCapabilityStatus != initial.LastCapabilityStatus ||
			item.LastCapabilityTestedAt != initial.LastCapabilityTestedAt || item.LastCapabilityUsage != initial.LastCapabilityUsage {
			t.Fatalf("model listing changed generation evidence: before=%#v after=%#v", initial, item)
		}
		values := managedProbePersistence(&result)
		if values.UpdateCapability || values.UpdateText {
			t.Fatalf("model listing requested generation evidence persistence: %#v", values)
		}
	}
}

func TestManagedTextVerificationDoesNotDependOnLatestConnectionOrJSONResult(t *testing.T) {
	for _, test := range []struct {
		name   string
		config ManagedAPIConfig
		want   bool
	}{
		{name: "model list alone", config: ManagedAPIConfig{LastTestStatus: "success", LastProbeMode: "quick"}},
		{name: "old structured result alone", config: ManagedAPIConfig{LastTestStatus: "success", LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3"}},
		{name: "text success with JSON failure", config: ManagedAPIConfig{LastTextStatus: "success", LastCapabilityStatus: "failed"}, want: true},
		{name: "text success during provider outage", config: ManagedAPIConfig{LastTextStatus: "success", LastTestStatus: "temporary_unavailable"}, want: true},
		{name: "text failure with old JSON success", config: ManagedAPIConfig{LastTextStatus: "failed", LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v4"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := managedTextVerified(test.config); got != test.want {
				t.Fatalf("text verification=%t want %t for %#v", got, test.want, test.config)
			}
		})
	}
}

func TestModelRoleBindingRequiresCurrentStructuredEvidenceAfterTextVerification(t *testing.T) {
	if ManagedCapabilityProbeVersion != "structured-json-v4" {
		t.Fatalf("structured probe meaning changed without the expected new version: %q", ManagedCapabilityProbeVersion)
	}
	for _, test := range []struct {
		name, capabilityStatus, version string
		wantError                       bool
	}{
		{name: "text only", capabilityStatus: "untested", wantError: true},
		{name: "structured failed", capabilityStatus: "failed", version: "structured-json-v4", wantError: true},
		{name: "old structured success", capabilityStatus: "success", version: "structured-json-v3", wantError: true},
		{name: "current structured success", capabilityStatus: "success", version: "structured-json-v4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			store.managedConfigs["model"] = ManagedAPIConfig{ID: "model", TenantID: "school", Status: "active", LastTextStatus: "success",
				LastCapabilityStatus: test.capabilityStatus, LastCapabilityVersion: test.version}
			_, err := store.SaveModelRoleBinding(context.Background(), "school", "actor", SaveModelRoleBindingInput{
				ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*",
				AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "model", PromptVersion: "prompt-v1", StrengthRank: 1,
			})
			if test.wantError && !errors.Is(err, ErrManagedCapabilityRequired) || !test.wantError && err != nil {
				t.Fatalf("role binding error=%v want capability required=%t", err, test.wantError)
			}
			items, listErr := store.ListConfiguredModelRoleBindings(context.Background(), "school", "senior", "physics", "*")
			if listErr != nil || test.wantError && len(items) != 0 || !test.wantError && len(items) != 1 {
				t.Fatalf("role binding persistence disagrees with admission: %#v err=%v", items, listErr)
			}
		})
	}
}

func TestModelRoleBindingStopsResolvingWhenStructuredEvidenceBecomesStale(t *testing.T) {
	store := NewMemoryStore()
	store.managedConfigs["model"] = ManagedAPIConfig{ID: "model", TenantID: "school", Status: "active", LastTextStatus: "success",
		LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v4"}
	_, err := store.SaveModelRoleBinding(context.Background(), "school", "actor", SaveModelRoleBindingInput{
		ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*",
		AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: "model", PromptVersion: "prompt-v1", StrengthRank: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := store.managedConfigs["model"]
	config.LastCapabilityVersion = "structured-json-v3"
	store.managedConfigs["model"] = config
	items, err := store.ListModelRoleBindings(context.Background(), "school", "senior", "physics", "*")
	if err != nil || len(items) != 0 {
		t.Fatalf("runtime resolved old structured evidence despite text success: %#v err=%v", items, err)
	}
	configured, err := store.ListConfiguredModelRoleBindings(context.Background(), "school", "senior", "physics", "*")
	if err != nil || len(configured) != 1 {
		t.Fatalf("stale runtime evidence deleted configured binding: %#v err=%v", configured, err)
	}
}

func TestManagedCapabilityPersistenceDoesNotStoreTextPreview(t *testing.T) {
	result := ManagedAPIProbeResult{OK: true, ProbeMode: "text", GeneratedRequest: true,
		TextCheck: ManagedAPICheckResult{OK: true}, Diagnostic: ManagedAPIProbeDiagnostic{ContentPreview: "temporary response", ContentSHA256: "response-hash"}}
	var config ManagedAPIConfig
	applyManagedProbe(&config, result, time.Now().UTC())
	if config.LastTextStatus != "success" || config.LastTextDiagnostic.ContentPreview != "" || config.LastTextDiagnostic.ContentSHA256 != "response-hash" ||
		result.Diagnostic.ContentPreview != "temporary response" {
		t.Fatalf("text preview must stay request scoped: config=%#v result=%#v", config, result)
	}
	values := managedProbePersistence(&result)
	encoded, _ := json.Marshal(values)
	if !values.UpdateText || values.TextStatus != "success" || strings.Contains(string(encoded), "temporary response") {
		t.Fatalf("text preview leaked into persistence values: %s", encoded)
	}
}

func TestManagedDefaultChatSurvivesQuickChecksAndStructuredFailure(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	text := managedTextProbeFixture(successfulManagedProbe())
	item, err := store.CreateManagedAPIConfig(ctx, "school", "actor", ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "文本模型", AdapterType: "openai_compatible", BaseURL: "https://provider.example/v1",
		APIKey: "synthetic-text-secret-123456", ModelName: "chat-model", ModelVersion: "v1", Region: "global",
		Status: "active", IsDefault: true, InitialProbe: &text,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []ManagedAPIProbeResult{
		{OK: true, ProbeMode: "quick", Message: "model listed"},
		{ProbeMode: "structured", GeneratedRequest: true, ErrorCode: "invalid_json", Message: "not JSON",
			TextCheck: ManagedAPICheckResult{OK: true}, CapabilityCheck: ManagedAPICheckResult{Code: "invalid_json"}},
		{ProbeMode: "quick", ErrorCode: "provider_timeout", Message: "temporary outage"},
	} {
		item, err = store.RecordManagedAPIProbe(ctx, "school", item.ID, item.UpdatedAt, result)
		if err != nil {
			t.Fatal(err)
		}
		connection, resolveErr := ResolveDefaultManagedAPI(ctx, store, "school")
		if resolveErr != nil || connection == nil || connection.Config.ID != item.ID || item.LastTextStatus != "success" {
			t.Fatalf("text-verified chat was disabled by %s: config=%#v connection=%#v err=%v", result.ProbeMode, item, connection, resolveErr)
		}
	}
	if item.LastCapabilityStatus != "failed" {
		t.Fatalf("structured failure was not retained independently: %#v", item)
	}
	_, err = store.SaveModelRoleBinding(ctx, "school", "actor", SaveModelRoleBindingInput{
		ExpectedRevision: ModelRoleBindingAbsent, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "*",
		AgentRole: ModelRolePrimaryA, ManagedModelAPIConfigID: item.ID, PromptVersion: "p1", StrengthRank: 1,
	})
	if !errors.Is(err, ErrManagedCapabilityRequired) {
		t.Fatalf("chat availability bypassed scoring capability gate: %v", err)
	}
}

func TestManagedTextProbeNeverFallsBackToStructuredRequest(t *testing.T) {
	store := NewMemoryStore()
	const tenant = "00000000-0000-0000-0000-000000000020"
	item, err := store.CreateManagedAPIConfig(context.Background(), tenant, "actor", ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "文本模型", AdapterType: "openai_compatible", BaseURL: "https://provider.example/v1",
		APIKey: "synthetic-text-secret-123456", ModelName: "chat-model", ModelVersion: "v1", Region: "global", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	handler := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIProber(generatedOnlyManagedProber{calls: &calls})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenant+"&mode=text", nil)
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
	response := httptest.NewRecorder()
	handler.ProbeManagedAPIConfig(response, request)
	if response.Code != http.StatusOK || calls != 0 || !strings.Contains(response.Body.String(), `"error_code":"text_probe_unsupported"`) {
		t.Fatalf("text check fell back to a structured generation: status=%d calls=%d body=%s", response.Code, calls, response.Body.String())
	}
}

type independentManagedModeProber struct {
	quick, text, structured int
}

func (p *independentManagedModeProber) Probe(context.Context, ManagedAPIConnection) ManagedAPIProbeResult {
	p.structured++
	result := successfulManagedProbe()
	result.ProbeMode, result.GeneratedRequest = "capability", true
	return result
}

func (p *independentManagedModeProber) ProbeText(context.Context, ManagedAPIConnection) ManagedAPIProbeResult {
	p.text++
	return managedTextProbeFixture(successfulManagedProbe())
}

func (p *independentManagedModeProber) ProbeQuick(context.Context, ManagedAPIConnection) ManagedAPIProbeResult {
	p.quick++
	return ManagedAPIProbeResult{OK: true, ProbeMode: "quick"}
}

func TestManagedProbeHandlerDispatchesIndependentModesAndLegacyAlias(t *testing.T) {
	for _, mode := range []string{"quick", "text", "structured", "capability"} {
		t.Run(mode, func(t *testing.T) {
			store := NewMemoryStore()
			const tenant = "00000000-0000-0000-0000-000000000020"
			item, err := store.CreateManagedAPIConfig(context.Background(), tenant, "actor", ManagedAPIConfigInput{
				ProviderKey: "custom", DisplayName: "分用途检测", AdapterType: "openai_compatible", BaseURL: "https://provider.example/v1",
				APIKey: "synthetic-modes-secret-123456", ModelName: "chat-model", ModelVersion: "v1", Region: "global", Status: "active",
			})
			if err != nil {
				t.Fatal(err)
			}
			prober := &independentManagedModeProber{}
			handler := NewHandler(store, nil, nil, testBaseline()).WithManagedAPIProber(prober)
			request := httptest.NewRequest(http.MethodPost, "/api/v1/platform/model-api-configs/"+item.ID+"/probe?tenant_id="+tenant+"&mode="+mode+"&force=true", nil)
			request.SetPathValue("id", item.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
			response := httptest.NewRecorder()
			handler.ProbeManagedAPIConfig(response, request)
			var payload struct {
				Result ManagedAPIProbeResult `json:"result"`
				Config ManagedAPIConfig      `json:"config"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload.Result.ProbeMode != mode {
				t.Fatalf("probe mode was rejected or renamed: status=%d body=%s", response.Code, response.Body.String())
			}
			wantText, wantStructured := "untested", "untested"
			switch mode {
			case "quick":
				if prober.quick != 1 || prober.text != 0 || prober.structured != 0 {
					t.Fatalf("quick mode used generation: %#v", prober)
				}
			case "text":
				wantText = "success"
				if prober.quick != 0 || prober.text != 1 || prober.structured != 0 {
					t.Fatalf("text mode used another capability: %#v", prober)
				}
			default:
				wantText, wantStructured = "success", "success"
				if prober.quick != 0 || prober.text != 0 || prober.structured != 1 {
					t.Fatalf("structured mode used another capability: %#v", prober)
				}
			}
			if payload.Config.LastTextStatus != wantText || payload.Config.LastCapabilityStatus != wantStructured {
				t.Fatalf("probe updated the wrong evidence: %#v", payload.Config)
			}
		})
	}
}

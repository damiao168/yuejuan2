package modelgovernance

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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
	body := `{"expected_updated_at":"` + item.UpdatedAt.Format(time.RFC3339Nano) + `","display_name":"School model","adapter_type":"openai_compatible","base_url":"https://models.example.test/v1",` +
		`"model_name":"school-model","model_version":"school-model-v1","region":"global","status":"active","is_default":true}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+item.ID+"?tenant_id="+tenantID, strings.NewReader(body))
	request.SetPathValue("id", item.ID)
	request = request.WithContext(auth.WithUser(request.Context(), user))
	response := httptest.NewRecorder()
	handler.UpdateManagedAPIConfig(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "managed_model_text_required") || prober.calls != 0 {
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

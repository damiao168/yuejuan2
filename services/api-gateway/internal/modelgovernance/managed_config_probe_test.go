package modelgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestManagedReadOnlyProbeRetriesNetworkFailureOnce(t *testing.T) {
	attempts := 0
	response, err := retryManagedReadOnlyRequest(context.Background(), 20*time.Millisecond, func(context.Context) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	})
	if err != nil || response.StatusCode != http.StatusOK || attempts != 2 {
		t.Fatalf("expected one network retry, attempts=%d response=%#v err=%v", attempts, response, err)
	}
	response.Body.Close()
}

func TestManagedReadOnlyProbeDoesNotRetryResponseOrCertificateError(t *testing.T) {
	attempts := 0
	response, err := retryManagedReadOnlyRequest(context.Background(), 20*time.Millisecond, func(context.Context) (*http.Response, error) {
		attempts++
		return &http.Response{StatusCode: http.StatusUnauthorized, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	if err != nil || response.StatusCode != http.StatusUnauthorized || attempts != 1 {
		t.Fatalf("HTTP response was retried: attempts=%d err=%v", attempts, err)
	}
	response.Body.Close()
	attempts = 0
	_, err = retryManagedReadOnlyRequest(context.Background(), 20*time.Millisecond, func(context.Context) (*http.Response, error) {
		attempts++
		return nil, &url.Error{Op: "Get", Err: errors.New("certificate verification failed")}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("certificate error was retried: attempts=%d err=%v", attempts, err)
	}
}

func TestManagedPublicAddressFallbackKeepsPrivateAddressGuard(t *testing.T) {
	addresses := []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}
	attempts := 0
	connection, err := dialPublicProviderAddresses(context.Background(), "tcp", "443", addresses,
		func(context.Context, string, string) (net.Conn, error) {
			attempts++
			if attempts == 1 {
				return nil, errors.New("first public IP unavailable")
			}
			client, server := net.Pipe()
			server.Close()
			return client, nil
		}, nil)
	if err != nil || attempts != 2 {
		t.Fatalf("public address fallback failed: attempts=%d err=%v", attempts, err)
	}
	connection.Close()
	attempts = 0
	_, err = dialPublicProviderAddresses(context.Background(), "tcp", "443",
		[]net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}},
		func(context.Context, string, string) (net.Conn, error) { attempts++; return nil, nil }, nil)
	if !errors.Is(err, ErrInvalidManagedConfig) || attempts != 0 {
		t.Fatalf("mixed public/private DNS answer was not rejected before dial: attempts=%d err=%v", attempts, err)
	}
}

func TestManagedProbeClassifiesProviderFailures(t *testing.T) {
	tests := []struct {
		status int
		code   string
	}{
		{http.StatusUnauthorized, "credential_invalid"},
		{http.StatusForbidden, "model_permission_denied"},
		{http.StatusNotFound, "model_not_found"},
		{http.StatusTooManyRequests, "provider_rate_limited"},
		{http.StatusBadGateway, "provider_error"},
	}
	for _, test := range tests {
		result := statusManagedProbeFailure(ManagedAPIProbeResult{Model: "test-model"}, test.status, "capability")
		if result.OK || result.ErrorCode != test.code || result.StatusCode != test.status {
			t.Fatalf("status %d classified as %#v", test.status, result)
		}
	}
}

func TestManagedCapabilityPayloadBoundsOutputAndDisablesOptionalThinking(t *testing.T) {
	deepSeek := managedCapabilityPayload(ManagedAPIConnection{Config: ManagedAPIConfig{
		ProviderKey: "deepseek", ModelName: "deepseek-v4-pro",
	}})
	if deepSeek["max_tokens"] != 1024 || deepSeek["stream"] != false {
		t.Fatalf("DeepSeek capability probe is not bounded: %#v", deepSeek)
	}
	thinking, ok := deepSeek["thinking"].(map[string]string)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("DeepSeek thinking was not disabled: %#v", deepSeek)
	}
	message := deepSeek["messages"].([]map[string]string)[0]["content"]
	if !strings.Contains(strings.ToLower(message), "json") {
		t.Fatalf("JSON output request omitted the required JSON instruction: %q", message)
	}

	qwen := managedCapabilityPayload(ManagedAPIConnection{Config: ManagedAPIConfig{
		ProviderKey: "aliyun", ModelName: "qwen3.8-flash",
	}})
	if qwen["max_tokens"] != 1024 {
		t.Fatalf("Qwen capability probe does not use the standard output limit: %#v", qwen)
	}
	if enabled, exists := qwen["enable_thinking"]; !exists || enabled != false {
		t.Fatalf("Qwen hybrid thinking was not disabled: %#v", qwen)
	}
	thinkingOnly := managedCapabilityPayload(ManagedAPIConnection{Config: ManagedAPIConfig{
		ProviderKey: "aliyun", ModelName: "qwen3-235b-a22b-thinking-2507",
	}})
	if _, exists := thinkingOnly["enable_thinking"]; exists {
		t.Fatalf("thinking-only model received an unsupported disable flag: %#v", thinkingOnly)
	}
}

func TestManagedOpenAIProbeUsagePreservesTokenBreakdown(t *testing.T) {
	var usage managedOpenAIProbeUsage
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens":21,"completion_tokens":7,"total_tokens":28,
		"prompt_tokens_details":{"cached_tokens":5},
		"completion_tokens_details":{"reasoning_tokens":2}
	}`), &usage); err != nil {
		t.Fatal(err)
	}
	normalized := usage.normalized()
	if normalized.InputTokens != 21 || normalized.CachedInputTokens != 5 || normalized.OutputTokens != 7 ||
		normalized.ReasoningTokens != 2 || normalized.TotalTokens != 28 {
		t.Fatalf("unexpected normalized usage: %#v", normalized)
	}
}

func TestManagedProbeClassifiesStructuredOutputPrecisely(t *testing.T) {
	for _, value := range []string{`{"question_no":"1","max_score":2}`, "```json\n{\"question_no\":\"1\",\"max_score\":2}\n```"} {
		if code, message := validateManagedCapabilityJSON(value, "stop"); code != "" {
			t.Fatalf("valid structured payload rejected: %q code=%s message=%s", value, code, message)
		}
	}
	tests := []struct{ content, finishReason, code string }{
		{"", "stop", "empty_content"},
		{`{"question_no":"1",`, "length", "output_truncated"},
		{`not json`, "stop", "invalid_json"},
		{`{"question_no":1,"max_score":2}`, "stop", "schema_mismatch"},
		{`{"question_no":"2","max_score":2}`, "stop", "semantic_mismatch"},
		{`{"question_no":"1","max_score":2}`, "content_filter", "content_filtered"},
	}
	for _, test := range tests {
		if code, _ := validateManagedCapabilityJSON(test.content, test.finishReason); code != test.code {
			t.Fatalf("content=%q finish=%q: got %s want %s", test.content, test.finishReason, code, test.code)
		}
	}
}

func TestManagedCapabilityDiagnosticBoundsAndSanitizesPreview(t *testing.T) {
	diagnostic := managedCapabilityDiagnostic(strings.Repeat("甲", 300)+"\x00", "stop", "json_object")
	if diagnostic.ContentLength != 301 || strings.ContainsRune(diagnostic.ContentPreview, '\x00') || len([]rune(diagnostic.ContentPreview)) != 257 {
		t.Fatalf("unexpected diagnostic: %#v", diagnostic)
	}
}

func TestManagedProbeEndpointAndPublicIPGuards(t *testing.T) {
	endpoint, err := managedCompletionEndpoint("https://api.example.com/v1/")
	if err != nil || endpoint != "https://api.example.com/v1/chat/completions" {
		t.Fatalf("unexpected endpoint %q: %v", endpoint, err)
	}
	if _, err = managedModelsEndpoint("https://api.example.com/v1?tenant=other"); err == nil {
		t.Fatal("endpoint with query string was accepted")
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "::1"} {
		if publicProviderIP(net.ParseIP(value)) {
			t.Fatalf("non-public address was accepted: %s", value)
		}
	}
	if !publicProviderIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address was rejected")
	}
}

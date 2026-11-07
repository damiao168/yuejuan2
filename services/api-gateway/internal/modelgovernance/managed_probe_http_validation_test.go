package modelgovernance

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

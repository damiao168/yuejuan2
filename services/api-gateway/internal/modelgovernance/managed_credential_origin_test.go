package modelgovernance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestManagedCredentialReuseRequiresSameHTTPSOrigin(t *testing.T) {
	for _, test := range []struct {
		target string
		want   bool
	}{
		{"https://provider.example/", true},
		{"https://provider.example/v2/", true},
		{"https://PROVIDER.example:443/v2", true},
		{"https://provider.example:8443/v1", false},
		{"https://other.example/v1", false},
		{"https://provider.example.attacker.test/v1", false},
		{"https://sub.provider.example/v1", false},
		{"http://provider.example/v1", false},
		{"https://user@provider.example/v1", false},
		{"https://provider.example/v1?token=value", false},
		{"https://provider.example/v1#fragment", false},
		{"not a URL", false},
	} {
		t.Run(test.target, func(t *testing.T) {
			if got := sameManagedAPIOrigin("https://provider.example/v1", test.target); got != test.want {
				t.Fatalf("same origin=%t, want=%t", got, test.want)
			}
		})
	}
}

type originManagedProber struct {
	seen   []ManagedAPIConnection
	result *ManagedAPIProbeResult
}

func (p *originManagedProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	p.seen = append(p.seen, connection)
	if p.result != nil {
		return *p.result
	}
	return successfulManagedProbe()
}

func (p *originManagedProber) ProbeQuick(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return p.Probe(ctx, connection)
}

func (p *originManagedProber) ListModels(_ context.Context, connection ManagedAPIConnection) (ManagedAPIModelListResult, ManagedAPIProbeResult) {
	p.seen = append(p.seen, connection)
	return ManagedAPIModelListResult{Models: []string{"school-model"}}, successfulManagedProbe()
}

func TestManagedModelDiscoveryReusesSavedKeyForSameOriginPath(t *testing.T) {
	store := NewMemoryStore()
	const tenantID = "00000000-0000-0000-0000-000000000020"
	const secret = "synthetic-origin-key-123456"
	source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
		ProviderKey: "custom", DisplayName: "School API", AdapterType: "openai_compatible",
		BaseURL: "https://provider.example/v1", APIKey: secret, ModelName: "school-model",
		ModelVersion: "school-model", Region: "custom", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	prober := &originManagedProber{}
	handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
	user := auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}
	for _, test := range []struct {
		target string
		status int
	}{
		{"https://provider.example/", http.StatusOK},
		{"https://PROVIDER.example:443/v2", http.StatusOK},
		{"https://other.example/v1", http.StatusBadRequest},
		{"https://provider.example:8443/v1", http.StatusBadRequest},
	} {
		t.Run(test.target, func(t *testing.T) {
			before := len(prober.seen)
			body, _ := json.Marshal(AutoManagedAPIConfigInput{TenantID: tenantID, CredentialSourceID: source.ID, Provider: "custom", BaseURL: test.target})
			response := performHandlerRequest(t, user, http.MethodPost, "/api/v1/platform/model-api-configs/models", string(body), handler.ListAvailableManagedAPIModels)
			if response.Code != test.status || strings.Contains(response.Body.String(), secret) {
				t.Fatalf("unexpected discovery response: %d %s", response.Code, response.Body.String())
			}
			if test.status == http.StatusOK {
				if len(prober.seen) != before+1 || prober.seen[before].APIKey != secret || prober.seen[before].Config.BaseURL != strings.TrimRight(test.target, "/") {
					t.Fatal("discovery must use the saved key at the edited path")
				}
			} else if len(prober.seen) != before || !strings.Contains(response.Body.String(), "managed_model_credential_origin_changed") {
				t.Fatal("cross-origin discovery must stop before sending the saved key")
			}
		})
	}
	connection, err := store.GetManagedAPIConnection(context.Background(), tenantID, source.ID)
	if err != nil || connection.Config.BaseURL != source.BaseURL || connection.APIKey != secret {
		t.Fatal("discovery must not change the saved connection")
	}
}

func TestManagedUpdateKeepsKeyForPathChangeAndRequiresInputForNewOrigin(t *testing.T) {
	for _, test := range []struct {
		name         string
		target       string
		explicitKey  string
		providerFail bool
		status       int
	}{
		{name: "same origin path", target: "https://provider.example/", status: http.StatusOK},
		{name: "failed path check preserves previous connection", target: "https://provider.example/v2", providerFail: true, status: http.StatusUnprocessableEntity},
		{name: "new domain without input", target: "https://other.example/v1", status: http.StatusBadRequest},
		{name: "new port without input", target: "https://provider.example:8443/v1", status: http.StatusBadRequest},
		{name: "new domain can explicitly reuse original key", target: "https://other.example/v1", explicitKey: "synthetic-origin-key-123456", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			const tenantID = "00000000-0000-0000-0000-000000000020"
			const secret = "synthetic-origin-key-123456"
			verified := successfulManagedProbe()
			verified.ProbeMode, verified.GeneratedRequest = "structured", true
			source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
				ProviderKey: "custom", DisplayName: "School API", AdapterType: "openai_compatible",
				BaseURL: "https://provider.example/v1", APIKey: secret, ModelName: "school-model",
				ModelVersion: "school-model", Region: "custom", Status: "active", InitialProbe: &verified,
			})
			if err != nil {
				t.Fatal(err)
			}
			result := successfulManagedProbe()
			if test.providerFail {
				result.OK, result.ErrorCode = false, "model_not_found"
			}
			prober := &originManagedProber{result: &result}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
			input := ManagedAPIConfigUpdateInput{DisplayName: source.DisplayName, AdapterType: source.AdapterType,
				BaseURL: test.target, APIKey: test.explicitKey, ModelName: source.ModelName, ModelVersion: source.ModelVersion,
				Region: source.Region, Status: source.Status}
			body, _ := json.Marshal(input)
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+source.ID+"?tenant_id="+tenantID, strings.NewReader(string(body)))
			request.SetPathValue("id", source.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
			response := httptest.NewRecorder()
			handler.UpdateManagedAPIConfig(response, request)
			if response.Code != test.status || strings.Contains(response.Body.String(), secret) {
				t.Fatalf("unexpected update response: %d %s", response.Code, response.Body.String())
			}
			connection, err := store.GetManagedAPIConnection(context.Background(), tenantID, source.ID)
			if err != nil || connection.APIKey != secret || connection.Config.CredentialHint != source.CredentialHint {
				t.Fatal("the original credential must be retained")
			}
			if test.status == http.StatusOK {
				if len(prober.seen) != 1 || prober.seen[0].APIKey != secret || prober.seen[0].Config.BaseURL != strings.TrimRight(test.target, "/") ||
					connection.Config.BaseURL != strings.TrimRight(test.target, "/") ||
					connection.Config.LastTestStatus != "success" || connection.Config.LastTextStatus != "untested" || connection.Config.LastCapabilityStatus != "untested" {
					t.Fatal("new path must be checked without inheriting previous text or structured evidence")
				}
			} else if connection.Config.BaseURL != source.BaseURL || !connection.Config.UpdatedAt.Equal(source.UpdatedAt) {
				t.Fatal("a rejected update must leave the saved connection unchanged")
			}
			if test.status == http.StatusBadRequest && (len(prober.seen) != 0 || !strings.Contains(response.Body.String(), "managed_model_credential_origin_changed")) {
				t.Fatal("cross-origin update must request explicit input before probing")
			}
		})
	}
}

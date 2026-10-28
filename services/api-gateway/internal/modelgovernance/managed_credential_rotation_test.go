package modelgovernance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type rotatingCredentialProber struct {
	originManagedProber
	duringProbe func()
}

func (p *rotatingCredentialProber) ProbeQuick(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	if p.duringProbe != nil {
		p.duringProbe()
	}
	return p.originManagedProber.ProbeQuick(ctx, connection)
}

func TestCredentialOnlyRotationUsesCurrentFieldsAndRejectsStaleVersions(t *testing.T) {
	for _, scenario := range []string{"current", "stale before probe", "changed during probe", "missing version", "missing key"} {
		t.Run(scenario, func(t *testing.T) {
			const tenantID = "00000000-0000-0000-0000-000000000020"
			const originalKey = "synthetic-original-secret-123456"
			const newKey = "synthetic-replacement-secret-123456"
			store := NewMemoryStore()
			source, err := store.CreateManagedAPIConfig(context.Background(), tenantID, "actor", ManagedAPIConfigInput{
				ProviderKey: "custom", DisplayName: "School API", AdapterType: "openai_compatible", BaseURL: "https://provider.example/v1",
				ModelName: "school-model", ModelVersion: "v1", Region: "global", APIKey: originalKey, Status: "active",
			})
			if err != nil {
				t.Fatal(err)
			}
			input := ManagedAPIConfigUpdateInput{CredentialOnly: true, ClientExpectedUpdatedAt: source.UpdatedAt.Format(time.RFC3339Nano), APIKey: newKey,
				DisplayName: "ignored old form", BaseURL: "https://unrelated.example/v9", ModelName: "ignored-model", Status: "disabled", IsDefault: true}
			prober := &rotatingCredentialProber{}
			if scenario == "stale before probe" {
				input.ClientExpectedUpdatedAt = source.UpdatedAt.Add(-time.Second).Format(time.RFC3339Nano)
			}
			if scenario == "missing version" {
				input.ClientExpectedUpdatedAt = ""
			}
			if scenario == "missing key" {
				input.APIKey = ""
			}
			if scenario == "changed during probe" {
				prober.duringProbe = func() {
					// 连接检测等待时另一位管理员改名；迟到轮换必须保留该修改和原密钥。
					_, mutationErr := store.UpdateManagedAPIConfig(context.Background(), tenantID, source.ID, ManagedAPIConfigUpdateInput{
						DisplayName: "New name", AdapterType: source.AdapterType, BaseURL: source.BaseURL, ModelName: source.ModelName,
						ModelVersion: source.ModelVersion, Region: source.Region, Status: source.Status, ExpectedUpdatedAt: source.UpdatedAt,
					})
					if mutationErr != nil {
						t.Fatal(mutationErr)
					}
				}
			}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(prober)
			body, _ := json.Marshal(input)
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+source.ID+"?tenant_id="+tenantID, strings.NewReader(string(body)))
			request.SetPathValue("id", source.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
			response := httptest.NewRecorder()
			handler.UpdateManagedAPIConfig(response, request)
			connection, err := store.GetManagedAPIConnection(context.Background(), tenantID, source.ID)
			if err != nil || strings.Contains(response.Body.String(), originalKey) || strings.Contains(response.Body.String(), newKey) {
				t.Fatal("rotation must keep credentials out of the response")
			}
			if scenario == "current" {
				if response.Code != http.StatusOK || connection.APIKey != newKey || connection.Config.DisplayName != source.DisplayName || connection.Config.BaseURL != source.BaseURL || connection.Config.ModelName != source.ModelName || connection.Config.IsDefault || connection.Config.Status != "active" {
					t.Fatal("credential-only rotation must preserve the server connection and usage fields")
				}
			} else {
				if response.Code == http.StatusOK || connection.APIKey != originalKey {
					t.Fatal("rejected rotation must keep the original credential")
				}
				if scenario == "changed during probe" && connection.Config.DisplayName != "New name" {
					t.Fatal("stale rotation must not overwrite a concurrent change")
				}
				if scenario != "changed during probe" && len(prober.seen) != 0 {
					t.Fatal("invalid or stale input must stop before sending the credential to the provider")
				}
			}
		})
	}
}

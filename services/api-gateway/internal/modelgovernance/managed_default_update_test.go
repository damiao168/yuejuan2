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

// 版本和区域变更会使原能力证据失效；默认模型必须先验证新连接，失败保留原可用配置。
// 改名对照保护纯展示更新，不使用供应商网络或真实密钥。
func TestManagedDefaultConnectionMetadataIsValidatedBeforeSave(t *testing.T) {
	for _, test := range []struct {
		name, field string
		fail        bool
	}{
		{"version_success", "version", false}, {"region_success", "region", false},
		{"version_failure", "version", true}, {"region_failure", "region", true},
		{"display_name_control", "display_name", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const tenant = "00000000-0000-0000-0000-000000000020"
			store := NewMemoryStore()
			initial := successfulManagedProbe()
			initial.ProbeMode, initial.GeneratedRequest = "capability", true
			current, err := store.CreateManagedAPIConfig(ctx, tenant, "actor", ManagedAPIConfigInput{
				ProviderKey: "metadata-provider", DisplayName: "合成模型", AdapterType: "openai_compatible",
				BaseURL: "https://metadata.example.test/v1", APIKey: "synthetic-metadata-key-123456",
				ModelName: "metadata-model", ModelVersion: "v1", Region: "global", Status: "active", IsDefault: true, InitialProbe: &initial,
			})
			if err != nil {
				t.Fatal(err)
			}
			probe := &defaultMetadataUpdateProber{fail: test.fail}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(probe)
			input := ManagedAPIConfigUpdateInput{DisplayName: current.DisplayName, AdapterType: current.AdapterType,
				BaseURL: current.BaseURL, ModelName: current.ModelName, ModelVersion: current.ModelVersion,
				Region: current.Region, Status: current.Status, IsDefault: true}
			switch test.field {
			case "version":
				input.ModelVersion = "v2"
			case "region":
				input.Region = "cn-review"
			default:
				input.DisplayName = "合成模型改名"
			}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/platform/model-api-configs/"+current.ID+"?tenant_id="+tenant, strings.NewReader(string(raw)))
			request.SetPathValue("id", current.ID)
			request = request.WithContext(auth.WithUser(request.Context(), auth.User{ID: "actor", TenantID: auth.PlatformTenantID, Permissions: []string{"model:provider:manage"}}))
			response := httptest.NewRecorder()
			handler.UpdateManagedAPIConfig(response, request)
			wantStatus, wantCalls := http.StatusOK, 1
			if test.fail {
				wantStatus = http.StatusUnprocessableEntity
			}
			if test.field == "display_name" {
				wantCalls = 0
			}
			if response.Code != wantStatus || probe.calls != wantCalls {
				t.Fatalf("update status=%d calls=%d; want status=%d calls=%d; body=%s", response.Code, probe.calls, wantStatus, wantCalls, response.Body.String())
			}
			if wantCalls == 1 && (probe.seen.Config.ModelVersion != input.ModelVersion || probe.seen.Config.Region != input.Region || probe.seen.APIKey != "synthetic-metadata-key-123456") {
				t.Fatalf("probe did not use the proposed connection identity: %#v", probe.seen.Config)
			}
			stored, err := store.GetManagedAPIConnection(ctx, tenant, current.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantVersion, wantRegion, wantName := input.ModelVersion, input.Region, input.DisplayName
			if test.fail {
				wantVersion, wantRegion, wantName = current.ModelVersion, current.Region, current.DisplayName
				if !stored.Config.UpdatedAt.Equal(current.UpdatedAt) {
					t.Fatal("failed validation changed the saved revision")
				}
			}
			resolved, err := ResolveDefaultManagedAPI(ctx, store, tenant)
			if err != nil || resolved == nil || !stored.Config.IsDefault || stored.Config.LastTextStatus != "success" || stored.Config.ModelVersion != wantVersion || stored.Config.Region != wantRegion || stored.Config.DisplayName != wantName {
				t.Fatalf("default connection became unavailable or saved incorrect metadata: %#v err=%v", stored.Config, err)
			}
		})
	}
}

type defaultMetadataUpdateProber struct {
	calls int
	fail  bool
	seen  ManagedAPIConnection
}

func (p *defaultMetadataUpdateProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	p.calls++
	p.seen = connection
	result := successfulManagedProbe()
	result.ProbeMode, result.GeneratedRequest = "capability", true
	if p.fail {
		result.OK = false
		result.CapabilityCheck.OK = false
		result.ErrorCode = "model_not_found"
	}
	return result
}

func (p *defaultMetadataUpdateProber) ProbeText(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return managedTextProbeFixture(p.Probe(ctx, connection))
}

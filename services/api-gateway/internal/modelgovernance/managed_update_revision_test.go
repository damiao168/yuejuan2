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

const reviewManagedOldKey = "synthetic-review-old-key-123456"
const reviewManagedNewKey = "synthetic-review-new-key-123456"

// 使用同源的两个路径，避免跨域密钥输入规则提前拦截；本用例专门验证检测与轮换的竞争。
const reviewManagedOldURL = "https://review.example.test/v1"
const reviewManagedNewURL = "https://review.example.test/v2"

type reviewManagedProbeBarrier struct {
	started chan ManagedAPIConnection
	release chan struct{}
}

func (p *reviewManagedProbeBarrier) Probe(ctx context.Context, c ManagedAPIConnection) ManagedAPIProbeResult {
	if p.started != nil && c.Config.BaseURL == reviewManagedNewURL && c.APIKey == reviewManagedOldKey {
		p.started <- c
		select {
		case <-p.release:
		case <-ctx.Done():
			return ManagedAPIProbeResult{ErrorCode: "provider_timeout", ProbeMode: "capability"}
		}
	}
	// 合成提供方的两个连接各自有效；新端点与新密钥的组合明确无效。
	valid := c.Config.BaseURL == reviewManagedOldURL || c.APIKey == reviewManagedOldKey
	result := ManagedAPIProbeResult{OK: valid, ProbeMode: "capability", GeneratedRequest: true,
		CredentialCheck: ManagedAPICheckResult{OK: valid}, ModelCheck: ManagedAPICheckResult{OK: valid},
		TextCheck:       ManagedAPICheckResult{OK: valid},
		CapabilityCheck: ManagedAPICheckResult{OK: valid}, StatusCode: 200}
	if !valid {
		result.ErrorCode = "credential_invalid"
		result.Message = "合成连接组合无效"
	}
	return result
}

func (p *reviewManagedProbeBarrier) ProbeText(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return managedTextProbeFixture(p.Probe(ctx, connection))
}

func reviewManagedUpdate(t *testing.T, h *Handler, tenant, id string, input ManagedAPIConfigUpdateInput) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/v1/platform/model-api-configs/"+id+"?tenant_id="+tenant, strings.NewReader(string(body)))
	request.SetPathValue("id", id)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{
		ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID,
		Roles: []string{"platform_admin"}, Permissions: []string{"model:provider:manage"},
	}))
	response := httptest.NewRecorder()
	h.UpdateManagedAPIConfig(response, request)
	return response
}

// 两个真实 Handler 更新都先完成能力探测；A 探测旧密钥时等待 B 轮换，再释放 A。
// 迟到A必须返回冲突并保留B；顺序正对照仍检测拒绝无效组合，不使用外部网络。
func TestManagedUpdateRejectsProbeForEarlierCredentialRevision(t *testing.T) {
	for _, interleave := range []bool{true, false} {
		name := "sequential_rejects_invalid_effective_connection"
		if interleave {
			name = "interleaved_rejects_stale_probe"
		}
		t.Run(name, func(t *testing.T) {
			store := NewMemoryStore()
			ctx := context.Background()
			const tenant = "00000000-0000-0000-0000-000000000020"
			initial := (&reviewManagedProbeBarrier{}).Probe(ctx, ManagedAPIConnection{Config: ManagedAPIConfig{BaseURL: reviewManagedOldURL}, APIKey: reviewManagedOldKey})
			config, err := store.CreateManagedAPIConfig(ctx, tenant, "actor", ManagedAPIConfigInput{
				ProviderKey: "review-provider", DisplayName: "合成模型", AdapterType: "openai_compatible",
				BaseURL: reviewManagedOldURL, APIKey: reviewManagedOldKey, ModelName: "review-model",
				ModelVersion: "v1", Region: "global", Status: "active", IsDefault: true, InitialProbe: &initial,
			})
			if err != nil {
				t.Fatal(err)
			}
			if config.LastCapabilityStatus != "success" {
				t.Fatal("initial fixture must be verified")
			}
			probe := &reviewManagedProbeBarrier{}
			handler := NewHandler(store, nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline()).WithManagedAPIProber(probe)
			a := ManagedAPIConfigUpdateInput{DisplayName: config.DisplayName, AdapterType: config.AdapterType,
				BaseURL: reviewManagedNewURL, ModelName: config.ModelName, ModelVersion: config.ModelVersion,
				Region: config.Region, Status: "active", IsDefault: true}
			b := a
			b.BaseURL, b.APIKey = reviewManagedOldURL, reviewManagedNewKey
			var pending <-chan *httptest.ResponseRecorder
			if interleave {
				probe.started, probe.release = make(chan ManagedAPIConnection, 1), make(chan struct{})
				responses := make(chan *httptest.ResponseRecorder, 1)
				pending = responses
				go func() { responses <- reviewManagedUpdate(t, handler, tenant, config.ID, a) }()
				select {
				case started := <-probe.started:
					if started.APIKey != reviewManagedOldKey {
						t.Fatal("A must probe original synthetic credential")
					}
				case <-time.After(5 * time.Second):
					close(probe.release)
					t.Fatal("A did not reach the probe barrier")
				}
			}
			rotated := reviewManagedUpdate(t, handler, tenant, config.ID, b)
			if rotated.Code != http.StatusOK {
				if interleave {
					close(probe.release)
				}
				t.Fatalf("B rotation status=%d", rotated.Code)
			}
			var response *httptest.ResponseRecorder
			if interleave {
				close(probe.release)
				select {
				case response = <-pending:
				case <-time.After(5 * time.Second):
					t.Fatal("A did not finish")
				}
			} else {
				response = reviewManagedUpdate(t, handler, tenant, config.ID, a)
			}
			connection, err := ResolveDefaultManagedAPI(ctx, store, tenant)
			if err != nil || connection == nil {
				t.Fatalf("default connection resolve failed: %v", err)
			}
			if connection.APIKey != reviewManagedNewKey {
				t.Fatal("B synthetic credential was not retained")
			}
			if interleave {
				if response.Code != http.StatusConflict || connection.Config.BaseURL != reviewManagedOldURL || connection.Config.LastTextStatus != "success" || !strings.Contains(response.Body.String(), "managed_model_probe_stale") {
					t.Fatalf("stale probe must preserve B, status=%d url=%s text=%s body=%s", response.Code, connection.Config.BaseURL, connection.Config.LastTextStatus, response.Body.String())
				}
				if !(&reviewManagedProbeBarrier{}).Probe(ctx, *connection).OK {
					t.Fatal("retained B connection must remain verified")
				}
				t.Log("A=409 B=200; stale probe rejected without replacing B's verified connection")
			} else {
				if response.Code != http.StatusUnprocessableEntity || connection.Config.BaseURL != reviewManagedOldURL {
					t.Fatalf("sequential probe must reject invalid combination and retain B, status=%d", response.Code)
				}
				t.Log("B=200 then A=422; effective credential was rechecked and B's verified connection retained")
			}
		})
	}
}

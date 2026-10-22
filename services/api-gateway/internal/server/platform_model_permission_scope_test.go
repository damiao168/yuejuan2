package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
)

// 真实登录和路由覆盖细分/旧权限，错误操作权限、非平台管理员和跨校普通用户均拒绝。
// 每例使用可成功执行的学校输入，不能把非法输入的拒绝当作权限保证。
func TestPlatformModelFinePermissionsSelectOnlyAuthorizedSchoolOperation(t *testing.T) {
	const school = "00000000-0000-0000-0000-000000000020"
	for _, action := range []string{"read", "managed", "panel"} {
		for _, mode := range []string{"fine", "legacy", "wrong_operation", "missing_permission", "missing_role", "school_user"} {
			permission := map[string]string{"read": "model:read", "managed": "model:managed_api:manage", "panel": "model:panel:manage"}[action]
			name := action + "_" + mode
			want := http.StatusOK
			actorTenant, tenantCode := auth.PlatformTenantID, "platform"
			roles := []string{"platform_admin"}
			switch mode {
			case "legacy":
				permission = "model:config:manage"
			case "wrong_operation":
				permission = "model:read"
				if action == "read" {
					permission = "model:panel:manage"
				}
				want = http.StatusForbidden
			case "missing_permission":
				permission = "unrelated:read"
				want = http.StatusForbidden
			case "missing_role":
				roles = []string{"teacher"}
				want = http.StatusForbidden
			case "school_user":
				actorTenant, tenantCode = "00000000-0000-0000-0000-000000000030", "other-school"
				want = http.StatusForbidden
			}
			t.Run(name, func(t *testing.T) {
				stores := NewMemoryApplicationStores()
				identity := stores.Identity.Auth.(*auth.MemoryStore)
				passwordHash, err := auth.HashPassword("Synthetic review password 2026!")
				if err != nil {
					t.Fatal(err)
				}
				identity.AddUser(auth.UserWithPassword{User: auth.User{
					ID: "00000000-0000-0000-0000-000000000010", TenantID: actorTenant,
					TenantCode: tenantCode, Username: "review_model_admin", Status: "active", Roles: roles,
					Permissions: []string{permission}, DataScope: map[string]any{"scope": "platform"},
				}, PasswordHash: passwordHash})
				models := stores.AIGovernance.ModelGovernance.(*modelgovernance.MemoryStore)
				probe := modelgovernance.ManagedAPIProbeResult{OK: true, ProbeMode: "capability", GeneratedRequest: true,
					CredentialCheck: modelgovernance.ManagedAPICheckResult{OK: true}, ModelCheck: modelgovernance.ManagedAPICheckResult{OK: true},
					CapabilityCheck: modelgovernance.ManagedAPICheckResult{OK: true}}
				item, err := models.CreateManagedAPIConfig(context.Background(), school, "actor", modelgovernance.ManagedAPIConfigInput{
					ProviderKey: "review-provider", DisplayName: "合成模型", AdapterType: "openai_compatible", BaseURL: "https://review.example.test/v1",
					APIKey: "synthetic-review-key-123456", ModelName: "review-model", ModelVersion: "v1", Region: "global", Status: "active", InitialProbe: &probe,
				})
				if err != nil {
					t.Fatal(err)
				}
				router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
				login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", strings.NewReader(`{"tenant_code":"`+tenantCode+`","username":"review_model_admin","password":"Synthetic review password 2026!"}`))
				loginResponse := httptest.NewRecorder()
				router.ServeHTTP(loginResponse, login)
				if loginResponse.Code != http.StatusOK {
					t.Fatalf("fixture login failed: %d %s", loginResponse.Code, loginResponse.Body.String())
				}
				var session struct {
					AccessToken string `json:"access_token"`
				}
				if err := json.Unmarshal(loginResponse.Body.Bytes(), &session); err != nil {
					t.Fatal(err)
				}
				method, path, body := http.MethodGet, "/api/v1/platform/model-api-configs?tenant_id="+school, ""
				if action == "managed" {
					method, path = http.MethodPatch, "/api/v1/platform/model-api-configs/"+item.ID+"?tenant_id="+school
					raw, err := json.Marshal(modelgovernance.ManagedAPIConfigUpdateInput{DisplayName: "改名对照", AdapterType: item.AdapterType,
						BaseURL: item.BaseURL, ModelName: item.ModelName, ModelVersion: item.ModelVersion, Region: item.Region, Status: "active"})
					if err != nil {
						t.Fatal(err)
					}
					body = string(raw)
				} else if action == "panel" {
					method, path = http.MethodPut, "/api/v1/platform/panel-model-bindings"
					raw, err := json.Marshal(map[string]any{"tenant_id": school, "expected_revision": "absent", "education_stage": "senior",
						"subject_code": "mathematics", "archetype_code": "*", "agent_role": "primary_a", "managed_model_api_config_id": item.ID,
						"prompt_version": "review-v1", "strength_rank": 1, "status": "active"})
					if err != nil {
						t.Fatal(err)
					}
					body = string(raw)
				}
				request := httptest.NewRequest(method, path, strings.NewReader(body))
				request.Header.Set("Authorization", "Bearer "+session.AccessToken)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != want {
					t.Fatalf("permission=%s expected=%d got=%d body=%s", permission, want, response.Code, response.Body.String())
				}
				t.Logf("permission=%s target school route status=%d", permission, response.Code)
			})
		}
	}
}

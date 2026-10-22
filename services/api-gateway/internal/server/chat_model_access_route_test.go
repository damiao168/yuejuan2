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

const chatAccessRouteSchool = "00000000-0000-0000-0000-000000000020"

// 使用真实登录和注册路由，防止仅调用 handler 的测试遗漏外层细分权限和平台身份检查。
// 拒绝场景也提交同一份可成功保存的输入，不能把模型资格错误当成权限边界。
func TestChatModelAccessRoutesRequirePlatformIdentityAndOperationPermission(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, mode := range []string{"fine", "legacy", "wrong_operation", "panel_only", "missing_permission", "missing_role", "school_own", "school_cross", "school_with_platform_role", "unauthenticated"} {
			t.Run(method+"_"+mode, func(t *testing.T) {
				permission := "model:read"
				if method == http.MethodPut {
					permission = "model:managed_api:manage"
				}
				user := auth.User{ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID, TenantCode: "platform", Username: "chat_access_admin", Status: "active", Roles: []string{"platform_admin"}, DataScope: map[string]any{"scope": "platform"}}
				want := http.StatusOK
				switch mode {
				case "legacy":
					permission = "model:config:manage"
				case "wrong_operation":
					permission = "model:managed_api:manage"
					if method == http.MethodPut {
						permission = "model:read"
					}
					want = http.StatusForbidden
				case "panel_only":
					permission = "model:panel:manage"
					want = http.StatusForbidden
				case "missing_permission":
					permission = "unrelated:read"
					want = http.StatusForbidden
				case "missing_role":
					user.Roles = []string{"teacher"}
					want = http.StatusForbidden
				case "school_own", "school_cross", "school_with_platform_role":
					user.TenantID, user.TenantCode = chatAccessRouteSchool, "school"
					user.Roles = []string{"school_admin"}
					user.DataScope = map[string]any{"scope": "school", "school_id": chatAccessRouteSchool}
					if mode == "school_cross" {
						user.TenantID, user.TenantCode = "00000000-0000-0000-0000-000000000030", "other-school"
					} else if mode == "school_with_platform_role" {
						user.Roles = []string{"platform_admin"}
					}
					want = http.StatusForbidden
				case "unauthenticated":
					want = http.StatusUnauthorized
				}
				user.Permissions = []string{permission}
				router, models, token, configID := chatAccessRouteFixture(t, user)
				path, body := "/api/v1/platform/chat-model-access?tenant_id="+chatAccessRouteSchool, ""
				if method == http.MethodPut {
					path = "/api/v1/platform/chat-model-access"
					body = `{"tenant_id":"` + chatAccessRouteSchool + `","expected_revision":"absent","model_ids":["` + configID + `"]}`
				}
				if mode == "unauthenticated" {
					token = ""
				}
				response := chatAccessRouteRequest(router, token, method, path, body)
				if response.Code != want {
					t.Fatalf("expected %d, got %d: %s", want, response.Code, response.Body.String())
				}
				access, err := models.GetChatModelAccess(t.Context(), chatAccessRouteSchool)
				if err != nil {
					t.Fatal(err)
				}
				if method == http.MethodPut && want == http.StatusOK {
					if access.Revision == "absent" || len(access.ModelIDs) != 1 || access.ModelIDs[0] != configID {
						t.Fatalf("authorized route did not save: %+v", access)
					}
				} else if access.Revision != "absent" {
					t.Fatalf("read or rejected route changed policy: %+v", access)
				}
			})
		}
	}
}

func TestChatModelAccessRoutesValidateTargetSchoolAndRevision(t *testing.T) {
	user := auth.User{ID: "00000000-0000-0000-0000-000000000010", TenantID: auth.PlatformTenantID, TenantCode: "platform", Username: "chat_access_admin", Status: "active", Roles: []string{"platform_admin"}, Permissions: []string{"model:read", "model:managed_api:manage"}, DataScope: map[string]any{"scope": "platform"}}
	router, models, token, configID := chatAccessRouteFixture(t, user)
	for _, target := range []string{"", auth.PlatformTenantID, "not-a-uuid"} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			path, body := "/api/v1/platform/chat-model-access?tenant_id="+target, ""
			if method == http.MethodPut {
				path, body = "/api/v1/platform/chat-model-access", `{"tenant_id":"`+target+`","expected_revision":"absent","model_ids":[]}`
			}
			if response := chatAccessRouteRequest(router, token, method, path, body); response.Code != http.StatusBadRequest {
				t.Fatalf("target %q %s: %d %s", target, method, response.Code, response.Body.String())
			}
		}
	}
	// 平台可指定其他学校，但不能把甲校连接授予乙校；此处用合法乙校 ID 防止只测到格式校验。
	foreign := "00000000-0000-0000-0000-000000000030"
	body := `{"tenant_id":"` + foreign + `","expected_revision":"absent","model_ids":["` + configID + `"]}`
	if response := chatAccessRouteRequest(router, token, http.MethodPut, "/api/v1/platform/chat-model-access", body); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("cross-school grant: %d %s", response.Code, response.Body.String())
	}
	access, err := models.GetChatModelAccess(t.Context(), foreign)
	if err != nil || access.Revision != "absent" || len(access.ModelIDs) != 0 {
		t.Fatalf("cross-school grant changed access: %+v %v", access, err)
	}
	body = `{"tenant_id":"` + chatAccessRouteSchool + `","expected_revision":"absent","model_ids":["` + configID + `"]}`
	if response := chatAccessRouteRequest(router, token, http.MethodPut, "/api/v1/platform/chat-model-access", body); response.Code != http.StatusOK {
		t.Fatalf("save: %d %s", response.Code, response.Body.String())
	}
	if response := chatAccessRouteRequest(router, token, http.MethodPut, "/api/v1/platform/chat-model-access", body); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "chat_model_access_changed") {
		t.Fatalf("stale save: %d %s", response.Code, response.Body.String())
	}
	access, err = models.GetChatModelAccess(t.Context(), chatAccessRouteSchool)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"", `,"model_ids":null`} {
		body = `{"tenant_id":"` + chatAccessRouteSchool + `","expected_revision":"` + access.Revision + `"` + field + `}`
		if response := chatAccessRouteRequest(router, token, http.MethodPut, "/api/v1/platform/chat-model-access", body); response.Code != http.StatusBadRequest {
			t.Fatalf("missing or null list accepted: %d %s", response.Code, response.Body.String())
		}
		unchanged, err := models.GetChatModelAccess(t.Context(), chatAccessRouteSchool)
		if err != nil || unchanged.Revision != access.Revision || len(unchanged.ModelIDs) != 1 {
			t.Fatalf("missing or null list revoked models: %+v %v", unchanged, err)
		}
	}
	body = `{"tenant_id":"` + chatAccessRouteSchool + `","expected_revision":"` + access.Revision + `","model_ids":[]}`
	if response := chatAccessRouteRequest(router, token, http.MethodPut, "/api/v1/platform/chat-model-access", body); response.Code != http.StatusOK {
		t.Fatalf("explicit empty list rejected: %d %s", response.Code, response.Body.String())
	}
}

func chatAccessRouteFixture(t *testing.T, user auth.User) (http.Handler, *modelgovernance.MemoryStore, string, string) {
	t.Helper()
	stores := NewMemoryApplicationStores()
	identity := stores.Identity.Auth.(*auth.MemoryStore)
	passwordHash, err := auth.HashPassword("Synthetic chat access password 2026!")
	if err != nil {
		t.Fatal(err)
	}
	identity.AddUser(auth.UserWithPassword{User: user, PasswordHash: passwordHash})
	models := stores.AIGovernance.ModelGovernance.(*modelgovernance.MemoryStore)
	probe := modelgovernance.ManagedAPIProbeResult{OK: true, ProbeMode: "text", GeneratedRequest: true, TextCheck: modelgovernance.ManagedAPICheckResult{OK: true}}
	item, err := models.CreateManagedAPIConfig(context.Background(), chatAccessRouteSchool, user.ID, modelgovernance.ManagedAPIConfigInput{ProviderKey: "custom", DisplayName: "合成模型", AdapterType: "openai_compatible", BaseURL: "https://example.invalid/v1", APIKey: "synthetic-chat-access-key-2026", ModelName: "synthetic-chat-model", ModelVersion: "v1", Region: "global", Status: "active", InitialProbe: &probe})
	if err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	login := chatAccessRouteRequest(router, "", http.MethodPost, "/api/v1/auth/token", `{"tenant_code":"`+user.TenantCode+`","username":"`+user.Username+`","password":"Synthetic chat access password 2026!"}`)
	if login.Code != http.StatusOK {
		t.Fatalf("fixture login: %d %s", login.Code, login.Body.String())
	}
	var session struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || session.AccessToken == "" {
		t.Fatalf("fixture token: %v", err)
	}
	return router, models, session.AccessToken, item.ID
}

func chatAccessRouteRequest(router http.Handler, token, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

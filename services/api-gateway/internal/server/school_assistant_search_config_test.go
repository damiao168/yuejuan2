package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/schoolassistant"
)

func TestSchoolAssistantSearchConfigurationReachesApplicationModule(t *testing.T) {
	user := auth.User{ID: "66666666-6666-4666-8666-666666666666", TenantID: assistantRouteTenant, Roles: []string{"school_admin"}, DataScope: assistantSchoolDataScope()}
	scope, err := auth.ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithAccessScope(auth.WithUser(context.Background(), user), scope)
	// 能力读取只检查注入结果；这里使用不可达地址，防止页面初始化变成搜索引擎连通性探测。
	for _, endpoint := range []string{"", "http://127.0.0.1:1/search"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg := testConfig()
			cfg.SchoolAssistantSearchURL = endpoint
			modules, err := newApplicationModules(ApplicationDependencies{Config: cfg}, NewMemoryApplicationStores(), false)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/school-assistant/capabilities", nil).WithContext(ctx)
			response := httptest.NewRecorder()
			modules.Assistant.Handler.Capabilities(response, request)
			var capabilities schoolassistant.Capabilities
			if err := json.Unmarshal(response.Body.Bytes(), &capabilities); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || capabilities.WebSearch == nil || capabilities.WebSearch.Available != (endpoint != "") {
				t.Fatalf("search dependency not wired: status=%d response=%s", response.Code, response.Body.String())
			}
		})
	}
}

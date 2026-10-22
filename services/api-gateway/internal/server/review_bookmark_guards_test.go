package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/config"
)

// 历史任务不在活动 scope 中；专用入口只放行认证和实际权限，源材料仍由 provider 检查。
func TestPersonalReviewGuardsAllowHistoryWithoutActiveScope(t *testing.T) {
	guards := bookmarkTestGuards(t, []string{"review:work"})
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response := bookmarkGuardRequest(guards.requirePersonalReviewRead(func(w http.ResponseWriter, r *http.Request) {
			if commandreceipt.ID(r.Context()) != "" {
				t.Error("read unexpectedly requires a command identity")
			}
			w.WriteHeader(http.StatusNoContent)
		}), method, "")
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s history read = %d %s", method, response.Code, response.Body.String())
		}
	}
	write := guards.requirePersonalReviewWrite(func(w http.ResponseWriter, r *http.Request) {
		if commandreceipt.ID(r.Context()) != "bookmark-command-1" {
			t.Error("write is missing its transaction command identity")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	if result := bookmarkGuardRequest(write, http.MethodPut, ""); result.Code != http.StatusBadRequest {
		t.Fatalf("write without command key = %d", result.Code)
	}
	if result := bookmarkGuardRequest(write, http.MethodPut, "bookmark-command-1"); result.Code != http.StatusNoContent {
		t.Fatalf("authorized write = %d %s", result.Code, result.Body.String())
	}
}

// 管理权限不隐含阅卷权限；取消本人元数据允许没有阅卷角色的活跃账户进入。
func TestPersonalReviewGuardsSeparateWorkFromMetadataCleanup(t *testing.T) {
	guards := bookmarkTestGuards(t, []string{"review:manage"})
	handler := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	for _, guard := range []func(http.HandlerFunc) http.Handler{guards.requirePersonalReviewRead, guards.requirePersonalReviewWrite} {
		if result := bookmarkGuardRequest(guard(handler), http.MethodGet, ""); result.Code != http.StatusForbidden {
			t.Fatalf("review:manage gained personal contents = %d", result.Code)
		}
	}
	if result := bookmarkGuardRequest(guards.requirePersonalReviewCleanup(handler), http.MethodGet, ""); result.Code != http.StatusNoContent {
		t.Fatalf("cleanup list without work permission = %d", result.Code)
	}
	if result := bookmarkGuardRequest(guards.requirePersonalReviewCleanup(handler), http.MethodDelete, ""); result.Code != http.StatusBadRequest {
		t.Fatalf("cleanup write without key = %d", result.Code)
	}
	if result := bookmarkGuardRequest(guards.requirePersonalReviewCleanup(handler), http.MethodDelete, "bookmark-command-1"); result.Code != http.StatusNoContent {
		t.Fatalf("cleanup write = %d", result.Code)
	}
}

func bookmarkTestGuards(t *testing.T, permissions []string) routerGuards {
	t.Helper()
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{User: auth.User{ID: "bookmark-owner", TenantID: "bookmark-tenant", Username: "owner", Status: "active", Roles: []string{"grader"}, Permissions: permissions}})
	_, err := store.CreateSession(context.Background(), auth.CreateSessionInput{
		TenantID: "bookmark-tenant", UserID: "bookmark-owner", TokenHash: auth.HashToken("bookmark-guard-session"), ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return buildRouterGuards(config.Config{}, ApplicationModules{Identity: &IdentityModule{AuthStore: store}, Capture: &CaptureProcessingModule{}})
}

func bookmarkGuardRequest(handler http.Handler, method, key string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "/api/v1/me/review-bookmarks", nil)
	request.Header.Set("Authorization", "Bearer bookmark-guard-session")
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

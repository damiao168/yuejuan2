package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

func TestScoringTrialRoutesRequireManagementPermissionAndRunBoundary(t *testing.T) {
	const runID = "91d5ec90-f8af-4480-929e-ac440bd566bd"
	const trialID = "18c5e41c-4187-47a4-a093-1ce5df01d8b5"
	base := "/api/v1/scoring-runs/" + runID + "/ai-trials"
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, base},
		{http.MethodPost, base},
		{http.MethodPost, base + "/" + trialID + "/enqueue"},
		{http.MethodPost, base + "/" + trialID + "/decision"},
	} {
		t.Run(endpoint.method+" "+endpoint.path, func(t *testing.T) {
			for _, scenario := range []struct {
				name   string
				status int
			}{
				{"unauthenticated", http.StatusUnauthorized},
				{"no management permission", http.StatusForbidden},
				{"foreign school", http.StatusForbidden},
				{"foreign tenant", http.StatusForbidden},
				{"unknown run", http.StatusNotFound},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					stores := NewMemoryApplicationStores()
					identities := stores.Identity.Auth.(*auth.MemoryStore)
					permissions := []string{"grading:manage"}
					if scenario.name == "no management permission" {
						permissions = []string{"review:work"}
					}
					identities.AddUser(auth.UserWithPassword{User: auth.User{
						ID: "trial-manager", TenantID: "tenant-a", Username: "manager", Status: "active", Roles: []string{"school_admin"}, Permissions: permissions,
						DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}},
					}, SchoolID: "school-a"})
					if _, err := identities.CreateSession(context.Background(), auth.CreateSessionInput{
						TenantID: "tenant-a", UserID: "trial-manager", TokenHash: auth.HashToken("trial-session"), ExpiresAt: time.Now().Add(time.Hour),
					}); err != nil {
						t.Fatal(err)
					}
					boundary := auth.ResourceBoundary{TenantID: "tenant-a", SchoolID: "school-a", ExamID: "exam-a"}
					if scenario.name == "foreign school" {
						boundary.SchoolID = "school-b"
					}
					if scenario.name == "foreign tenant" {
						boundary.TenantID = "tenant-b"
					}
					if scenario.name != "unknown run" {
						identities.AddResourceBoundary("scoring_run", runID, boundary)
					}
					cfg := config.Config{Service: config.ServiceConfig{Name: "trial-authorization", Environment: "test"}}
					router := NewRouterWithApplicationStores(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
					request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Idempotency-Key", "trial-authorization-command")
					if scenario.name != "unauthenticated" {
						request.Header.Set("Authorization", "Bearer trial-session")
					}
					response := httptest.NewRecorder()
					router.ServeHTTP(response, request)
					// 空业务输入不会到达处理器；未授权读取和写命令均须先在路由边界拒绝。
					if response.Code != scenario.status {
						t.Fatalf("response=%d %s; want %d", response.Code, response.Body.String(), scenario.status)
					}
				})
			}
		})
	}
}

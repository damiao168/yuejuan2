package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/platformschools"
)

func TestPlatformSchoolRoutesRequirePlatformRoleAndTenantManage(t *testing.T) {
	const schoolTenantID = "11111111-1111-1111-1111-111111111111"
	paths := []string{
		"/api/v1/platform/schools",
		"/api/v1/platform/schools/" + schoolTenantID,
		"/api/v1/platform/schools/" + schoolTenantID + "/members",
		"/api/v1/platform/schools/" + schoolTenantID + "/usage",
		"/api/v1/platform/schools/" + schoolTenantID + "/model-health",
		"/api/v1/platform/schools/" + schoolTenantID + "/activity",
	}
	t.Run("unauthenticated", func(t *testing.T) {
		router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), NewMemoryApplicationStores())
		for _, path := range paths {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusUnauthorized {
				t.Errorf("GET %s: got %d, want %d: %s", path, response.Code, http.StatusUnauthorized, response.Body.String())
			}
		}
	})
	cases := []struct {
		name                       string
		tenantID, tenantCode, role string
		permissions                []string
		want                       int
	}{
		{"platform administrator", auth.PlatformTenantID, "platform", "platform_admin", []string{"tenant:manage"}, http.StatusOK},
		{"platform without permission", auth.PlatformTenantID, "platform", "platform_admin", nil, http.StatusForbidden},
		{"platform role outside platform tenant", schoolTenantID, "school", "platform_admin", []string{"tenant:manage"}, http.StatusForbidden},
		{"tenant administrator with permission", schoolTenantID, "school", "tenant_admin", []string{"tenant:manage"}, http.StatusForbidden},
		{"school administrator with permission", schoolTenantID, "school", "school_admin", []string{"tenant:manage"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passwordHash, err := auth.HashPassword("secret123")
			if err != nil {
				t.Fatal(err)
			}
			authStore := auth.NewMemoryStore()
			authStore.AddUser(auth.UserWithPassword{User: auth.User{
				ID: "22222222-2222-2222-2222-222222222222", TenantID: tc.tenantID, TenantCode: tc.tenantCode,
				Username: "admin", DisplayName: "Administrator", Status: "active", Roles: []string{tc.role}, Permissions: tc.permissions,
				DataScope: map[string]any{"scope": "tenant"},
			}, PasswordHash: passwordHash})
			stores := NewMemoryApplicationStores()
			stores.Identity.Auth = authStore
			schools := platformschools.NewMemoryStore()
			schools.PutSchool(platformschools.PlatformSchoolSummary{
				TenantID: schoolTenantID, SchoolID: "33333333-3333-3333-3333-333333333333", Name: "学校", Code: "school", Status: "active", CreatedAt: time.Now().UTC(),
			})
			stores.PlatformSchools = schools
			router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
			loginBody, _ := json.Marshal(map[string]string{"tenant_code": tc.tenantCode, "username": "admin", "password": "secret123"})
			login := httptest.NewRecorder()
			router.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(loginBody)))
			if login.Code != http.StatusOK {
				t.Fatalf("login: %d %s", login.Code, login.Body.String())
			}
			var token struct {
				AccessToken string `json:"access_token"`
			}
			if err := json.Unmarshal(login.Body.Bytes(), &token); err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Authorization", "Bearer "+token.AccessToken)
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if response.Code != tc.want {
					t.Errorf("GET %s: got %d, want %d: %s", path, response.Code, tc.want, response.Body.String())
				}
			}
		})
	}
}

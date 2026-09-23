package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestGovernanceSchoolScopeRequiresPlatformModelPermission(t *testing.T) {
	school := "11111111-1111-4111-8111-111111111111"
	handler := withGovernanceSchoolScope(func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserFromContext(r.Context())
		if user.TenantID != school {
			t.Fatalf("scope = %s, want %s", user.TenantID, school)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	request := func(user auth.User) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/ai-eligibility/policy?tenant_id="+school, nil)
		w := httptest.NewRecorder()
		handler(w, r.WithContext(auth.WithUser(r.Context(), user)))
		return w
	}
	permitted := auth.User{TenantID: auth.PlatformTenantID, Permissions: []string{"model:config:manage"}}
	if response := request(permitted); response.Code != http.StatusNoContent {
		t.Fatalf("platform scope = %d: %s", response.Code, response.Body.String())
	}
	forbidden := auth.User{TenantID: "22222222-2222-4222-8222-222222222222", Permissions: []string{"model:config:manage"}}
	if response := request(forbidden); response.Code != http.StatusForbidden {
		t.Fatalf("school cross-scope = %d", response.Code)
	}
	forbidden = auth.User{TenantID: auth.PlatformTenantID, Permissions: []string{"model:read"}}
	if response := request(forbidden); response.Code != http.StatusForbidden {
		t.Fatalf("platform read-only cross-scope = %d", response.Code)
	}
}

func TestLegacyRegistrationWritesAreGone(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/model-providers", nil)
	w := httptest.NewRecorder()
	legacyModelWriteGone(w, r)
	if w.Code != http.StatusGone || w.Header().Get("Deprecation") != "true" {
		t.Fatalf("legacy write = %d, headers: %v", w.Code, w.Header())
	}
}

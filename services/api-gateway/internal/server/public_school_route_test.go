package server

import (
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/platformschools"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicSchoolNameRouteDoesNotExposePlatformSchoolReadModel(t *testing.T) {
	stores := NewMemoryApplicationStores()
	schools := platformschools.NewMemoryStore()
	schools.PutSchool(platformschools.PlatformSchoolSummary{TenantID: "private-id", SchoolID: "private-school", Code: "known", Name: "学校名称", Status: "active", Administrator: platformschools.AdministratorSummary{Username: "private-account"}})
	stores.PlatformSchools = schools
	router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
	for _, path := range []string{"/api/v1/public/schools/known", "/api/v1/public/schools/platform", "/api/v1/public/schools/missing", "/api/v1/platform/schools"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		switch path {
		case "/api/v1/public/schools/known":
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "学校名称") || strings.Contains(rec.Body.String(), "private-") {
				t.Fatalf("public=%d %s", rec.Code, rec.Body)
			}
		case "/api/v1/platform/schools":
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("platform guard=%d %s", rec.Code, rec.Body)
			}
		default:
			if rec.Code != http.StatusNotFound {
				t.Fatalf("unknown=%d %s", rec.Code, rec.Body)
			}
		}
	}
}

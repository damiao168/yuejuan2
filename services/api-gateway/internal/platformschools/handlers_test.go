package platformschools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPlatformSchoolHandlersExposeSeparateReadModels(t *testing.T) {
	store := NewMemoryStore()
	created := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	store.PutSchool(PlatformSchoolSummary{
		TenantID: "tenant-a", SchoolID: "school-a", Name: "实验中学", Code: "syzx", Status: "active", CreatedAt: created,
		Administrator: AdministratorSummary{ID: "admin-a", DisplayName: "张老师", Username: "zhang", AdminCount: 1},
		Members:       MemberCounts{Accounts: 2, Students: 120}, ModelHealth: ModelHealth{Status: ModelHealthy},
	})
	store.PutMembers("tenant-a", []Member{{ID: "teacher-a", Username: "teacher", DisplayName: "李老师", Status: "active", Roles: []string{"teacher"}, CreatedAt: created}})
	store.PutUsage("tenant-a", UsageResult{Summary: UsageSummary{TotalTokens: 42, Requests: 3, WindowDays: 7}, Trend: []UsageTrendPoint{}, ByFeature: []UsageBreakdown{}, ByModel: []UsageBreakdown{}, StartDate: "2026-09-01", EndDate: "2026-09-07"})
	store.PutModelHealth("tenant-a", ModelHealthResult{DefaultModel: ModelHealth{Status: ModelHealthy, ModelName: "example-model", CredentialHint: "sk-••••1234"}, Roles: []ModelRole{}})
	store.PutActivity("tenant-a", ActivityResult{Activities: []ActivityItem{}, Security: SecuritySummary{ActiveAdmins: 1}})
	handler := NewHandler(store)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/schools", handler.List)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}", handler.Get)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/members", handler.Members)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/usage", handler.Usage)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/model-health", handler.ModelHealth)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/activity", handler.Activity)

	tests := []struct {
		path string
		keys []string
	}{
		{"/api/v1/platform/schools?q=zhang&usage_window=7d", []string{"schools", "summary", "next_cursor", "has_more"}},
		{"/api/v1/platform/schools/tenant-a", []string{"school"}},
		{"/api/v1/platform/schools/tenant-a/members?role=teacher", []string{"members", "summary"}},
		{"/api/v1/platform/schools/tenant-a/usage?start_date=2026-09-01&end_date=2026-09-07", []string{"summary", "trend", "by_feature", "by_model", "start_date", "end_date"}},
		{"/api/v1/platform/schools/tenant-a/model-health", []string{"default_model", "roles"}},
		{"/api/v1/platform/schools/tenant-a/activity", []string{"activities", "security"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			for _, key := range test.keys {
				if _, ok := data[key]; !ok {
					t.Errorf("missing JSON key %q in %s", key, response.Body.String())
				}
			}
			if strings.Contains(response.Body.String(), "password") || strings.Contains(response.Body.String(), "credential_ciphertext") || strings.Contains(response.Body.String(), "totp_secret") {
				t.Fatalf("sensitive account/model data leaked: %s", response.Body.String())
			}
			if (strings.HasPrefix(test.path, "/api/v1/platform/schools?") || strings.Contains(test.path, "/usage?")) && !strings.Contains(response.Body.String(), `"arbitration_requests":`) {
				t.Fatalf("usage response is missing arbitration_requests: %s", response.Body.String())
			}
		})
	}
}

func TestPlatformSchoolHandlersRejectBadQueriesAndUnknownSchool(t *testing.T) {
	store := NewMemoryStore()
	store.PutSchool(PlatformSchoolSummary{TenantID: "known", Status: "active"})
	handler := NewHandler(store)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/platform/schools", handler.List)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}", handler.Get)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/members", handler.Members)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/usage", handler.Usage)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/model-health", handler.ModelHealth)
	mux.HandleFunc("GET /api/v1/platform/schools/{tenant_id}/activity", handler.Activity)
	for _, path := range []string{
		"/api/v1/platform/schools?usage_window=garbage",
		"/api/v1/platform/schools?cursor=garbage",
		"/api/v1/platform/schools?limit=garbage",
		"/api/v1/platform/schools?limit=-1",
		"/api/v1/platform/schools/known/members?status=unknown",
		"/api/v1/platform/schools/known/usage?window=unknown",
		"/api/v1/platform/schools/known/usage?start_date=2026-09-02&end_date=2026-09-01",
		"/api/v1/platform/schools/known/activity?limit=garbage",
		"/api/v1/platform/schools/known/activity?limit=-1",
		"/api/v1/platform/schools/known/activity?limit=201",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_platform_school_filter") {
			t.Errorf("%s: status %d body %s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/v1/platform/schools/missing", "/api/v1/platform/schools/missing/members", "/api/v1/platform/schools/missing/usage",
		"/api/v1/platform/schools/missing/model-health", "/api/v1/platform/schools/missing/activity",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), "platform_school_not_found") {
			t.Errorf("%s: status %d body %s", path, response.Code, response.Body.String())
		}
	}
}

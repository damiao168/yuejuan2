package platformschools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicSchoolOnlyReturnsActiveKnownCodeAndDisplayName(t *testing.T) {
	store := NewMemoryStore()
	for _, item := range []PlatformSchoolSummary{{TenantID: "private-id", Code: "known", Name: "示例学校", Status: "active"}, {TenantID: "disabled", Code: "disabled", Name: "停用学校", Status: "suspended"}, {TenantID: "platform", Code: "platform", Name: "平台", Status: "active"}} {
		store.PutSchool(item)
	}
	handler := NewHandler(store)
	for _, code := range []string{"known", "disabled", "platform", "missing"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/schools/"+code, nil)
		req.SetPathValue("code", code)
		rec := httptest.NewRecorder()
		handler.PublicSchool(rec, req)
		if code != "known" {
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status=%d", code, rec.Code)
			}
			continue
		}
		var body map[string]map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body) != 1 || len(body["school"]) != 2 || body["school"]["name"] != "示例学校" || body["school"]["code"] != "known" {
			t.Fatalf("public body=%d %s", rec.Code, rec.Body)
		}
	}
}

func TestPublicSchoolLookupRateLimitIgnoresUntrustedForwardedHeader(t *testing.T) {
	handler := LimitPublicSchoolRead(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for i := range 61 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/public/schools/known", nil)
		req.RemoteAddr = "192.0.2.1:12345"
		req.Header.Set("X-Forwarded-For", string(rune(i+65)))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if i < 60 && rec.Code != http.StatusNoContent || i == 60 && (rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "") {
			t.Fatalf("attempt=%d status=%d", i, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/schools/known", nil)
	req.RemoteAddr = "192.0.2.2:12345"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("independent source=%d", rec.Code)
	}
}

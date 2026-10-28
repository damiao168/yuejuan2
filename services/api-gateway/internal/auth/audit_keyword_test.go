package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAuditKeywordSearchBeforePaginationAndSameExportScope(t *testing.T) {
	store := NewMemoryStore()
	store.users["school|teacher"] = UserWithPassword{User: User{ID: "teacher", TenantID: "tenant", Username: "math-teacher", DisplayName: "数学老师"}}
	_ = store.Audit(t.Context(), AuditEvent{TenantID: "tenant", ActorID: "teacher", Action: "old.event", Reason: "原卷100%_完成", BeforeValue: map[string]any{"password": "hidden-secret"}})
	for range 205 {
		_ = store.Audit(t.Context(), AuditEvent{TenantID: "tenant", Action: "new.event"})
	}
	_ = store.Audit(t.Context(), AuditEvent{TenantID: "other", Action: "old.event", Reason: "原卷100%_完成"})
	actor := User{ID: "auditor", TenantID: "tenant"}
	handler := NewHandler(store, time.Hour)
	request := func(query, scope string, export bool) *httptest.ResponseRecorder {
		t.Helper()
		path := "/api/v1/audit-logs?q=" + url.QueryEscape(query)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(WithUser(req.Context(), actor))
		if scope != "" {
			req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: scope == "tenant"}))
		}
		rec := httptest.NewRecorder()
		if export {
			handler.ExportAudits(rec, req)
		} else {
			handler.ListAudits(rec, req)
		}
		return rec
	}
	for _, query := range []string{"数学老师", "MATH-TEACHER", "100%_", "old.event"} {
		rec := request(query, "tenant", false)
		var page struct {
			Records []AuditRecord `json:"audit_logs"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil || len(page.Records) != 1 || page.Records[0].TenantID != "tenant" {
			t.Fatalf("q=%q response=%d %s", query, rec.Code, rec.Body)
		}
	}
	exported := request("100%_", "tenant", true)
	if exported.Code != http.StatusOK || strings.Count(exported.Body.String(), "old.event") != 1 || strings.Contains(exported.Body.String(), "new.event") {
		t.Fatalf("export=%d %s", exported.Code, exported.Body)
	}
	for _, query := range []string{"hidden-secret", "not-present"} {
		if rec := request(query, "tenant", false); strings.Contains(rec.Body.String(), "old.event") {
			t.Fatalf("hidden/unmatched field was searched: %s", rec.Body)
		}
	}
	if rec := request("100%_", "limited", false); strings.Contains(rec.Body.String(), "old.event") {
		t.Fatalf("keyword expanded limited scope: %s", rec.Body)
	}
	if rec := request(strings.Repeat("中", 129), "tenant", false); rec.Code != http.StatusBadRequest {
		t.Fatalf("long query=%d", rec.Code)
	}
}

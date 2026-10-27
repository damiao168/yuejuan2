package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuditMaximumPageKeepsContinuationWithoutExpandingExportLimit(t *testing.T) {
	store := NewMemoryStore()
	for range 201 {
		if err := store.Audit(t.Context(), AuditEvent{TenantID: "tenant", Action: "synthetic.event"}); err != nil {
			t.Fatal(err)
		}
	}
	actor := User{ID: "auditor", TenantID: "tenant"}
	handler := NewHandler(store, time.Hour)
	read := func(cursor string) struct {
		Records []AuditRecord `json:"audit_logs"`
		HasMore bool          `json:"has_more"`
		Next    string        `json:"next_cursor"`
	} {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?limit=200&cursor="+cursor, nil)
		req = req.WithContext(WithAccessScope(WithUser(req.Context(), actor), AccessScope{TenantID: actor.TenantID, ActorID: actor.ID, TenantWide: true}))
		rec := httptest.NewRecorder()
		handler.ListAudits(rec, req)
		var body struct {
			Records []AuditRecord `json:"audit_logs"`
			HasMore bool          `json:"has_more"`
			Next    string        `json:"next_cursor"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("page response=%d %s", rec.Code, rec.Body)
		}
		return body
	}
	first := read("")
	if len(first.Records) != 200 || !first.HasMore || first.Next == "" {
		t.Fatalf("maximum page lost continuation: records=%d more=%v cursor=%q", len(first.Records), first.HasMore, first.Next)
	}
	last := read(first.Next)
	if len(last.Records) != 1 || last.HasMore || last.Next != "" {
		t.Fatalf("last page=%#v", last)
	}
	for _, item := range first.Records {
		if item.ID == last.Records[0].ID {
			t.Fatal("cursor returned a duplicate audit record")
		}
	}
	ordinary, err := store.ListAudits(t.Context(), "tenant", AuditFilter{Limit: 1000})
	if err != nil || len(ordinary) != 200 {
		t.Fatalf("ordinary/export cap changed: len=%d err=%v", len(ordinary), err)
	}
}

func TestAuditProbeLimitIsSeparateFromRequestedPageSize(t *testing.T) {
	for _, tc := range []struct {
		limit int
		probe bool
		want  int
	}{{0, false, 50}, {200, false, 200}, {201, false, 200}, {1000, false, 200}, {199, true, 200}, {200, true, 201}, {1000, true, 201}} {
		if got := auditQueryLimit(AuditFilter{Limit: tc.limit, IncludeNext: tc.probe}); got != tc.want {
			t.Fatalf("limit=%d probe=%v got=%d want=%d", tc.limit, tc.probe, got, tc.want)
		}
	}
}

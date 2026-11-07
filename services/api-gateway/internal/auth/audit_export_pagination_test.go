package auth

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuditExportIncludesEveryPageAndRejectsOverflow(t *testing.T) {
	for _, count := range []int{451, maxAuditExportRecords + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			store := NewMemoryStore()
			for index := range count {
				store.appendAudit(AuditEvent{TenantID: "tenant", Action: "synthetic.event", TargetID: fmt.Sprint(index)}, time.Now().UTC().Add(-time.Hour))
			}
			store.appendAudit(AuditEvent{TenantID: "other", Action: "synthetic.event"}, time.Now().UTC().Add(-time.Hour))
			req := httptest.NewRequest(http.MethodPost, "/api/v1/audit-logs/export?limit=1&action=synthetic.event", nil)
			req = req.WithContext(WithUser(req.Context(), User{ID: "auditor", TenantID: "tenant"}))
			rec := httptest.NewRecorder()
			NewHandler(store, time.Hour).ExportAudits(rec, req)
			if count > maxAuditExportRecords {
				if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "audit_export_too_large") || strings.Contains(rec.Header().Get("Content-Type"), "csv") {
					t.Fatalf("overflow was silently exported: %d %s", rec.Code, rec.Body.String())
				}
				return
			}
			rows, err := csv.NewReader(rec.Body).ReadAll()
			if rec.Code != http.StatusOK || err != nil || len(rows) != count+1 {
				t.Fatalf("export missed pages: status=%d rows=%d err=%v", rec.Code, len(rows), err)
			}
			seen := map[string]bool{}
			for _, row := range rows[1:] {
				if row[1] != "tenant" || seen[row[0]] {
					t.Fatalf("duplicate or foreign audit: %#v", row)
				}
				seen[row[0]] = true
			}
		})
	}
}

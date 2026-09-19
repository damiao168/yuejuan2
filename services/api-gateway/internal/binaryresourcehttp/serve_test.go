package binaryresourcehttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/binaryresource"
)

func TestServeImageConditionalsDoNotOpenOrAudit(t *testing.T) {
	opened := 0
	resource := binaryresource.Resource{
		ContentType: "image/png", Size: 3, Disposition: `inline; filename="segment.png"`,
		ETag: `"sha256:crop"`, CacheControl: "private, no-cache", AllowHead: true,
		Open: func(context.Context) (io.ReadCloser, error) {
			opened++
			return io.NopCloser(strings.NewReader("png")), nil
		},
		AuditAction: "segment.image_viewed", AuditTargetType: "answer_segment", AuditTargetID: "segment-1",
	}
	audit := auth.NewMemoryStore()
	for _, test := range []struct {
		method string
		etag   string
		status int
	}{{http.MethodGet, resource.ETag, http.StatusNotModified}, {http.MethodHead, "", http.StatusOK}} {
		req := httptest.NewRequest(test.method, "/image", nil)
		req.Header.Set("If-None-Match", test.etag)
		rec := httptest.NewRecorder()
		Serve(rec, req, resource, audit)
		if rec.Code != test.status || opened != 0 || len(audit.Audits()) != 0 {
			t.Fatalf("method %s: status=%d opened=%d audits=%d", test.method, rec.Code, opened, len(audit.Audits()))
		}
		if rec.Header().Get("Cache-Control") != "private, no-cache" || rec.Header().Get("ETag") != resource.ETag {
			t.Fatalf("method %s: conditional headers changed", test.method)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/image", nil)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "grader-1", TenantID: "tenant-1"}))
	rec := httptest.NewRecorder()
	Serve(rec, req, resource, audit)
	if rec.Code != http.StatusOK || rec.Body.String() != "png" || opened != 1 {
		t.Fatalf("image response: status=%d body=%q opened=%d", rec.Code, rec.Body.String(), opened)
	}
	events := audit.Audits()
	if len(events) != 1 || events[0].Action != "segment.image_viewed" || events[0].TenantID != "tenant-1" {
		t.Fatalf("image audit changed: %#v", events)
	}
}

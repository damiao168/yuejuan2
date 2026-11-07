package files_test

import (
	"bytes"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnsupportedFileTypeRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "tool.exe", "application/octet-stream", []byte("MZ synthetic"), nil)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported type expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidOwnerIDRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), map[string]string{
		"owner_type": "exam",
		"owner_id":   "exam-1",
	})
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_owner_id") {
		t.Fatalf("invalid owner id expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInconsistentOwnerReferenceRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), map[string]string{
		"owner_type": "exam",
		"owner_id":   "00000000-0000-0000-0000-000000000101",
		"exam_id":    "00000000-0000-0000-0000-000000000102",
	})
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_owner_reference") {
		t.Fatalf("inconsistent owner reference expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestNormalizedSubmissionPageOwnerTypeAccepted(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	asset := uploadFile(t, router, token, "normalized.png", "image/png", []byte("\x89PNG\r\n\x1a\nsynthetic"), map[string]string{
		"owner_type":    "submission_page_normalized",
		"owner_id":      "00000000-0000-0000-0000-000000000301",
		"submission_id": "00000000-0000-0000-0000-000000000302",
	})
	if asset.OwnerType != "submission_page_normalized" {
		t.Fatalf("normalized page owner type mismatch: %#v", asset)
	}
}

func TestOversizedFileRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	content := append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte("x"), 1024*1024)...)
	body, contentType := multipartUploadBody(t, "large.pdf", "application/pdf", content, nil)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized file expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPathFilenameIsSanitized(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	asset := uploadFile(t, router, token, `..\unsafe\paper.pdf`, "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), nil)
	if asset.OriginalName != "paper.pdf" {
		t.Fatalf("expected sanitized filename, got %s", asset.OriginalName)
	}
}

func TestMaliciousFilenamesRejected(t *testing.T) {
	for _, name := range []string{"CON.pdf", "answer.pdf:evil.exe", ".."} {
		if cleaned, err := files.CleanFilename(name); err == nil {
			t.Fatalf("expected %q to be rejected, got %q", name, cleaned)
		}
	}
}

func TestFilePermissionDenied(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), nil)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("permission denied expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

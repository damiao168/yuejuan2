package files_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
)

func TestUploadGetDownloadAndDeleteFile(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(authStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)

	asset := uploadFile(t, router, token, "paper.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), map[string]string{
		"owner_type": "exam",
		"owner_id":   "00000000-0000-0000-0000-000000000101",
		"exam_id":    "00000000-0000-0000-0000-000000000101",
	})
	if asset.OriginalName != "paper.pdf" || asset.ContentType != "application/pdf" {
		t.Fatalf("unexpected uploaded asset: %#v", asset)
	}

	req := authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID, nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "storage_key") || strings.Contains(rec.Body.String(), "storage_bucket") {
		t.Fatalf("metadata response must not expose object storage path: %s", rec.Body.String())
	}

	req = authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID+"/download", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "%PDF-1.4\nsynthetic pdf\n" {
		t.Fatalf("download returned wrong content: %q", rec.Body.String())
	}

	req = authedRequest(http.MethodDelete, "/api/v1/files/"+asset.ID, nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID, nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted metadata expected 404, got %d %s", rec.Code, rec.Body.String())
	}

	assertAuditAction(t, authStore, "file.uploaded")
	assertAuditAction(t, authStore, "file.downloaded")
	assertAuditAction(t, authStore, "file.deleted")
}

func TestDuplicateUploadRejected(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(authStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	content := []byte("%PDF-1.4\nsame\n")
	fields := map[string]string{"owner_type": "exam", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101"}

	_ = uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "duplicate_file") {
		t.Fatalf("duplicate expected 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestDuplicateImportUploadReusesActiveAsset(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(authStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	content := []byte("\x89PNG\r\n\x1a\nsynthetic")
	fields := map[string]string{"owner_type": "import", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101"}

	first := uploadFile(t, router, token, "paper.png", "image/png", content, fields)
	body, contentType := multipartUploadBody(t, "paper.png", "image/png", content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate import expected reusable 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		File      files.FileAsset `json:"file"`
		Duplicate bool            `json:"duplicate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Duplicate || response.File.ID != first.ID {
		t.Fatalf("expected active asset %s to be reused: %#v", first.ID, response)
	}
}

func TestUploadFailureRemainsDurablyRetryable(t *testing.T) {
	store := files.NewMemoryStore()
	objects := &faultStorage{MemoryObjectStorage: files.NewMemoryObjectStorage(), failPut: true}
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), store, objects)
	token := login(t, router)
	content := []byte("%PDF-1.4\nretryable\n")
	fields := map[string]string{"owner_type": "exam", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101"}

	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("failed object write expected 502, got %d %s", rec.Code, rec.Body.String())
	}
	hash := filesHash(content)
	pending, ok, err := store.FindDuplicate(t.Context(), "00000000-0000-0000-0000-000000000002", "exam", fields["owner_id"], hash)
	if err != nil || !ok || pending.Lifecycle != files.LifecycleUploadFailed {
		t.Fatalf("upload failure must remain durable: err=%v ok=%t asset=%#v", err, ok, pending)
	}

	objects.failPut = false
	asset := uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	if asset.ID != pending.ID || asset.Lifecycle != files.LifecycleActive {
		t.Fatalf("retry should activate the same asset: pending=%#v active=%#v", pending, asset)
	}
	if len(objects.putKeys) != 2 || objects.putKeys[0] != pending.StorageKey || objects.putKeys[1] != pending.StorageKey {
		t.Fatalf("retry must use the persisted object path: %#v", objects.putKeys)
	}
	req = authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID+"/download", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("retry must restore downloadable bytes: status=%d body=%q", rec.Code, rec.Body.Bytes())
	}
}

func TestActivationFailureRetryUsesExistingObjectPath(t *testing.T) {
	// 模拟对象已写入但资产激活失败；重试必须复用原路径，不能产生第二个对象。
	store := &activationFailureStore{MemoryStore: files.NewMemoryStore(), failOnce: true}
	objects := &faultStorage{MemoryObjectStorage: files.NewMemoryObjectStorage()}
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), store, objects)
	token := login(t, router)
	content := []byte("%PDF-1.4\nactivation retry\n")
	fields := map[string]string{"owner_type": "exam", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101"}
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code < 500 {
		t.Fatalf("activation failure must be visible: %d %s", rec.Code, rec.Body.String())
	}
	pending, ok, err := store.FindDuplicate(t.Context(), "00000000-0000-0000-0000-000000000002", "exam", fields["owner_id"], filesHash(content))
	if err != nil || !ok {
		t.Fatalf("missing pending asset after activation failure: %v %t", err, ok)
	}
	asset := uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	if asset.ID != pending.ID || len(objects.putKeys) != 2 || objects.putKeys[0] != objects.putKeys[1] || objects.putKeys[0] != pending.StorageKey {
		t.Fatalf("activation retry created another object: asset=%#v pending=%#v puts=%#v", asset, pending, objects.putKeys)
	}
	req = authedRequest(http.MethodGet, "/api/v1/files/"+asset.ID+"/download", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("retry download: %d %q", rec.Code, rec.Body.Bytes())
	}
}

func TestMissingActiveObjectCanBeRestoredAtPersistedPath(t *testing.T) {
	store := files.NewMemoryStore()
	objects := &faultStorage{MemoryObjectStorage: files.NewMemoryObjectStorage()}
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), store, objects)
	token := login(t, router)
	content := []byte("%PDF-1.4\nrecover missing object\n")
	fields := map[string]string{"owner_type": "exam", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101"}
	first := uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	asset, ok, err := store.FindDuplicate(t.Context(), "00000000-0000-0000-0000-000000000002", "exam", fields["owner_id"], filesHash(content))
	if err != nil || !ok {
		t.Fatalf("missing uploaded asset: %v %t", err, ok)
	}
	if err := objects.Remove(t.Context(), asset.StorageBucket, asset.StorageKey); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkMissing(t.Context(), asset.TenantID, asset.ID, asset.Revision); err != nil {
		t.Fatal(err)
	}
	restored := uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	if restored.ID != first.ID || restored.Lifecycle != files.LifecycleActive || len(objects.putKeys) != 2 || objects.putKeys[0] != objects.putKeys[1] {
		t.Fatalf("missing object retry must reuse the original asset and path: first=%#v restored=%#v keys=%#v", first, restored, objects.putKeys)
	}
	req := authedRequest(http.MethodGet, "/api/v1/files/"+restored.ID+"/download", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), content) {
		t.Fatalf("restored object download: %d %q", rec.Code, rec.Body.Bytes())
	}
}

func TestUploadRetryAuthorizesPersistedAssetScope(t *testing.T) {
	const (
		tenantID = "00000000-0000-0000-0000-000000000002"
		schoolA  = "00000000-0000-0000-0000-000000000301"
		schoolB  = "00000000-0000-0000-0000-000000000302"
		ownerID  = "00000000-0000-0000-0000-000000000401"
	)
	content := []byte("%PDF-1.4\ncross-scope retry\n")
	store := files.NewMemoryStore()
	objects := &faultStorage{MemoryObjectStorage: files.NewMemoryObjectStorage()}
	if _, err := store.CreatePending(t.Context(), files.CreateAssetInput{
		TenantID: tenantID, SchoolID: schoolB, OwnerType: "generic", OwnerID: ownerID,
		OriginalName: "paper.pdf", ContentType: "application/pdf", SizeBytes: int64(len(content)),
		HashSHA256: filesHash(content), StorageBucket: "edugrade-private", StorageKey: "tenant/foreign/paper.pdf",
		Visibility: "private", UploadedBy: "00000000-0000-0000-0000-000000000999",
	}); err != nil {
		t.Fatal(err)
	}
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	account, err := authStore.FindUserByLogin(t.Context(), "demo", "file_admin")
	if err != nil {
		t.Fatal(err)
	}
	account.User.DataScope = map[string]any{"scope": "school", "school_id": schoolA}
	authStore.AddUser(account)
	router := testRouter(authStore, store, objects)
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", content, map[string]string{
		"owner_type": "generic", "owner_id": ownerID, "school_id": schoolA,
	})
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || len(objects.putKeys) != 0 {
		t.Fatalf("cross-scope retry wrote persisted object: status=%d puts=%#v body=%s", rec.Code, objects.putKeys, rec.Body.String())
	}
}

func TestChunkedUploadStopsAtOuterLimitBeforeIdempotencySpool(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)
	t.Setenv("TMP", tempDir)
	t.Setenv("TEMP", tempDir)
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "large.pdf", "application/pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{'x'}, 3*1024*1024)...), nil)
	reader := &countingReader{Reader: bytes.NewReader(body.Bytes())}
	req := authedRequest(http.MethodPost, "/api/v1/files", nil, token)
	req.Body = io.NopCloser(reader)
	req.ContentLength = -1 // chunked request: no early Content-Length rejection
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Idempotency-Key", "oversized-chunked-file")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || reader.read > 2*1024*1024+1 {
		t.Fatalf("expected bounded 413, got %d after reading %d bytes: %s", rec.Code, reader.read, rec.Body.String())
	}
	if leftover, err := os.ReadDir(tempDir); err != nil || len(leftover) != 0 {
		t.Fatalf("temporary idempotency upload was not cleaned up: %v %#v", err, leftover)
	}
}

func TestChunkedOversizedUploadWithoutIdempotencyKeyReturns413(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)
	body, contentType := multipartUploadBody(t, "large.pdf", "application/pdf", append([]byte("%PDF-1.4\n"), bytes.Repeat([]byte{'x'}, 2*1024*1024)...), nil)
	req := authedRequest(http.MethodPost, "/api/v1/files", nil, token)
	req.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
	req.ContentLength = -1
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rec.Body.String(), "request_body_too_large") {
		t.Fatalf("oversized chunked upload: %d %s", rec.Code, rec.Body.String())
	}
}

type countingReader struct {
	io.Reader
	read int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.read += n
	return n, err
}

func TestDeleteFailureDoesNotClaimSuccessAndCanBeRetried(t *testing.T) {
	store := files.NewMemoryStore()
	objects := &faultStorage{MemoryObjectStorage: files.NewMemoryObjectStorage()}
	router := testRouter(authStoreWithPermissions(t, []string{"file:manage"}), store, objects)
	token := login(t, router)
	asset := uploadFile(t, router, token, "paper.pdf", "application/pdf", []byte("%PDF-1.4\ndelete\n"), map[string]string{
		"owner_type": "exam", "owner_id": "00000000-0000-0000-0000-000000000101", "exam_id": "00000000-0000-0000-0000-000000000101",
	})

	objects.failRemove = true
	req := authedRequest(http.MethodDelete, "/api/v1/files/"+asset.ID, nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "object_cleanup_pending") {
		t.Fatalf("failed cleanup must be explicit, got %d %s", rec.Code, rec.Body.String())
	}
	scope := auth.AccessScope{TenantID: asset.TenantID, TenantWide: true}
	pending, err := store.GetScoped(t.Context(), scope, asset.ID)
	if err != nil || pending.Lifecycle != files.LifecycleDeleteFailed {
		t.Fatalf("delete failure must remain retryable: %v %#v", err, pending)
	}

	objects.failRemove = false
	req = authedRequest(http.MethodDelete, "/api/v1/files/"+asset.ID, nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleanup retry expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

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

func testRouter(authStore *auth.MemoryStore, fileStore files.Store, objectStore files.ObjectStorage) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
		Files: config.FileConfig{
			Bucket:            "test-files",
			MaxUploadBytes:    1024 * 1024,
			AllowedExtensions: []string{".pdf", ".png", ".jpg", ".jpeg", ".csv", ".docx"},
		},
	}
	return server.NewRouterWithFiles(cfg, logger.New(io.Discard, "error"), nil, authStore, org.NewMemoryStore(), exam.NewMemoryStore(), paper.NewMemoryStore(), fileStore, objectStore)
}

func authStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "00000000-0000-0000-0000-000000000201",
			TenantID:    "00000000-0000-0000-0000-000000000002",
			TenantCode:  "demo",
			Username:    "file_admin",
			DisplayName: "File Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "tenant"},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "file_admin", "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func uploadFile(t *testing.T, router http.Handler, token string, filename string, fileContentType string, content []byte, fields map[string]string) files.FileResponse {
	t.Helper()
	body, contentType := multipartUploadBody(t, filename, fileContentType, content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		File files.FileResponse `json:"file"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.File
}

func multipartUploadBody(t *testing.T, filename string, fileContentType string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	header.Set("Content-Type", fileContentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body, writer.FormDataContentType()
}

func authedRequest(method string, path string, body *bytes.Buffer, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}

type faultStorage struct {
	*files.MemoryObjectStorage
	failPut    bool
	failRemove bool
	putKeys    []string
}

func (s *faultStorage) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	s.putKeys = append(s.putKeys, key)
	if s.failPut {
		return errors.New("injected put failure")
	}
	return s.MemoryObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

type activationFailureStore struct {
	*files.MemoryStore
	failOnce bool
}

func (s *activationFailureStore) Activate(ctx context.Context, tenantID, id string, revision int64) (files.FileAsset, error) {
	if s.failOnce {
		s.failOnce = false
		return files.FileAsset{}, errors.New("injected activation failure")
	}
	return s.MemoryStore.Activate(ctx, tenantID, id, revision)
}

func (s *faultStorage) Remove(ctx context.Context, bucket, key string) error {
	if s.failRemove {
		return errors.New("injected remove failure")
	}
	return s.MemoryObjectStorage.Remove(ctx, bucket, key)
}

func filesHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

package files_test

import (
	"bytes"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestUploadGetDownloadAndDeleteFile(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(authStore, files.NewMemoryStore(), files.NewMemoryObjectStorage())
	token := login(t, router)

	asset := uploadFile(t, router, token, "paper.pdf", "application/pdf", []byte("%PDF-1.4\nsynthetic pdf\n"), map[string]string{
		"owner_type": "generic",
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
	req.ContentLength = -1 // 分块请求不会因 Content-Length 而提前被拒绝。
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
		"owner_type": "generic",
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

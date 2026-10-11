package files_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func TestBatch31UploadVerifiesBeforeActivationAndRetriesSameAsset(t *testing.T) {
	content := []byte("%PDF-1.4\nverification retry\n")
	for _, fault := range []struct {
		name string
		body []byte
	}{
		{name: "size", body: content[:len(content)-1]},
		{name: "hash", body: bytes.Repeat([]byte("x"), len(content))},
	} {
		t.Run(fault.name, func(t *testing.T) {
			assertUnverifiedUploadRetriesSameAsset(t, content, fault.body)
		})
	}
}

type batch31ReadbackStorage struct {
	*files.MemoryObjectStorage
	readback []byte
	putKeys  []string
}

func (s *batch31ReadbackStorage) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	s.putKeys = append(s.putKeys, key)
	return s.MemoryObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

func (s *batch31ReadbackStorage) Get(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	if s.readback != nil {
		return io.NopCloser(bytes.NewReader(s.readback)), nil
	}
	return s.MemoryObjectStorage.Get(ctx, bucket, key)
}

type batch31ActivationStore struct {
	*files.MemoryStore
	activations int
}

func (s *batch31ActivationStore) Activate(ctx context.Context, tenantID, id string, revision int64) (files.FileAsset, error) {
	s.activations++
	return s.MemoryStore.Activate(ctx, tenantID, id, revision)
}

func assertUnverifiedUploadRetriesSameAsset(t *testing.T, content, readback []byte) {
	t.Helper()
	objects := &batch31ReadbackStorage{MemoryObjectStorage: files.NewMemoryObjectStorage(), readback: readback}
	store := &batch31ActivationStore{MemoryStore: files.NewMemoryStore()}
	audit := authStoreWithPermissions(t, []string{"file:manage"})
	router := testRouter(audit, store, objects)
	token := login(t, router)
	fields := map[string]string{"owner_type": "generic"}
	body, contentType := multipartUploadBody(t, "paper.pdf", "application/pdf", content, fields)
	req := authedRequest(http.MethodPost, "/api/v1/files", body, token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "object_verification_failed") {
		t.Fatalf("verification failure: %d %s", rec.Code, rec.Body.String())
	}
	failed, found, err := store.FindDuplicate(t.Context(), "00000000-0000-0000-0000-000000000002", "generic", "", filesHash(content))
	if err != nil || !found || failed.Lifecycle != files.LifecycleUploadFailed || store.activations != 0 {
		t.Fatalf("unverified object was activated: asset=%+v found=%t err=%v activations=%d", failed, found, err, store.activations)
	}
	for _, event := range audit.Audits() {
		if event.Action == "file.uploaded" {
			t.Fatal("verification failure must not emit successful upload audit")
		}
	}
	// 首次 Put 成功但回读不匹配；恢复后须激活同一资产及存储键，而非另建上传记录。
	objects.readback = nil
	restored := uploadFile(t, router, token, "paper.pdf", "application/pdf", content, fields)
	if restored.ID != failed.ID || restored.Lifecycle != files.LifecycleActive || store.activations != 1 {
		t.Fatalf("retry changed asset identity: failed=%+v restored=%+v activations=%d", failed, restored, store.activations)
	}
	if len(objects.putKeys) != 2 || objects.putKeys[0] != failed.StorageKey || objects.putKeys[1] != failed.StorageKey {
		t.Fatalf("retry changed persisted storage key: %v", objects.putKeys)
	}
	assertAuditAction(t, audit, "file.uploaded")
}

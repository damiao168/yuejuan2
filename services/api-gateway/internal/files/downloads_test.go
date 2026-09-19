package files

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestDownloadReaderRetainsFileScopeAndLifecycle(t *testing.T) {
	store := NewMemoryStore()
	asset, err := store.Create(context.Background(), CreateAssetInput{
		TenantID: "tenant-1", ExamID: "exam-1", OwnerType: "exam", OwnerID: "exam-1",
		OriginalName: "paper.pdf", ContentType: "application/pdf", SizeBytes: 3,
		HashSHA256: "hash-1", StorageBucket: "files", StorageKey: "paper.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	reader := NewDownloadService(store, NewMemoryObjectStorage())
	user := auth.User{ID: "grader-1", TenantID: "tenant-1"}
	if _, err := reader.ReadDownload(context.Background(), user, asset.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing scope: got %v, want forbidden", err)
	}
	wrongScope := auth.WithAccessScope(context.Background(), auth.AccessScope{TenantID: "tenant-1", ActorID: user.ID, ExamIDs: []string{"exam-2"}})
	if _, err := reader.ReadDownload(wrongScope, user, asset.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other exam scope: got %v, want forbidden", err)
	}
	allowedScope := auth.WithAccessScope(context.Background(), auth.AccessScope{TenantID: "tenant-1", ActorID: user.ID, ExamIDs: []string{"exam-1"}})
	resource, err := reader.ReadDownload(allowedScope, user, asset.ID)
	if err != nil || resource.AuditAction != "file.downloaded" {
		t.Fatalf("authorized download: resource=%#v err=%v", resource, err)
	}
	pending, err := store.CreatePending(context.Background(), CreateAssetInput{
		TenantID: "tenant-1", ExamID: "exam-1", OwnerType: "exam", OwnerID: "exam-1",
		OriginalName: "pending.pdf", ContentType: "application/pdf", SizeBytes: 3,
		HashSHA256: "hash-2", StorageBucket: "files", StorageKey: "pending.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ReadDownload(allowedScope, user, pending.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("pending asset: got %v, want not active", err)
	}
}

package capture

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMixedDuplicateUploadGroupDoesNotSkipFilePositions(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	batch, err := store.CreateBatch(ctx, "tenant", "exam", "actor", CreateBatchInput{Name: "混合选择", SourceType: "web_upload"})
	if err != nil {
		t.Fatal(err)
	}
	register := func(group string, count, index int, hash string) File {
		t.Helper()
		asset := FileAssetSnapshot{ID: uuid.NewString(), SizeBytes: 1, SHA256: hash}
		file, err := store.RegisterFile(ctx, "tenant", batch.ID, "actor", RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: uuid.NewString(), UploadGroupID: group, UploadGroupFileCount: count, UploadGroupFileIndex: index, PagesPerStudent: 2}, asset)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	decode := func(file File) {
		t.Helper()
		if _, err := store.ApplyFileResult(ctx, "tenant", file.ID, []DecodedPageInput{{SourceIndex: 1, FileAssetID: uuid.NewString()}}); err != nil {
			t.Fatal(err)
		}
	}
	old := register(uuid.NewString(), 1, 1, "old")
	if _, err := store.QueueBatch(ctx, "tenant", batch.ID, "actor"); err != nil {
		t.Fatal(err)
	}
	decode(old)
	group := uuid.NewString()
	duplicate := register(group, 3, 1, "old")
	front := register(group, 3, 2, "new-front")
	back := register(group, 3, 3, "new-back")
	if duplicate.Status != "duplicate" {
		t.Fatalf("expected duplicate source: %+v", duplicate)
	}
	if _, err := store.QueueBatch(ctx, "tenant", batch.ID, "actor"); err != nil {
		t.Fatal(err)
	}
	decode(front)
	decode(back)
	// 虽然两张新页恰好够一份答卷，也不能跳过重复位置改变这次选择的分组边界。
	pages, err := store.ListPages(ctx, "tenant", batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range pages {
		if page.CaptureFileID == front.ID || page.CaptureFileID == back.ID {
			if page.Status != "decoded" || page.PageIdentity["grouping_status"] != "waiting_files" {
				t.Fatalf("mixed group silently skipped its duplicate: %+v", page)
			}
		}
	}
}

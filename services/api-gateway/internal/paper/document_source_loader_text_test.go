package paper

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func TestStoredDocumentSourceLoaderPreservesMarkdownMath(t *testing.T) {
	ctx := context.Background()
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	content := "# 数学试题\n\n1. 已知 $x^2+2x+1=0$，求 $x$。\n\n$$\\frac{1}{2}+\\frac{1}{3}=?$$"
	asset, err := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: "tenant-1", OwnerType: "import", OwnerID: "exam-1",
		OriginalName: "pasted-material.md", ContentType: "text/plain; charset=utf-8",
		SizeBytes: int64(len(content)), HashSHA256: strings.Repeat("a", 64),
		StorageBucket: "files", StorageKey: "pasted-material.md", Visibility: "private", UploadedBy: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, "files", "pasted-material.md", bytes.NewBufferString(content), int64(len(content)), "text/plain; charset=utf-8"); err != nil {
		t.Fatal(err)
	}

	loaded, err := (storedDocumentSourceLoader{files: fileStore, objects: objects}).Load(ctx, "tenant-1", asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != content {
		t.Fatalf("markdown or LaTeX changed during loading:\n%s", loaded)
	}
}

func TestStoredDocumentSourceLoaderRejectsNonUTF8Text(t *testing.T) {
	ctx := context.Background()
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	asset, err := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: "tenant-1", OwnerType: "import", OwnerID: "exam-1",
		OriginalName: "invalid.md", ContentType: "text/plain",
		SizeBytes: 3, HashSHA256: strings.Repeat("b", 64), StorageBucket: "files", StorageKey: "invalid.md",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = objects.Put(ctx, "files", "invalid.md", bytes.NewReader([]byte{0xff, 0xfe, 0xfd}), 3, "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, err = (storedDocumentSourceLoader{files: fileStore, objects: objects}).Load(ctx, "tenant-1", asset.ID); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("expected UTF-8 validation error, got %v", err)
	}
}

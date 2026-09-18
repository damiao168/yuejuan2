package paper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func TestDocumentVisualPageLoaderRejectsUnsupportedMediaAndMissingObject(t *testing.T) {
	ctx := context.Background()
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	documents := []normalizedImportDocument{{SourceID: "source", DocumentIndex: 0}}

	unsupported, err := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: "tenant", OwnerType: "paper_import_page", OwnerID: "import",
		OriginalName: "page.gif", ContentType: "image/gif", SizeBytes: 4, HashSHA256: "unsupported", StorageBucket: "pages", StorageKey: "page.gif",
	})
	if err != nil {
		t.Fatal(err)
	}
	loader := storedDocumentVisualPageLoader{files: fileStore, objects: objects}
	if _, err = loader.Load(ctx, "tenant", documents, []PaperImportDecodedPage{{SourceID: "source", DocumentIndex: 0, PageNo: 1, FileAssetID: unsupported.ID}}); err == nil || !strings.Contains(err.Error(), "unsupported media type") {
		t.Fatalf("unsupported media error = %v", err)
	}

	missing, err := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: "tenant", OwnerType: "paper_import_page", OwnerID: "import",
		OriginalName: "missing.png", ContentType: "image/png", SizeBytes: 4, HashSHA256: "missing", StorageBucket: "pages", StorageKey: "missing.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = loader.Load(ctx, "tenant", documents, []PaperImportDecodedPage{{SourceID: "source", DocumentIndex: 0, PageNo: 1, FileAssetID: missing.ID}}); err == nil || !strings.Contains(err.Error(), "load paper page image") {
		t.Fatalf("missing object error = %v", err)
	}
}

func TestDocumentVisualPageLoaderRejectsTotalSizeExceeded(t *testing.T) {
	ctx := context.Background()
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	documents := []normalizedImportDocument{{SourceID: "source", DocumentIndex: 0}}
	pages := make([]PaperImportDecodedPage, 0, 5)
	for index := 0; index < 5; index++ {
		payload := bytes.Repeat([]byte{byte('a' + index)}, 7<<20)
		digest := fmt.Sprintf("%x", sha256.Sum256(payload))
		key := "page-" + string(rune('a'+index)) + ".png"
		if err := objects.Put(ctx, "pages", key, bytes.NewReader(payload), int64(len(payload)), "image/png"); err != nil {
			t.Fatal(err)
		}
		asset, err := fileStore.Create(ctx, files.CreateAssetInput{
			TenantID: "tenant", OwnerType: "paper_import_page", OwnerID: "import",
			OriginalName: key, ContentType: "image/png", SizeBytes: int64(len(payload)), HashSHA256: digest, StorageBucket: "pages", StorageKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, PaperImportDecodedPage{SourceID: "source", DocumentIndex: 0, PageNo: index + 1, FileAssetID: asset.ID})
	}
	loader := storedDocumentVisualPageLoader{files: fileStore, objects: objects}
	if _, err := loader.Load(ctx, "tenant", documents, pages); err == nil || !strings.Contains(err.Error(), "multimodal request limit") {
		t.Fatalf("total size error = %v", err)
	}
}

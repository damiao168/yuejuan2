package paper

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

func completenessLoader(t *testing.T, name, contentType string, data []byte) (storedDocumentSourceLoader, string) {
	t.Helper()
	ctx := context.Background()
	store, objects := files.NewMemoryStore(), files.NewMemoryObjectStorage()
	asset, err := store.Create(ctx, files.CreateAssetInput{TenantID: "tenant", OwnerType: "import", OwnerID: "exam", OriginalName: name,
		ContentType: contentType, SizeBytes: int64(len(data)), HashSHA256: strings.Repeat("a", 64), StorageBucket: "fixture", StorageKey: name})
	if err != nil {
		t.Fatal(err)
	}
	if err := objects.Put(ctx, "fixture", name, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		t.Fatal(err)
	}
	return storedDocumentSourceLoader{files: store, objects: objects}, asset.ID
}

func TestDocumentTextBudgetNeverReturnsTruncatedPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		accepted      bool
	}{
		{"ascii at limit", strings.Repeat("a", maxDocumentTextBytes-4) + "tail", true},
		{"unicode under limit", strings.Repeat("中", maxDocumentTextBytes/3-2) + "尾题", true},
		{"ascii over limit", strings.Repeat("a", maxDocumentTextBytes) + "tail", false},
		{"unicode would split", strings.Repeat("中", maxDocumentTextBytes/3) + "末题", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader, id := completenessLoader(t, "source.md", "text/markdown", []byte(tc.content))
			text, err := loader.Load(context.Background(), "tenant", id)
			if tc.accepted {
				if err != nil || text != tc.content || !utf8.ValidString(text) {
					t.Fatalf("body changed: length=%d err=%v", len(text), err)
				}
			} else if err == nil || text != "" || !strings.Contains(err.Error(), "拆分") {
				t.Fatalf("silent truncation: length=%d err=%v", len(text), err)
			}
		})
	}
}

func TestDOCXBudgetRejectsIncompleteXMLInsteadOfExtractingPrefix(t *testing.T) {
	var data bytes.Buffer
	archive := zip.NewWriter(&data)
	document, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = document.Write([]byte("<w:p><w:t>" + strings.Repeat("a", maxDocumentTextBytes*2) + "tail</w:t></w:p>"))
	if err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	text, err := extractDOCXText(data.Bytes())
	if err == nil || text != "" || !strings.Contains(err.Error(), "拆分") {
		t.Fatalf("incomplete XML accepted: length=%d err=%v", len(text), err)
	}
}

func TestPDFWithPartialLiteralTextAlwaysRequiresWholePageDecode(t *testing.T) {
	// 一段可见Tj标题不代表其他TJ正文已提取，必须交给已有的整份PDF页面路径。
	data := []byte("%PDF-1.7\nBT (A long literal title with more than twenty letters) Tj [(Body of question 2) 10 (answer)] TJ ET\n%%EOF")
	loader, id := completenessLoader(t, "partial.pdf", "application/pdf", data)
	text, err := loader.Load(context.Background(), "tenant", id)
	if !errors.Is(err, errDocumentOCRRequired) || text != "" {
		t.Fatalf("partial PDF accepted as complete: %q %v", text, err)
	}
	loader, id = completenessLoader(t, "invalid.pdf", "application/pdf", []byte("not a PDF"))
	if _, err := loader.Load(context.Background(), "tenant", id); err == nil || errors.Is(err, errDocumentOCRRequired) {
		t.Fatalf("damaged PDF not rejected: %v", err)
	}
}

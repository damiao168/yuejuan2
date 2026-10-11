package paper

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

type batch43VisualImageReader struct {
	io.Reader
	readErr  error
	closeErr error
	closed   bool
}

func (r *batch43VisualImageReader) Read(data []byte) (int, error) {
	if r.readErr != nil {
		return 0, r.readErr
	}
	return r.Reader.Read(data)
}

func (r *batch43VisualImageReader) Close() error {
	r.closed = true
	return r.closeErr
}

type batch43VisualObjectStorage struct {
	files.ObjectStorage
	reader io.ReadCloser
}

func (s batch43VisualObjectStorage) Get(context.Context, string, string) (io.ReadCloser, error) {
	return s.reader, nil
}

func TestBatch43DocumentVisualLoaderClosesReaderBeforeReturningFirstError(t *testing.T) {
	readErr := errors.New("synthetic image read failure")
	closeErr := errors.New("synthetic image close failure")
	// 同时失败时仍须执行 Close，并返回读取首错；读成功时才返回关闭错误。
	for _, test := range []struct {
		name    string
		readErr error
		wantErr error
		prefix  string
	}{
		{name: "read and close failure", readErr: readErr, wantErr: readErr, prefix: "read paper page image: "},
		{name: "close failure", wantErr: closeErr, prefix: "close paper page image: "},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			fileStore := files.NewMemoryStore()
			asset, err := fileStore.Create(ctx, files.CreateAssetInput{
				TenantID: "tenant", OwnerType: "paper_import_page", OwnerID: "import",
				OriginalName: "page.png", ContentType: "image/png", SizeBytes: 4,
				StorageBucket: "pages", StorageKey: "page.png",
			})
			if err != nil {
				t.Fatal(err)
			}
			reader := &batch43VisualImageReader{Reader: strings.NewReader("page"), readErr: test.readErr, closeErr: closeErr}
			objects := batch43VisualObjectStorage{ObjectStorage: files.NewMemoryObjectStorage(), reader: reader}
			loader := storedDocumentVisualPageLoader{files: fileStore, objects: objects}
			visual, err := loader.Load(ctx, "tenant", []normalizedImportDocument{{SourceID: "source", DocumentIndex: 0}}, []PaperImportDecodedPage{{SourceID: "source", DocumentIndex: 0, PageNo: 1, FileAssetID: asset.ID}})
			if !reader.closed || !errors.Is(err, test.wantErr) || err.Error() != test.prefix+test.wantErr.Error() || visual != nil {
				t.Fatalf("close/first error = closed:%v visual:%+v error:%v", reader.closed, visual, err)
			}
		})
	}
}

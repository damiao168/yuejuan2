package paper

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

type batch24OCRSource struct {
	DocumentSourceLoader
	metadataError error
}

func (batch24OCRSource) Load(context.Context, string, string) (string, error) {
	return "", errDocumentOCRRequired
}

func (s batch24OCRSource) Metadata(context.Context, string, string) (files.FileAsset, error) {
	return files.FileAsset{ContentType: "application/pdf"}, s.metadataError
}

func TestBatch24MissingOCRRuntimeKeepsFailureWriteAndReadErrorOrder(t *testing.T) {
	writeErr := errors.New("synthetic failure record rejected")
	metadataErr := errors.New("synthetic metadata unavailable")
	for _, tc := range []struct {
		name          string
		writeError    error
		metadataError error
		wantError     error
		wantID        string
		wantCalls     int
	}{
		{name: "runtime_missing", wantID: "failed-job", wantCalls: 1},
		{name: "failure_write_rejected", writeError: writeErr, wantError: writeErr, wantCalls: 1},
		{name: "metadata_error_first", metadataError: metadataErr, wantError: metadataErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := &batch23SourceFailureStore{failure: tc.writeError}
			service := &DocumentImportService{store: store, sourceLoader: batch24OCRSource{metadataError: tc.metadataError}}
			job := PaperImportJob{ID: "import-24", Sources: []PaperImportSource{{ID: "source", FileAssetID: "file"}}}
			// 元数据读取成功后才处理缺少运行时；记录失败本身出错时，不能返回成功的失败任务。
			got, err := service.processSources(ctx, "tenant-24", "user", job)
			if !errors.Is(err, tc.wantError) || got.ID != tc.wantID || store.calls != tc.wantCalls {
				t.Fatalf("job=%#v error=%v failureCalls=%d", got, err, store.calls)
			}
			if tc.wantCalls == 1 && (store.context != ctx || store.tenant != "tenant-24" || store.importID != job.ID || store.code != "paper_ocr_unavailable" || !reflect.DeepEqual(store.details, []string{"扫描版 PDF 需要 OCR Worker，但当前存储未配置任务运行时"})) {
				t.Fatalf("unexpected failure write: %#v", store)
			}
		})
	}
}

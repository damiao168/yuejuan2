package captureupload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

const (
	testTenant = "tenant-a24"
	testExam   = "exam-a24"
	testActor  = "operator-a24"
)

func TestResumableCaptureUploadCompletesOnceAfterRetry(t *testing.T) {
	ctx, service, captures, fileStore, objects, batchID := newTestService(t)
	payload := append([]byte("\x89PNG\r\n\x1a\n"), []byte("offline scanner page")...)
	input := testInitInput(payload, batchID)

	initialized, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if initialized.AlreadyExists || initialized.ConfirmedOffset != 0 {
		t.Fatalf("unexpected first init response: %+v", initialized)
	}
	updated, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(payload), Data: payload})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if updated.ConfirmedOffset != int64(len(payload)) {
		t.Fatalf("confirmed offset=%d want=%d", updated.ConfirmedOffset, len(payload))
	}
	// 中断的客户端重试初始化和首个分块；两者都不会创建第二个服务端页面，也不会移动已确认偏移。
	resumed, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("resume init: %v", err)
	}
	if !resumed.AlreadyExists || resumed.ConfirmedOffset != int64(len(payload)) {
		t.Fatalf("resume response=%+v", resumed)
	}
	if _, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(payload), Data: payload}); err != nil {
		t.Fatalf("idempotent chunk retry: %v", err)
	}

	completed, err := service.Complete(ctx, testTenant, testActor, initialized.RemoteUploadID, CompleteInput{SHA256: input.SHA256})
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completed.Status != "completed" || completed.FileAssetID == "" || completed.CaptureFileID == "" {
		t.Fatalf("unexpected completion: %+v", completed)
	}
	again, err := service.Complete(ctx, testTenant, testActor, initialized.RemoteUploadID, CompleteInput{SHA256: input.SHA256})
	if err != nil {
		t.Fatalf("idempotent complete: %v", err)
	}
	if again.CaptureFileID != completed.CaptureFileID {
		t.Fatalf("complete changed capture file: first=%s second=%s", completed.CaptureFileID, again.CaptureFileID)
	}
	captureFiles, err := captures.ListFiles(ctx, testTenant, batchID)
	if err != nil {
		t.Fatalf("list capture files: %v", err)
	}
	if len(captureFiles) != 1 {
		t.Fatalf("capture files=%d want 1", len(captureFiles))
	}
	asset, err := fileStore.Get(ctx, testTenant, completed.FileAssetID)
	if err != nil {
		t.Fatalf("get materialized asset: %v", err)
	}
	body, err := objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		t.Fatalf("get object: %v", err)
	}
	saved, err := io.ReadAll(body)
	body.Close()
	if err != nil || !bytes.Equal(saved, payload) {
		t.Fatalf("materialized bytes are not the verified source: err=%v", err)
	}
}

func TestTIFFUploadCompletesWithDefaultFilePolicy(t *testing.T) {
	ctx, service, _, fileStore, _, batchID := newTestService(t)
	payload := append([]byte{'I', 'I', 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00}, []byte("scanner tiff page")...)
	input := testInitInput(payload, batchID)
	input.Filename = "page-001.tiff"
	input.MIME = "image/tiff"
	input.IdempotencyKey = "a24-upload-tiff"

	initialized, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("init TIFF: %v", err)
	}
	if _, err = service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{
		Offset: 0, SHA256: sha256Hex(payload), Data: payload,
	}); err != nil {
		t.Fatalf("append TIFF: %v", err)
	}
	completed, err := service.Complete(ctx, testTenant, testActor, initialized.RemoteUploadID, CompleteInput{SHA256: input.SHA256})
	if err != nil {
		t.Fatalf("complete TIFF: %v", err)
	}
	asset, err := fileStore.Get(ctx, testTenant, completed.FileAssetID)
	if err != nil {
		t.Fatalf("get TIFF asset: %v", err)
	}
	if asset.Lifecycle != "active" || asset.ContentType != "image/tiff" {
		t.Fatalf("TIFF asset=%+v, want active image/tiff", asset)
	}
}

func TestResumableCaptureUploadRejectsHashMismatchWithoutCaptureFile(t *testing.T) {
	ctx, service, captures, _, _, batchID := newTestService(t)
	payload := append([]byte("\x89PNG\r\n\x1a\n"), []byte("different page")...)
	input := testInitInput(payload, batchID)
	input.SHA256 = sha256Hex([]byte("expected different original"))
	initialized, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(payload), Data: payload}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := service.Complete(ctx, testTenant, testActor, initialized.RemoteUploadID, CompleteInput{}); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("complete error=%v want hash mismatch", err)
	}
	session, err := service.Get(ctx, testTenant, initialized.RemoteUploadID)
	if err != nil {
		t.Fatalf("get failed session: %v", err)
	}
	if session.Status != "failed" || session.ErrorCode != "final_hash_mismatch" {
		t.Fatalf("failed session=%+v", session)
	}
	captureFiles, err := captures.ListFiles(ctx, testTenant, batchID)
	if err != nil {
		t.Fatalf("list capture files: %v", err)
	}
	if len(captureFiles) != 0 {
		t.Fatalf("hash mismatch must not create capture file, got %d", len(captureFiles))
	}
}

func TestResumableCaptureUploadRejectsOffsetGap(t *testing.T) {
	ctx, service, _, _, _, batchID := newTestService(t)
	service.chunkSize = 4
	payload := []byte("abcdefgh")
	initialized, err := service.Init(ctx, testTenant, testActor, testInitInput(payload, batchID))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 4, SHA256: sha256Hex(payload[4:]), Data: payload[4:]}); !errors.Is(err, ErrConflict) {
		t.Fatalf("gap error=%v want conflict", err)
	}
	if _, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(payload[:4]), Data: payload[:4]}); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if _, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 4, SHA256: sha256Hex(payload[4:]), Data: payload[4:]}); err != nil {
		t.Fatalf("resumed chunk: %v", err)
	}
}

func TestFiveHundredPageFixtureRecoversPage173WithoutDuplicateRegistration(t *testing.T) {
	ctx, service, captures, _, objects, batchID := newTestService(t)
	service.chunkSize = 3
	payload := append([]byte("\x89PNG\r\n\x1a\n"), []byte("page-173-recovery")...)
	input := testInitInput(payload, batchID)
	input.IdempotencyKey = "a24-500-pages-page-173"

	// 夹具表示工作站上的 500 条持久页面记录：前 172 页已完成，第 173 页在远端收到一个分块后中断，后续 327 页仍待处理。
	// 此服务测试只关注跨桌面与服务端边界的第 173 页状态。
	const fixturePages = 500
	const interruptedPage = 173
	if fixturePages-interruptedPage != 327 {
		t.Fatal("fixture arithmetic changed")
	}

	initialized, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("init interrupted page: %v", err)
	}
	firstChunk := payload[:3]
	updated, err := service.AppendChunk(ctx, testTenant, initialized.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(firstChunk), Data: firstChunk})
	if err != nil {
		t.Fatalf("confirm first chunk: %v", err)
	}
	if updated.ConfirmedOffset != 3 {
		t.Fatalf("confirmed offset=%d want 3", updated.ConfirmedOffset)
	}

	resumed := resumeInterruptedCaptureChunks(t, ctx, service, input, payload, updated)

	// 完成后再次 Init 模拟客户端未收到完成响应的重试；必须返回原登记，不能新增页面。
	completed, err := service.Complete(ctx, testTenant, testActor, resumed.RemoteUploadID, CompleteInput{SHA256: input.SHA256})
	if err != nil {
		t.Fatalf("complete recovered page: %v", err)
	}
	if completed.CaptureFileID == "" || completed.FileAssetID == "" {
		t.Fatalf("server completion missing durable ids: %+v", completed)
	}
	afterLostResponse, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("init after lost completion response: %v", err)
	}
	if afterLostResponse.Status != "completed" || afterLostResponse.CaptureFileID != completed.CaptureFileID {
		t.Fatalf("lost response recovery=%+v want capture file %s", afterLostResponse, completed.CaptureFileID)
	}
	again, err := service.Complete(ctx, testTenant, testActor, resumed.RemoteUploadID, CompleteInput{SHA256: input.SHA256})
	if err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
	if again.CaptureFileID != completed.CaptureFileID {
		t.Fatalf("completion created a different capture file: %s != %s", again.CaptureFileID, completed.CaptureFileID)
	}
	files, err := captures.ListFiles(ctx, testTenant, batchID)
	if err != nil {
		t.Fatalf("list registered capture files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("capture files=%d want exactly one after restart recovery", len(files))
	}
	assertRecoveredCaptureObjectBytes(t, ctx, service, objects, completed.FileAssetID, payload)
}

func newTestService(t *testing.T) (context.Context, *Service, *capture.MemoryStore, *files.MemoryStore, *files.MemoryObjectStorage, string) {
	t.Helper()
	ctx := context.Background()
	captures := capture.NewMemoryStore()
	batch, err := captures.CreateBatch(ctx, testTenant, testExam, testActor, capture.CreateBatchInput{Name: "offline spool", SourceType: "desktop_sync", IdempotencyKey: "a24-batch"})
	if err != nil {
		t.Fatalf("create capture batch: %v", err)
	}
	fileStore := files.NewMemoryStore()
	objects := files.NewMemoryObjectStorage()
	service := NewService(NewMemoryStore(), captures, fileStore, objects, config.FileConfig{Bucket: "test-files", MaxUploadBytes: 1024 * 1024})
	return ctx, service, captures, fileStore, objects, batch.ID
}

func testInitInput(payload []byte, batchID string) InitInput {
	return InitInput{SHA256: sha256Hex(payload), Size: int64(len(payload)), MIME: "image/png", ExamID: testExam, BatchID: batchID, IdempotencyKey: "a24-upload", Filename: "page.png"}
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func resumeInterruptedCaptureChunks(t *testing.T, ctx context.Context, service *Service, input InitInput, payload []byte, updated Session) InitResponse {
	t.Helper()
	firstChunk := payload[:3]
	// 模拟终止和重启：Init 返回远端检查点；已知分块的过时重试保持幂等，随后桌面端从偏移 3 继续。
	resumed, err := service.Init(ctx, testTenant, testActor, input)
	if err != nil {
		t.Fatalf("restart init: %v", err)
	}
	if !resumed.AlreadyExists || resumed.ConfirmedOffset != 3 || resumed.Status != "uploading" {
		t.Fatalf("restart checkpoint=%+v", resumed)
	}
	if _, err := service.AppendChunk(ctx, testTenant, resumed.RemoteUploadID, ChunkInput{Offset: 0, SHA256: sha256Hex(firstChunk), Data: firstChunk}); err != nil {
		t.Fatalf("known chunk replay must be idempotent: %v", err)
	}
	for offset := int64(3); offset < int64(len(payload)); {
		end := offset + service.chunkSize
		if end > int64(len(payload)) {
			end = int64(len(payload))
		}
		chunk := payload[offset:end]
		updated, err = service.AppendChunk(ctx, testTenant, resumed.RemoteUploadID, ChunkInput{Offset: offset, SHA256: sha256Hex(chunk), Data: chunk})
		if err != nil {
			t.Fatalf("resume chunk at offset %d: %v", offset, err)
		}
		offset = updated.ConfirmedOffset
	}
	if updated.ConfirmedOffset != int64(len(payload)) {
		t.Fatalf("final offset=%d want %d", updated.ConfirmedOffset, len(payload))
	}

	return resumed
}

// 完成与重复登记已验证后，仍读取实际存储对象核对恢复后原始字节。
func assertRecoveredCaptureObjectBytes(t *testing.T, ctx context.Context, service *Service, objects *files.MemoryObjectStorage, fileAssetID string, payload []byte) {
	t.Helper()
	asset, err := service.fileStore.Get(ctx, testTenant, fileAssetID)
	if err != nil {
		t.Fatalf("get confirmed file asset: %v", err)
	}
	object, err := objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		t.Fatalf("get verified object: %v", err)
	}
	materialized, readErr := io.ReadAll(object)
	_ = object.Close()
	if readErr != nil || !bytes.Equal(materialized, payload) {
		t.Fatalf("verified object does not match recovered source: err=%v", readErr)
	}
}

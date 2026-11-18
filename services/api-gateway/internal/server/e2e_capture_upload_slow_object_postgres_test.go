package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

type pausedCaptureObjectStorage struct {
	files.ObjectStorage
	started chan struct{}
	resume  chan struct{}
	puts    atomic.Int32
}

func (s *pausedCaptureObjectStorage) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if s.puts.Add(1) == 1 {
		var firstByte [1]byte
		if _, err := io.ReadFull(body, firstByte[:]); err != nil {
			return err
		}
		// 已开始读取对象正文，再暂停读取后续字节，稳定制造对象存储背压。
		close(s.started)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.resume:
			body = io.MultiReader(bytes.NewReader(firstByte[:]), body)
		}
	}
	return s.ObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

func TestPostgresCaptureUploadSlowObjectStorageReleasesDatabaseConnection(t *testing.T) {
	db, _, _, f, _, _ := repairFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	db.SetMaxOpenConns(1)
	captures := capture.NewPostgresStore(db)
	fileStore := files.NewPostgresStore(db)
	objects := &pausedCaptureObjectStorage{
		ObjectStorage: files.NewMemoryObjectStorage(),
		started:       make(chan struct{}),
		resume:        make(chan struct{}),
	}
	service := captureupload.NewService(captureupload.NewPostgresStore(db), captures, fileStore, objects, config.FileConfig{Bucket: "capture-slow-test"})
	batch, err := captures.CreateBatch(ctx, f.TenantID, f.ExamID, f.AdminID, capture.CreateBatchInput{
		Name: "slow-object-storage", SourceType: "desktop_sync", IdempotencyKey: "slow-object-batch",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte("synthetic-scan-page"), 8)...)
	hash := fmt.Sprintf("%x", sha256.Sum256(payload))
	initialized, err := service.Init(ctx, f.TenantID, f.AdminID, captureupload.InitInput{
		SHA256: hash, Size: int64(len(payload)), MIME: "image/png", ExamID: f.ExamID,
		BatchID: batch.ID, IdempotencyKey: "slow-object-file", Filename: "scan.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	uploadID := initialized.RemoteUploadID
	// 切成多个分块，完成回执必须确认全部分块，且对象内容与原件一致。
	const chunkSize = 16
	for offset := 0; offset < len(payload); offset += chunkSize {
		chunk := payload[offset:min(offset+chunkSize, len(payload))]
		_, err := service.AppendChunk(ctx, f.TenantID, uploadID, captureupload.ChunkInput{
			Offset: int64(offset), SHA256: fmt.Sprintf("%x", sha256.Sum256(chunk)), Data: chunk,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	type completionResult struct {
		session captureupload.Session
		err     error
	}
	finished := make(chan completionResult, 1)
	go func() {
		session, err := service.Complete(ctx, f.TenantID, f.AdminID, uploadID, captureupload.CompleteInput{SHA256: hash})
		finished <- completionResult{session: session, err: err}
	}()
	select {
	case <-objects.started:
	case completion := <-finished:
		t.Fatalf("upload returned before object pause: %+v %v", completion.session, completion.err)
	case <-ctx.Done():
		t.Fatalf("object upload did not reach the pause: %v", ctx.Err())
	}

	// 对象继续读取前，另一个请求必须能用池中唯一的连接查询采集批次。
	waitBefore := db.Stats()
	probeCtx, stopProbe := context.WithTimeout(ctx, 3*time.Second)
	probed, probeErr := captures.GetBatch(probeCtx, f.TenantID, batch.ID)
	stopProbe()
	waitAfter := db.Stats()
	close(objects.resume)
	t.Logf("paused object upload SQL probe: pool waits=%d wait duration=%s", waitAfter.WaitCount-waitBefore.WaitCount, waitAfter.WaitDuration-waitBefore.WaitDuration)

	select {
	case completion := <-finished:
		if probeErr != nil || probed.ID != batch.ID {
			t.Fatalf("unrelated query could not use the single connection while object upload was paused: %v", probeErr)
		}
		if completion.err != nil {
			t.Fatal(completion.err)
		}
		session := completion.session
		if session.Status != "completed" || session.ConfirmedOffset != int64(len(payload)) || session.CaptureFileID == "" {
			t.Fatalf("unexpected completion: %+v", session)
		}
		assertSlowCaptureUploadObject(t, ctx, fileStore, session, payload, objects)
		replay, err := service.Complete(ctx, f.TenantID, f.AdminID, session.ID, captureupload.CompleteInput{SHA256: hash})
		if err != nil || replay.CaptureFileID != session.CaptureFileID || replay.FileAssetID != session.FileAssetID || objects.puts.Load() != 1 {
			t.Fatalf("receipt replay: %+v puts=%d err=%v", replay, objects.puts.Load(), err)
		}
		registered, err := captures.ListFiles(ctx, f.TenantID, batch.ID)
		if err != nil || len(registered) != 1 || registered[0].ID != session.CaptureFileID {
			t.Fatalf("expected a single capture file matching the receipt: %+v %v", registered, err)
		}
	case <-ctx.Done():
		t.Fatalf("upload did not complete after object storage resumed: %v", ctx.Err())
	}
}

func assertSlowCaptureUploadObject(t *testing.T, ctx context.Context, fileStore *files.PostgresStore, session captureupload.Session, payload []byte, objects files.ObjectStorage) {
	t.Helper()
	asset, err := fileStore.Get(ctx, session.TenantID, session.FileAssetID)
	if err != nil || asset.Lifecycle != files.LifecycleActive {
		t.Fatalf("active source asset: %+v %v", asset, err)
	}
	body, err := objects.Get(ctx, asset.StorageBucket, asset.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	saved, err := io.ReadAll(body)
	if err != nil || !bytes.Equal(saved, payload) {
		t.Fatalf("saved source does not match the original: %v", err)
	}
}

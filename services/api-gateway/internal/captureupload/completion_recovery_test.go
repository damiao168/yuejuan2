package captureupload

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

type failingPutOnce struct {
	files.ObjectStorage
	failed bool
}

type cancelPutOnce struct {
	files.ObjectStorage
	cancel context.CancelFunc
}

func (s *cancelPutOnce) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
		return ctx.Err()
	}
	return s.ObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

type contextAwareResume struct{ Store }

func (s contextAwareResume) Resume(ctx context.Context, tenant, id, code, token string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Store.Resume(ctx, tenant, id, code, token)
}

func TestCancelledCompletionRestoresRetryableState(t *testing.T) {
	// 模拟存储写入中取消请求，要求恢复动作脱离已取消的上下文，避免卡在 finalizing。
	ctx, service, _, _, objects, batch := newTestService(t)
	id, input := readyCompletion(t, service, batch)
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	service.store = contextAwareResume{Store: service.store}
	service.objects = &cancelPutOnce{ObjectStorage: objects, cancel: cancel}
	if _, err := service.Complete(request, testTenant, testActor, id, CompleteInput{SHA256: input.SHA256}); !errors.Is(err, ErrStorage) {
		t.Fatalf("cancelled complete: %v", err)
	}
	session, err := service.store.Get(ctx, testTenant, id)
	if err != nil || session.Status != "uploading" {
		t.Fatalf("cancelled request stranded session: %+v %v", session, err)
	}
	if _, err = service.Complete(ctx, testTenant, testActor, id, CompleteInput{SHA256: input.SHA256}); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func (s *failingPutOnce) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if !s.failed {
		s.failed = true
		return errors.New("temporary object failure")
	}
	return s.ObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

func readyCompletion(t *testing.T, service *Service, batch string) (string, InitInput) {
	t.Helper()
	payload := []byte("\x89PNG\r\n\x1a\nrecovery test")
	input := testInitInput(payload, batch)
	init, err := service.Init(context.Background(), testTenant, testActor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AppendChunk(context.Background(), testTenant, init.RemoteUploadID, ChunkInput{SHA256: input.SHA256, Data: payload}); err != nil {
		t.Fatal(err)
	}
	return init.RemoteUploadID, input
}

func TestCompletionRetriesFailedObjectWithoutDuplicateAsset(t *testing.T) {
	ctx, service, captures, _, objects, batch := newTestService(t)
	service.objects = &failingPutOnce{ObjectStorage: objects}
	id, input := readyCompletion(t, service, batch)
	if _, err := service.Complete(ctx, testTenant, testActor, id, CompleteInput{SHA256: input.SHA256}); !errors.Is(err, ErrStorage) {
		t.Fatalf("first: %v", err)
	}
	asset, found, err := service.fileStore.FindDuplicate(ctx, testTenant, "capture_batch", batch, input.SHA256)
	if err != nil || !found {
		t.Fatalf("failed asset missing: %v", err)
	}
	result, err := service.Complete(ctx, testTenant, testActor, id, CompleteInput{SHA256: input.SHA256})
	if err != nil || result.Status != "completed" || result.FileAssetID != asset.ID {
		t.Fatalf("retry: %+v %v", result, err)
	}
	registered, err := captures.ListFiles(ctx, testTenant, batch)
	if err != nil || len(registered) != 1 {
		t.Fatalf("registrations: %+v %v", registered, err)
	}
}

func TestCompletionLeaseRecoveryFencesOldFinalizer(t *testing.T) {
	ctx, service, _, _, _, batch := newTestService(t)
	id, input := readyCompletion(t, service, batch)
	store := service.store.(*MemoryStore)
	old, _, err := store.BeginComplete(ctx, testTenant, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.BeginComplete(ctx, testTenant, id); !errors.Is(err, ErrConflict) {
		t.Fatalf("live lease: %v", err)
	}
	store.now = func() time.Time { return old.CompletionLeaseUntil.Add(time.Second) }
	newOwner, _, err := store.BeginComplete(ctx, testTenant, id)
	if err != nil || newOwner.CompletionToken == old.CompletionToken {
		t.Fatalf("takeover: %+v %v", newOwner, err)
	}
	if err = store.Resume(ctx, testTenant, id, "late failure", old.CompletionToken); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale resume: %v", err)
	}
	if _, err = store.Complete(ctx, testTenant, id, "asset", "capture", old.CompletionToken); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale complete: %v", err)
	}
	store.now = func() time.Time { return newOwner.CompletionLeaseUntil.Add(time.Second) }
	result, err := service.Complete(ctx, testTenant, testActor, id, CompleteInput{SHA256: input.SHA256})
	if err != nil || result.Status != "completed" {
		t.Fatalf("recovery: %+v %v", result, err)
	}
}

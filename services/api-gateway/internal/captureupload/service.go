package captureupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
)

type Service struct {
	store      Store
	captures   capture.Store
	fileStore  files.LifecycleStore
	objects    files.ObjectStorage
	fileConfig config.FileConfig
	chunkSize  int64
}

func NewService(store Store, captures capture.Store, fileStore files.LifecycleStore, objects files.ObjectStorage, fileConfig config.FileConfig) *Service {
	if fileConfig.Bucket == "" {
		fileConfig.Bucket = "edugrade-files"
	}
	if fileConfig.MaxUploadBytes <= 0 {
		fileConfig.MaxUploadBytes = 100 * 1024 * 1024
	}
	if len(fileConfig.AllowedExtensions) == 0 {
		fileConfig.AllowedExtensions = []string{".pdf", ".png", ".jpg", ".jpeg", ".tif", ".tiff"}
	}
	return &Service{store: store, captures: captures, fileStore: fileStore, objects: objects, fileConfig: fileConfig, chunkSize: DefaultChunkSize}
}

func (s *Service) Init(ctx context.Context, tenantID, actorID string, input InitInput) (InitResponse, error) {
	input, err := s.validateInit(input)
	if err != nil {
		return InitResponse{}, err
	}
	batch, err := s.captures.GetBatch(ctx, tenantID, input.BatchID)
	if err != nil {
		return InitResponse{}, translateCaptureError(err)
	}
	if batch.ExamID != input.ExamID {
		return InitResponse{}, ErrConflict
	}
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return InitResponse{}, ErrConflict
	}
	session, existing, err := s.store.Init(ctx, tenantID, actorID, input, s.chunkSize)
	if err != nil {
		return InitResponse{}, err
	}
	return initResponse(session, existing), nil
}

func (s *Service) Get(ctx context.Context, tenantID, uploadID string) (Session, error) {
	return s.store.Get(ctx, tenantID, uploadID)
}

func (s *Service) AppendChunk(ctx context.Context, tenantID, uploadID string, input ChunkInput) (Session, error) {
	if input.Offset < 0 || len(input.Data) == 0 || int64(len(input.Data)) > s.chunkSize {
		return Session{}, ErrInvalidInput
	}
	input.SHA256 = normalizeSHA256(input.SHA256)
	if input.SHA256 == "" || chunkHash(input.Data) != input.SHA256 {
		return Session{}, ErrHashMismatch
	}
	session, err := s.store.Get(ctx, tenantID, uploadID)
	if err != nil {
		return Session{}, err
	}
	if session.Status != "uploading" {
		if session.Status == "completed" && input.Offset < session.ConfirmedOffset {
			return session, nil
		}
		return Session{}, ErrConflict
	}
	if input.Offset+int64(len(input.Data)) > session.Size {
		return Session{}, ErrInvalidInput
	}
	return s.store.AppendChunk(ctx, tenantID, uploadID, input)
}

func (s *Service) Complete(ctx context.Context, tenantID, actorID, uploadID string, input CompleteInput) (Session, error) {
	ctx, cancel := context.WithTimeout(ctx, completionTimeout)
	defer cancel()
	input.SHA256 = normalizeSHA256(input.SHA256)
	session, shouldMaterialize, err := s.store.BeginComplete(ctx, tenantID, uploadID)
	if err != nil {
		return Session{}, err
	}
	if input.SHA256 != "" && input.SHA256 != session.SHA256 {
		_ = s.store.Fail(ctx, tenantID, uploadID, "final_hash_request_mismatch", session.CompletionToken)
		return Session{}, ErrHashMismatch
	}
	if !shouldMaterialize {
		return session, nil
	}
	// Best-effort recovery also runs when the request context has expired.
	// The token prevents an old finalizer from resetting a newer lease.
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		_ = s.store.Resume(cleanup, tenantID, uploadID, "completion_interrupted", session.CompletionToken)
	}()

	// First hash the exact persisted chunk sequence.  This prevents a partial
	// or corrupted upload from being materialized as a capture source asset.
	digest := sha256.New()
	sample := make([]byte, 0, 512)
	if err := s.store.ReadChunks(ctx, tenantID, uploadID, func(chunk []byte) error {
		if len(sample) < cap(sample) {
			remaining := cap(sample) - len(sample)
			if remaining > len(chunk) {
				remaining = len(chunk)
			}
			sample = append(sample, chunk[:remaining]...)
		}
		_, writeErr := digest.Write(chunk)
		return writeErr
	}); err != nil {
		_ = s.store.Resume(ctx, tenantID, uploadID, "chunk_read_failed", session.CompletionToken)
		return Session{}, fmt.Errorf("%w: %v", ErrStorage, err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != session.SHA256 {
		_ = s.store.Fail(ctx, tenantID, uploadID, "final_hash_mismatch", session.CompletionToken)
		return Session{}, ErrHashMismatch
	}
	if _, err := files.ValidateFileType(session.OriginalName, session.ContentType, files.SniffContentType(sample), s.fileConfig.AllowedExtensions); err != nil {
		_ = s.store.Fail(ctx, tenantID, uploadID, "content_type_mismatch", session.CompletionToken)
		return Session{}, ErrUnsupportedType
	}

	asset, reused, err := s.findOrCreateAsset(ctx, tenantID, actorID, session)
	if err != nil {
		_ = s.store.Resume(ctx, tenantID, uploadID, "asset_prepare_failed", session.CompletionToken)
		return Session{}, err
	}
	if !reused {
		if err := s.streamToObject(ctx, session, asset); err != nil {
			_, _ = s.fileStore.MarkUploadFailed(ctx, tenantID, asset.ID, asset.Revision, "resumable_upload_object_put_failed")
			_ = s.store.Resume(ctx, tenantID, uploadID, "object_put_failed", session.CompletionToken)
			return Session{}, fmt.Errorf("%w: %v", ErrStorage, err)
		}
		asset, err = s.fileStore.Activate(ctx, tenantID, asset.ID, asset.Revision)
		if err != nil {
			_ = s.store.Resume(ctx, tenantID, uploadID, "asset_activate_failed", session.CompletionToken)
			return Session{}, fmt.Errorf("%w: %v", ErrStorage, err)
		}
	}

	captureFile, err := s.captures.RegisterFile(ctx, tenantID, session.BatchID, actorID, capture.RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: session.IdempotencyKey}, capture.FileAssetSnapshot{
		ID: asset.ID, ExamID: asset.ExamID, OriginalName: asset.OriginalName, ContentType: asset.ContentType, SizeBytes: asset.SizeBytes, SHA256: asset.HashSHA256,
	})
	if err != nil {
		_ = s.store.Resume(ctx, tenantID, uploadID, "capture_file_register_failed", session.CompletionToken)
		return Session{}, translateCaptureError(err)
	}
	return s.store.Complete(ctx, tenantID, uploadID, asset.ID, captureFile.ID, session.CompletionToken)
}

func (s *Service) validateInit(input InitInput) (InitInput, error) {
	input.SHA256 = normalizeSHA256(input.SHA256)
	input.MIME = strings.ToLower(strings.TrimSpace(input.MIME))
	input.ExamID = strings.TrimSpace(input.ExamID)
	input.BatchID = strings.TrimSpace(input.BatchID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Filename = strings.TrimSpace(input.Filename)
	if input.SHA256 == "" || input.Size <= 0 || input.Size > s.fileConfig.MaxUploadBytes || input.ExamID == "" || input.BatchID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 200 {
		return InitInput{}, ErrInvalidInput
	}
	if input.Filename == "" {
		input.Filename = "offline-scan" + extensionForMIME(input.MIME)
	}
	cleaned, err := files.CleanFilename(input.Filename)
	if err != nil {
		return InitInput{}, ErrInvalidInput
	}
	if extensionForMIME(input.MIME) == "" || !mimeMatchesExtension(input.MIME, filepath.Ext(cleaned)) {
		return InitInput{}, ErrUnsupportedType
	}
	input.Filename = cleaned
	return input, nil
}

func (s *Service) findOrCreateAsset(ctx context.Context, tenantID, actorID string, session Session) (files.FileAsset, bool, error) {
	if existing, found, err := s.fileStore.FindDuplicate(ctx, tenantID, "capture_batch", session.BatchID, session.SHA256); err != nil {
		return files.FileAsset{}, false, err
	} else if found {
		if existing.Lifecycle == files.LifecyclePendingUpload || existing.Lifecycle == files.LifecycleUploadFailed {
			return existing, false, nil
		}
		if existing.Lifecycle != files.LifecycleActive {
			return files.FileAsset{}, false, ErrConflict
		}
		return existing, true, nil
	}
	storageKey, err := files.BuildStorageKey(tenantID, session.SHA256, session.OriginalName)
	if err != nil {
		return files.FileAsset{}, false, err
	}
	asset, err := s.fileStore.CreatePending(ctx, files.CreateAssetInput{
		TenantID: tenantID, ExamID: session.ExamID, OwnerType: "capture_batch", OwnerID: session.BatchID,
		OriginalName: session.OriginalName, ContentType: session.ContentType, SizeBytes: session.Size, HashSHA256: session.SHA256,
		StorageBucket: s.fileConfig.Bucket, StorageKey: storageKey, Visibility: "private", UploadedBy: actorID,
	})
	if errors.Is(err, files.ErrDuplicateFile) {
		return files.FileAsset{}, false, ErrConflict
	}
	if err != nil {
		return files.FileAsset{}, false, err
	}
	return asset, false, nil
}

func (s *Service) streamToObject(ctx context.Context, session Session, asset files.FileAsset) error {
	reader, writer := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		err := s.store.ReadChunks(ctx, session.TenantID, session.ID, func(chunk []byte) error {
			_, writeErr := writer.Write(chunk)
			return writeErr
		})
		_ = writer.CloseWithError(err)
		errCh <- err
	}()
	putErr := s.objects.Put(ctx, asset.StorageBucket, asset.StorageKey, reader, asset.SizeBytes, asset.ContentType)
	// 写入提前失败时也要关闭管道读端，唤醒仍在写分块的协程后再等待它退出。
	_ = reader.Close()
	readErr := <-errCh
	if putErr != nil {
		return putErr
	}
	return readErr
}

func normalizeSHA256(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != sha256.Size*2 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return value
}

func chunkHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func extensionForMIME(contentType string) string {
	switch contentType {
	case "application/pdf":
		return ".pdf"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/tiff":
		return ".tiff"
	default:
		return ""
	}
}

func mimeMatchesExtension(contentType, ext string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	switch contentType {
	case "application/pdf":
		return ext == ".pdf"
	case "image/png":
		return ext == ".png"
	case "image/jpeg":
		return ext == ".jpg" || ext == ".jpeg"
	case "image/tiff":
		return ext == ".tif" || ext == ".tiff"
	default:
		return false
	}
}

func translateCaptureError(err error) error {
	if errors.Is(err, capture.ErrNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, capture.ErrConflict) || errors.Is(err, capture.ErrInvalidTransition) || errors.Is(err, capture.ErrDuplicateFile) {
		return ErrConflict
	}
	if errors.Is(err, capture.ErrInvalidInput) {
		return ErrInvalidInput
	}
	return err
}

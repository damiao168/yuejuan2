package capture

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"time"
)

// 同一批次内按幂等键复用注册结果，活动文件的哈希重复则标为 duplicate；失败文件可再次上传。
func (s *MemoryStore) RegisterFile(_ context.Context, tenantID, batchID, actorID string, input RegisterFileInput, asset FileAssetSnapshot) (File, error) {
	if validateRegisterFile(&input, asset) != nil {
		return File{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return File{}, ErrNotFound
	}
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return File{}, ErrInvalidTransition
	}
	for _, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.IdempotencyKey == input.IdempotencyKey {
			if x.Status == memoryExcludedFile || x.FileAssetID != input.FileAssetID || x.UploadGroupID != input.UploadGroupID || x.UploadGroupFileCount != input.UploadGroupFileCount || x.UploadGroupFileIndex != input.UploadGroupFileIndex || x.PagesPerStudent != input.PagesPerStudent {
				return File{}, ErrConflict
			}
			return x, nil
		}
		if input.UploadGroupID != "" && x.Status != memoryExcludedFile && x.TenantID == tenantID && x.CaptureBatchID == batchID && x.UploadGroupID == input.UploadGroupID && (x.UploadGroupFileCount != input.UploadGroupFileCount || x.PagesPerStudent != input.PagesPerStudent || x.UploadGroupFileIndex == input.UploadGroupFileIndex) {
			return File{}, ErrConflict
		}
	}
	status := s.registeredFileStatusLocked(tenantID, batchID, asset.SHA256)
	x := File{ID: uuid.NewString(), TenantID: tenantID, CaptureBatchID: batchID, FileAssetID: asset.ID, OriginalName: asset.OriginalName, ContentType: asset.ContentType, SHA256: asset.SHA256, ByteSize: asset.SizeBytes, Status: status, IdempotencyKey: input.IdempotencyKey, UploadedBy: actorID, CreatedAt: time.Now().UTC()}
	x.UploadGroupID, x.UploadGroupFileCount, x.UploadGroupFileIndex, x.PagesPerStudent = input.UploadGroupID, input.UploadGroupFileCount, input.UploadGroupFileIndex, input.PagesPerStudent
	s.files[x.ID] = x
	batch.FileCount++
	batch.Status = "uploading"
	batch.Revision++
	s.batches[batchID] = batch
	return x, nil
}

// 调用方持有注册文件的写锁；重复判定只看同批次仍可处理的文件，失败文件允许重传。
func (s *MemoryStore) registeredFileStatusLocked(tenantID, batchID, sha256 string) string {
	for _, file := range s.files {
		if file.TenantID != tenantID || file.CaptureBatchID != batchID || file.SHA256 != sha256 {
			continue
		}
		switch file.Status {
		case "uploaded", "queued", "processing", "completed":
			return "duplicate"
		}
	}
	return "uploaded"
}

func (s *MemoryStore) ListFiles(_ context.Context, tenantID, batchID string) ([]File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []File{}
	for _, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && x.Status != memoryExcludedFile {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) GetFile(_ context.Context, tenantID, fileID string) (File, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x, ok := s.files[fileID]
	if !ok || x.TenantID != tenantID || x.Status == memoryExcludedFile {
		return File{}, ErrNotFound
	}
	return x, nil
}

func (s *MemoryStore) ApplyFileFailure(_ context.Context, tenantID, fileID, errorCode string, retryable bool) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyFileFailureLocked(tenantID, fileID, errorCode, retryable)
}

func (s *MemoryStore) applyFileFailureLocked(tenantID, fileID, errorCode string, retryable bool) (File, error) {
	x, ok := s.files[fileID]
	if !ok || x.TenantID != tenantID || x.Status == memoryExcludedFile {
		return File{}, ErrNotFound
	}
	x.Status = "failed"
	if retryable {
		x.Status = "queued"
	}
	x.ErrorCode = errorCode
	s.files[fileID] = x
	if batch, exists := s.batches[x.CaptureBatchID]; exists {
		batch.Revision++
		if retryable {
			batch.Status = "processing"
		} else {
			batch.Status = "needs_review"
			batch.FailedCount = 0
			for _, file := range s.files {
				if file.CaptureBatchID == batch.ID && file.Status == "failed" {
					batch.FailedCount++
				}
			}
		}
		s.batches[batch.ID] = batch
	}
	return x, nil
}

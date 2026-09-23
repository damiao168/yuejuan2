package files

import (
	"context"
	"fmt"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type MemoryStore struct {
	mu     sync.RWMutex
	next   int
	assets map[string]FileAsset
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, assets: map[string]FileAsset{}}
}

func (s *MemoryStore) Create(_ context.Context, input CreateAssetInput) (FileAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.findDuplicateLocked(input.TenantID, input.OwnerType, input.OwnerID, input.HashSHA256); ok {
		return existing, ErrDuplicateFile
	}
	asset := s.newAssetLocked(input, LifecycleActive)
	s.assets[asset.ID] = asset
	return asset, nil
}

func (s *MemoryStore) CreatePending(_ context.Context, input CreateAssetInput) (FileAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.findDuplicateLocked(input.TenantID, input.OwnerType, input.OwnerID, input.HashSHA256); ok {
		return existing, ErrDuplicateFile
	}
	asset := s.newAssetLocked(input, LifecyclePendingUpload)
	s.assets[asset.ID] = asset
	return asset, nil
}

func (s *MemoryStore) newAssetLocked(input CreateAssetInput, lifecycle string) FileAsset {
	asset := FileAsset{
		ID:            fmt.Sprintf("file-%d", s.next),
		TenantID:      input.TenantID,
		SchoolID:      input.SchoolID,
		ExamID:        input.ExamID,
		SubmissionID:  input.SubmissionID,
		OwnerType:     input.OwnerType,
		OwnerID:       input.OwnerID,
		OriginalName:  input.OriginalName,
		ContentType:   input.ContentType,
		SizeBytes:     input.SizeBytes,
		HashSHA256:    input.HashSHA256,
		StorageBucket: input.StorageBucket,
		StorageKey:    input.StorageKey,
		Visibility:    input.Visibility,
		UploadedBy:    input.UploadedBy,
		Lifecycle:     lifecycle,
		Revision:      1,
		CreatedAt:     time.Now().UTC(),
	}
	s.next++
	return asset
}

func (s *MemoryStore) FindDuplicate(_ context.Context, tenantID string, ownerType string, ownerID string, hashSHA256 string) (FileAsset, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.findDuplicateLocked(tenantID, ownerType, ownerID, hashSHA256)
	return asset, ok, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID string, id string) (FileAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.assets[id]
	if !ok || asset.TenantID != tenantID || asset.DeletedAt != nil || asset.Lifecycle != LifecycleActive {
		return FileAsset{}, ErrNotFound
	}
	return asset, nil
}

func (s *MemoryStore) ValidateCreateScope(_ context.Context, scope auth.AccessScope, input CreateAssetInput) error {
	if !AllowsCreate(scope, input) {
		return ErrForbidden
	}
	return nil
}

func (s *MemoryStore) GetScoped(_ context.Context, scope auth.AccessScope, id string) (FileAsset, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	asset, ok := s.assets[id]
	if !ok || asset.DeletedAt != nil {
		return FileAsset{}, ErrNotFound
	}
	if !AllowsAsset(scope, asset) {
		return FileAsset{}, ErrForbidden
	}
	if asset.LegalHold || (asset.RetentionUntil != nil && asset.RetentionUntil.After(time.Now().UTC())) {
		return FileAsset{}, ErrForbidden
	}
	return asset, nil
}

func (s *MemoryStore) Activate(_ context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecyclePendingUpload, LifecycleUploadFailed}, LifecycleActive, "", false)
}

func (s *MemoryStore) RecoverMissing(_ context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecycleMissingObject}, LifecycleActive, "", false)
}

func (s *MemoryStore) MarkMissing(_ context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecycleActive}, LifecycleMissingObject, "object_missing", false)
}

func (s *MemoryStore) MarkUploadFailed(_ context.Context, tenantID, id string, expectedRevision int64, detail string) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecyclePendingUpload}, LifecycleUploadFailed, detail, false)
}

func (s *MemoryStore) BeginDelete(_ context.Context, scope auth.AccessScope, id string, expectedRevision int64) (FileAsset, error) {
	s.mu.RLock()
	asset, ok := s.assets[id]
	s.mu.RUnlock()
	if !ok || asset.DeletedAt != nil {
		return FileAsset{}, ErrNotFound
	}
	if !AllowsAsset(scope, asset) {
		return FileAsset{}, ErrForbidden
	}
	return s.transition(scope.TenantID, id, expectedRevision, []string{LifecycleActive, LifecycleDeleteFailed}, LifecyclePendingDelete, "", false)
}

func (s *MemoryStore) CompleteDelete(_ context.Context, tenantID, id string, expectedRevision int64) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecyclePendingDelete}, LifecycleDeleted, "", true)
}

func (s *MemoryStore) MarkDeleteFailed(_ context.Context, tenantID, id string, expectedRevision int64, detail string) (FileAsset, error) {
	return s.transition(tenantID, id, expectedRevision, []string{LifecyclePendingDelete}, LifecycleDeleteFailed, detail, false)
}

func (s *MemoryStore) transition(tenantID, id string, expectedRevision int64, from []string, to, detail string, deleted bool) (FileAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	asset, ok := s.assets[id]
	if !ok || asset.TenantID != tenantID || asset.DeletedAt != nil {
		return FileAsset{}, ErrNotFound
	}
	if asset.Revision != expectedRevision {
		return FileAsset{}, ErrConflict
	}
	allowed := false
	for _, state := range from {
		allowed = allowed || asset.Lifecycle == state
	}
	if !allowed {
		return FileAsset{}, ErrConflict
	}
	asset.Lifecycle = to
	asset.LastError = detail
	if detail != "" {
		now := time.Now().UTC()
		asset.LastErrorAt = &now
	}
	if to == LifecycleDeleteFailed {
		asset.DeleteAttempts++
	}
	asset.Revision++
	if deleted {
		now := time.Now().UTC()
		asset.DeletedAt = &now
	}
	s.assets[id] = asset
	return asset, nil
}

func (s *MemoryStore) Delete(_ context.Context, tenantID string, id string) (FileAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	asset, ok := s.assets[id]
	if !ok || asset.TenantID != tenantID || asset.DeletedAt != nil {
		return FileAsset{}, ErrNotFound
	}
	now := time.Now().UTC()
	asset.DeletedAt = &now
	asset.Lifecycle = LifecycleDeleted
	asset.Revision++
	s.assets[id] = asset
	return asset, nil
}

func (s *MemoryStore) findDuplicateLocked(tenantID string, ownerType string, ownerID string, hashSHA256 string) (FileAsset, bool) {
	for _, asset := range s.assets {
		if asset.TenantID == tenantID && asset.OwnerType == ownerType && asset.OwnerID == ownerID && asset.HashSHA256 == hashSHA256 && asset.DeletedAt == nil {
			return asset, true
		}
	}
	return FileAsset{}, false
}

package capture

import (
	"context"
	"github.com/google/uuid"
	"sort"
	"time"
)

func (s *MemoryStore) CreateBatch(_ context.Context, tenantID, examID, actorID string, input CreateBatchInput) (Batch, error) {
	if validateCreateBatch(&input) != nil || tenantID == "" || examID == "" || actorID == "" {
		return Batch{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + actorID + ":" + input.IdempotencyKey
	if input.IdempotencyKey != "" {
		if id, ok := s.batchKeys[key]; ok {
			batch := s.batches[id]
			if batchCommandHash(examID, input) != batchCommandHash(batch.ExamID, CreateBatchInput{Name: batch.Name, SourceType: batch.SourceType, ScannerDevice: batch.ScannerDevice}) {
				return Batch{}, ErrConflict
			}
			return batch, nil
		}
	}
	now := time.Now().UTC()
	item := Batch{ID: uuid.NewString(), TenantID: tenantID, ExamID: examID, Name: input.Name, SourceType: input.SourceType, Purpose: input.Purpose, Status: "draft", Revision: 1, OperatorID: actorID, ScannerDevice: input.ScannerDevice, CreatedAt: now}
	s.batches[item.ID] = item
	if input.IdempotencyKey != "" {
		s.batchKeys[key] = item.ID
	}
	return item, nil
}

func (s *MemoryStore) RecoverBatchCommand(_ context.Context, tenantID, examID, actorID, commandID string) (BatchCommandRecovery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := BatchCommandRecovery{CommandID: commandID, Status: "not_accepted"}
	if id, ok := s.batchKeys[tenantID+":"+actorID+":"+commandID]; ok {
		batch := s.batches[id]
		if batch.ExamID != examID {
			return result, ErrConflict
		}
		result.Batch = &batch
		result.Status = "succeeded"
	}
	return result, nil
}

func (s *MemoryStore) ListBatches(_ context.Context, tenantID, examID string, filter BatchListFilter) ([]Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Batch{}
	for _, x := range s.batches {
		if x.TenantID == tenantID && x.ExamID == examID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		out = captureBatchesAfterCursor(out, filter)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// 输入已按创建时间、ID 降序排列；游标所在条目不重复返回，同时间只保留较小 ID。
func captureBatchesAfterCursor(batches []Batch, filter BatchListFilter) []Batch {
	start := 0
	for start < len(batches) {
		item := batches[start]
		if item.CreatedAt.Before(filter.CursorCreatedAt) ||
			(item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
			break
		}
		start++
	}
	return batches[start:]
}

func (s *MemoryStore) GetBatch(_ context.Context, tenantID, batchID string) (Batch, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	x, ok := s.batches[batchID]
	if !ok || x.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	return x, nil
}

func (s *MemoryStore) QueueBatch(_ context.Context, tenantID, batchID, actorID string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return Batch{}, ErrInvalidTransition
	}
	count := 0
	for id, x := range s.files {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID && (x.Status == "uploaded" || x.Status == "failed") {
			x.Status = "queued"
			x.ErrorCode = ""
			s.files[id] = x
			count++
		}
	}
	if count == 0 {
		return Batch{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	batch.Status = "processing"
	batch.StartedAt = &now
	batch.Revision++
	s.batches[batchID] = batch
	return batch, nil
}

func (s *MemoryStore) SetBatchStatus(_ context.Context, tenantID, batchID, actorID, status, reason string) (Batch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.batches[batchID]
	if !ok || x.TenantID != tenantID {
		return Batch{}, ErrNotFound
	}
	if !canSetBatchStatus(x.Status, status) {
		return Batch{}, ErrInvalidTransition
	}
	x.Status = status
	x.Revision++
	if status == "completed" {
		now := time.Now().UTC()
		x.CompletedAt = &now
	} else {
		x.CompletedAt = nil
	}
	s.batches[batchID] = x
	return x, nil
}

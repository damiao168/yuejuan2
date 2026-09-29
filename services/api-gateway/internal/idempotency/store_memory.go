package idempotency

import (
	"context"
	"sync"
	"time"
)

type memoryEntry struct {
	record    Record
	expiresAt time.Time
	updatedAt time.Time
}

type MemoryStore struct {
	mu      sync.Mutex
	records map[string]memoryEntry
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[string]memoryEntry{}}
}

func memoryKey(input BeginInput) string {
	return input.TenantID + "|" + input.ActorID + "|" + input.Method + "|" + input.Route + "|" + input.Key
}

func (s *MemoryStore) Begin(_ context.Context, input BeginInput) (Record, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(input)
	if existing, ok := s.records[key]; ok {
		expired := !time.Now().UTC().Before(existing.expiresAt)
		// 可恢复命令即使超过缓存期限，也必须保留处理中请求的指纹，防止换正文接管。
		if expired && (existing.record.State == "completed" || !input.AllowTakeover) {
			delete(s.records, key)
		} else {
			if existing.record.RequestHash != input.RequestHash {
				return Record{}, false, ErrKeyConflict
			}
			if existing.record.State == "processing" {
				if input.AllowTakeover && !input.StaleBefore.IsZero() && existing.updatedAt.Before(input.StaleBefore) {
					existing.updatedAt = time.Now().UTC()
					existing.record.UpdatedAt = existing.updatedAt
					existing.expiresAt = input.ExpiresAt
					s.records[key] = existing
					return cloneRecord(existing.record), true, nil
				}
				return Record{}, false, ErrInProgress
			}
			return cloneRecord(existing.record), false, nil
		}
	}
	record := Record{State: "processing", RequestHash: input.RequestHash, RequestBody: append([]byte(nil), input.RequestBody...)}
	now := time.Now().UTC()
	record.UpdatedAt = now
	s.records[key] = memoryEntry{record: record, expiresAt: input.ExpiresAt, updatedAt: now}
	return record, true, nil
}

func (s *MemoryStore) Complete(_ context.Context, input BeginInput, status int, headers map[string]string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(input)
	entry, ok := s.records[key]
	if !ok || entry.record.RequestHash != input.RequestHash {
		return ErrKeyConflict
	}
	entry.record = Record{State: "completed", RequestHash: input.RequestHash, RequestBody: append([]byte(nil), entry.record.RequestBody...), ResponseStatus: status, ResponseHeaders: cloneHeaders(headers), ResponseBody: append([]byte(nil), body...)}
	entry.updatedAt = time.Now().UTC()
	entry.record.UpdatedAt = entry.updatedAt
	s.records[key] = entry
	return nil
}

func (s *MemoryStore) Abort(_ context.Context, input BeginInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryKey(input)
	if entry, ok := s.records[key]; ok && entry.record.RequestHash == input.RequestHash {
		delete(s.records, key)
	}
	return nil
}

func cloneRecord(input Record) Record {
	input.RequestBody = append([]byte(nil), input.RequestBody...)
	input.ResponseHeaders = cloneHeaders(input.ResponseHeaders)
	input.ResponseBody = append([]byte(nil), input.ResponseBody...)
	return input
}

func cloneHeaders(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

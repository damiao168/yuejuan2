package captureupload

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

type memoryChunk struct {
	offset int64
	hash   string
	data   []byte
}

type MemoryStore struct {
	mu       sync.RWMutex
	now      func() time.Time
	sessions map[string]Session
	chunks   map[string]map[int64]memoryChunk
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{now: time.Now, sessions: map[string]Session{}, chunks: map[string]map[int64]memoryChunk{}}
}

func (s *MemoryStore) Init(_ context.Context, tenantID, _ string, input InitInput, chunkSize int64) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, current := range s.sessions {
		if current.TenantID != tenantID || current.BatchID != input.BatchID || current.IdempotencyKey != input.IdempotencyKey {
			continue
		}
		if current.ExamID != input.ExamID || current.SHA256 != input.SHA256 || current.Size != input.Size || current.ContentType != input.MIME {
			return Session{}, false, ErrConflict
		}
		return current, true, nil
	}
	now := time.Now().UTC()
	session := Session{
		ID: uuid.NewString(), TenantID: tenantID, ExamID: input.ExamID, BatchID: input.BatchID,
		IdempotencyKey: input.IdempotencyKey, OriginalName: input.Filename, ContentType: input.MIME,
		SHA256: input.SHA256, Size: input.Size, ChunkSize: chunkSize, Status: "uploading", CreatedAt: now,
	}
	s.sessions[session.ID] = session
	s.chunks[session.ID] = map[int64]memoryChunk{}
	return session, false, nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, uploadID string) (Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return Session{}, ErrNotFound
	}
	return session, nil
}

func (s *MemoryStore) AppendChunk(_ context.Context, tenantID, uploadID string, input ChunkInput) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return Session{}, ErrNotFound
	}
	if session.Status != "uploading" {
		return Session{}, ErrConflict
	}
	if input.Offset == session.ConfirmedOffset {
		if input.Offset+int64(len(input.Data)) > session.Size {
			return Session{}, ErrInvalidInput
		}
		copyData := append([]byte(nil), input.Data...)
		s.chunks[uploadID][input.Offset] = memoryChunk{offset: input.Offset, hash: input.SHA256, data: copyData}
		session.ConfirmedOffset += int64(len(copyData))
		s.sessions[uploadID] = session
		return session, nil
	}
	if input.Offset < session.ConfirmedOffset {
		// 旧偏移只接受同一分块的重传；不能用新内容覆盖已确认的字节范围。
		existing, exists := s.chunks[uploadID][input.Offset]
		if exists && existing.hash == input.SHA256 && len(existing.data) == len(input.Data) {
			return session, nil
		}
	}
	return Session{}, fmt.Errorf("%w: expected offset %d", ErrConflict, session.ConfirmedOffset)
}

func (s *MemoryStore) BeginComplete(_ context.Context, tenantID, uploadID string) (Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return Session{}, false, ErrNotFound
	}
	if session.Status == "completed" {
		return session, false, nil
	}
	if (session.Status == "finalizing" && session.CompletionLeaseUntil.After(s.now())) || session.Status == "failed" {
		return Session{}, false, ErrConflict
	}
	if (session.Status != "uploading" && session.Status != "finalizing") || session.ConfirmedOffset != session.Size {
		return Session{}, false, ErrIncomplete
	}
	// 过期租约可被接管，但必须换令牌，使旧请求不能再回退或提交该会话。
	session.Status = "finalizing"
	session.CompletionToken = uuid.NewString()
	session.CompletionLeaseUntil = s.now().Add(completionLeaseDuration)
	s.sessions[uploadID] = session
	return session, true, nil
}

func (s *MemoryStore) ReadChunks(_ context.Context, tenantID, uploadID string, consume func([]byte) error) error {
	s.mu.RLock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		s.mu.RUnlock()
		return ErrNotFound
	}
	chunks := make([]memoryChunk, 0, len(s.chunks[uploadID]))
	for _, chunk := range s.chunks[uploadID] {
		chunks = append(chunks, memoryChunk{offset: chunk.offset, hash: chunk.hash, data: append([]byte(nil), chunk.data...)})
	}
	s.mu.RUnlock()
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].offset < chunks[j].offset })
	offset := int64(0)
	for _, chunk := range chunks {
		if chunk.offset != offset {
			return ErrIncomplete
		}
		if err := consume(chunk.data); err != nil {
			return err
		}
		offset += int64(len(chunk.data))
	}
	if offset != session.Size {
		return ErrIncomplete
	}
	return nil
}

func (s *MemoryStore) Complete(_ context.Context, tenantID, uploadID, fileAssetID, captureFileID, completionToken string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return Session{}, ErrNotFound
	}
	if session.Status == "completed" {
		return session, nil
	}
	if session.Status != "finalizing" || session.CompletionToken != completionToken || completionToken == "" {
		return Session{}, ErrConflict
	}
	now := time.Now().UTC()
	session.Status = "completed"
	session.FileAssetID = fileAssetID
	session.CaptureFileID = captureFileID
	session.ErrorCode = ""
	session.CompletedAt = &now
	s.sessions[uploadID] = session
	delete(s.chunks, uploadID)
	return session, nil
}

func (s *MemoryStore) Resume(_ context.Context, tenantID, uploadID, errorCode, completionToken string) error {
	return s.transition(tenantID, uploadID, "uploading", errorCode, completionToken)
}

func (s *MemoryStore) Fail(_ context.Context, tenantID, uploadID, errorCode, completionToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return ErrNotFound
	}
	if session.Status != "finalizing" || session.CompletionToken != completionToken || completionToken == "" {
		return ErrConflict
	}
	session.Status = "failed"
	session.ErrorCode = errorCode
	s.sessions[uploadID] = session
	delete(s.chunks, uploadID)
	return nil
}

func (s *MemoryStore) transition(tenantID, uploadID, status, errorCode, completionToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[uploadID]
	if !ok || session.TenantID != tenantID {
		return ErrNotFound
	}
	if session.Status != "finalizing" || session.CompletionToken != completionToken || completionToken == "" {
		return ErrConflict
	}
	session.Status = status
	session.ErrorCode = errorCode
	s.sessions[uploadID] = session
	return nil
}

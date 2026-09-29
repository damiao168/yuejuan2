package mathunderstanding

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type MemoryStore struct {
	mu        sync.RWMutex
	next      int64
	current   map[string]Artifact
	artifacts map[string]Artifact
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{next: 1, current: map[string]Artifact{}, artifacts: map[string]Artifact{}}
}

// CreateArtifact 在内存中保存版本历史。同一题块和输入哈希会返回原识别记录，不重新激活旧版本。
func (s *MemoryStore) CreateArtifact(_ context.Context, tenantID string, input CreateArtifactInput) (Artifact, error) {
	if tenantID == "" || ValidateCreateArtifact(input) != nil {
		return Artifact{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + input.AnswerSegmentID
	for _, existing := range s.artifacts {
		if existing.TenantID == tenantID && existing.AnswerSegmentID == input.AnswerSegmentID && existing.InputHash == input.InputHash && existing.Stage == "recognition" {
			return cloneArtifact(existing), nil
		}
	}
	version := int64(1)
	if previous, ok := s.current[key]; ok {
		version = previous.Version + 1
		previous.IsCurrent = false
		s.artifacts[tenantID+":"+previous.ID] = previous
	}
	item := Artifact{ID: fmt.Sprintf("math-artifact-%d", s.next), TenantID: tenantID, AnswerSegmentID: input.AnswerSegmentID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID, Version: version, InputHash: input.InputHash, EngineVersion: input.EngineVersion, IsCurrent: true, Stage: "recognition", QualitySummary: map[string]any{}, CreateArtifactInput: cloneInput(input), CreatedAt: time.Now().UTC()}
	s.next++
	s.current[key] = item
	s.artifacts[tenantID+":"+item.ID] = item
	return cloneArtifact(item), nil
}

func (s *MemoryStore) CreateDerivedArtifact(_ context.Context, tenantID, parentArtifactID string, correctionRevision int64, input CreateArtifactInput, qualitySummary map[string]any) (Artifact, error) {
	if tenantID == "" || parentArtifactID == "" || correctionRevision < 0 || ValidateCreateArtifact(input) != nil {
		return Artifact{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createDerivedArtifactLocked(tenantID, parentArtifactID, correctionRevision, input, qualitySummary)
}

func (s *MemoryStore) createDerivedArtifactLocked(tenantID, parentArtifactID string, correctionRevision int64, input CreateArtifactInput, qualitySummary map[string]any) (Artifact, error) {
	parent, ok := s.artifacts[tenantID+":"+parentArtifactID]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	for _, existing := range s.artifacts {
		if existing.TenantID == tenantID && existing.ParentArtifactID == parentArtifactID && existing.Stage == "verified" && existing.CorrectionRevision == correctionRevision {
			return cloneArtifact(existing), nil
		}
	}
	if !parent.IsCurrent || !sameArtifactBinding(input, parent) {
		return Artifact{}, ErrRevisionConflict
	}
	key := tenantID + ":" + input.AnswerSegmentID
	current, ok := s.current[key]
	if !ok || current.ID != parent.ID {
		return Artifact{}, ErrRevisionConflict
	}
	parent.IsCurrent = false
	s.artifacts[tenantID+":"+parent.ID] = parent
	item := Artifact{
		ID: fmt.Sprintf("math-artifact-%d", s.next), TenantID: tenantID,
		AnswerSegmentID: input.AnswerSegmentID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID,
		Version: parent.Version + 1, InputHash: input.InputHash, EngineVersion: input.EngineVersion,
		IsCurrent: true, Stage: "verified", ParentArtifactID: parent.ID, CorrectionRevision: correctionRevision,
		QualitySummary: cloneMap(qualitySummary), CreateArtifactInput: cloneInput(input), CreatedAt: time.Now().UTC(),
	}
	s.next++
	s.current[key] = item
	s.artifacts[tenantID+":"+item.ID] = item
	return cloneArtifact(item), nil
}

func (s *MemoryStore) GetArtifact(_ context.Context, tenantID string, artifactID string) (Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.artifacts[tenantID+":"+artifactID]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return cloneArtifact(item), nil
}

func (s *MemoryStore) GetLatestArtifact(_ context.Context, tenantID string, answerSegmentID string) (Artifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.current[tenantID+":"+answerSegmentID]
	if !ok {
		return Artifact{}, ErrNotFound
	}
	return cloneArtifact(item), nil
}

func cloneInput(input CreateArtifactInput) CreateArtifactInput {
	raw, _ := json.Marshal(input)
	var out CreateArtifactInput
	_ = json.Unmarshal(raw, &out)
	return out
}

// 对外返回深拷贝，避免调用方通过嵌套切片或 map 修改内存中的审计证据。
func cloneArtifact(item Artifact) Artifact {
	raw, _ := json.Marshal(item)
	var out Artifact
	_ = json.Unmarshal(raw, &out)
	// Artifact embeds the contract but shadows its immutable identity fields.
	// Restore those fields after the JSON deep copy so the base contract can be
	// projected or validated without losing its segment/hash binding.
	out.CreateArtifactInput.AnswerSegmentID = out.AnswerSegmentID
	out.CreateArtifactInput.ExamQuestionSnapshotID = out.ExamQuestionSnapshotID
	out.CreateArtifactInput.InputHash = out.InputHash
	out.CreateArtifactInput.EngineVersion = out.EngineVersion
	return out
}

func sameArtifactBinding(input CreateArtifactInput, artifact Artifact) bool {
	return input.AnswerSegmentID == artifact.AnswerSegmentID &&
		input.ExamQuestionSnapshotID == artifact.ExamQuestionSnapshotID &&
		input.SubjectCode == artifact.SubjectCode &&
		input.InputHash == artifact.InputHash &&
		input.EngineVersion == artifact.EngineVersion
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	raw, _ := json.Marshal(input)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

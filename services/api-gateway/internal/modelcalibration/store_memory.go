package modelcalibration

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu           sync.RWMutex
	next         int
	calibrations map[string]Calibration
	evidence     map[string][]CalibrationEvidence
	candidates   map[string]Candidate
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{calibrations: map[string]Calibration{}, evidence: map[string][]CalibrationEvidence{}, candidates: map[string]Candidate{}}
}

func (s *MemoryStore) Create(_ context.Context, tenantID, actorID string, input CreateInput) (Calibration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.calibrations {
		if item.TenantID == tenantID && item.Key == input.Key {
			return Calibration{}, ErrConflict
		}
	}
	item := Calibration{ID: s.id("calibration"), TenantID: tenantID, Key: input.Key, EvaluationRunID: input.EvaluationRunID,
		Axis: input.Axis, Method: input.Method, Status: StatusDraft, CreatedBy: actorID, CreatedAt: time.Now().UTC()}
	s.calibrations[itemKey(tenantID, item.ID)] = cloneCalibration(item)
	return cloneCalibration(item), nil
}

func (s *MemoryStore) Get(_ context.Context, tenantID, calibrationID string) (Calibration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.calibrations[itemKey(tenantID, calibrationID)]
	if !ok {
		return Calibration{}, ErrNotFound
	}
	return cloneCalibration(item), nil
}

func (s *MemoryStore) List(_ context.Context, tenantID string, axis Axis, limit int) ([]Calibration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []Calibration{}
	for _, item := range s.calibrations {
		if item.TenantID == tenantID && sameAxis(item.Axis, axis) {
			items = append(items, cloneCalibration(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *MemoryStore) AddEvidence(_ context.Context, tenantID, calibrationID string, input CalibrationEvidence) (CalibrationEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, calibrationID)
	calibration, ok := s.calibrations[key]
	if !ok {
		return CalibrationEvidence{}, ErrNotFound
	}
	if calibration.Status != StatusDraft {
		return CalibrationEvidence{}, ErrStateConflict
	}
	for _, item := range s.evidence[key] {
		if item.ResponseKey == input.ResponseKey {
			return CalibrationEvidence{}, ErrConflict
		}
	}
	input.ID, input.CalibrationID = s.id("calibration-evidence"), calibrationID
	s.evidence[key] = append(s.evidence[key], input)
	return input, nil
}

func (s *MemoryStore) ListEvidence(_ context.Context, tenantID, calibrationID string) ([]CalibrationEvidence, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := itemKey(tenantID, calibrationID)
	if _, ok := s.calibrations[key]; !ok {
		return nil, ErrNotFound
	}
	return append([]CalibrationEvidence(nil), s.evidence[key]...), nil
}

func (s *MemoryStore) Complete(_ context.Context, tenantID, calibrationID string, expectedN int, artifact Artifact, reference ArtifactReference, at time.Time) (Calibration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, calibrationID)
	item, ok := s.calibrations[key]
	if !ok {
		return Calibration{}, ErrNotFound
	}
	if item.Status != StatusDraft {
		return Calibration{}, ErrStateConflict
	}
	if len(s.evidence[key]) != expectedN || expectedN == 0 {
		return Calibration{}, ErrConflict
	}
	completed := at.UTC()
	item.Status, item.CalibrationN, item.Artifact, item.ArtifactURI, item.ArtifactSHA256, item.CompletedAt = StatusCompleted, expectedN, cloneArtifact(artifact), reference.URI, reference.SHA256, &completed
	s.calibrations[key] = cloneCalibration(item)
	return cloneCalibration(item), nil
}

func (s *MemoryStore) Approve(_ context.Context, tenantID, calibrationID, actorID string, at time.Time) (Calibration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, calibrationID)
	item, ok := s.calibrations[key]
	if !ok {
		return Calibration{}, ErrNotFound
	}
	if item.Status != StatusCompleted {
		return Calibration{}, ErrStateConflict
	}
	approved := at.UTC()
	item.Status, item.ApprovedAt, item.ApprovedBy = StatusApproved, &approved, actorID
	s.calibrations[key] = cloneCalibration(item)
	return cloneCalibration(item), nil
}

func (s *MemoryStore) Invalidate(_ context.Context, tenantID, calibrationID, actorID, reason string, at time.Time) (Calibration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, calibrationID)
	item, ok := s.calibrations[key]
	if !ok {
		return Calibration{}, ErrNotFound
	}
	if item.Status == StatusInvalidated || item.Status == StatusDraft {
		return Calibration{}, ErrStateConflict
	}
	invalidated := at.UTC()
	item.Status, item.InvalidatedAt, item.InvalidatedBy, item.InvalidationReason = StatusInvalidated, &invalidated, actorID, reason
	s.calibrations[key] = cloneCalibration(item)
	return cloneCalibration(item), nil
}

func (s *MemoryStore) FindApproved(_ context.Context, tenantID string, axis Axis) (Calibration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result *Calibration
	for _, item := range s.calibrations {
		if item.TenantID != tenantID || item.Status != StatusApproved || !sameAxis(item.Axis, axis) {
			continue
		}
		if result == nil || item.ApprovedAt.After(*result.ApprovedAt) {
			copy := cloneCalibration(item)
			result = &copy
		}
	}
	if result == nil {
		return Calibration{}, ErrNotFound
	}
	return *result, nil
}

func (s *MemoryStore) CreateOrGetCandidate(_ context.Context, tenantID string, input Candidate) (Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, input.CandidateKey)
	if existing, ok := s.candidates[key]; ok {
		return cloneCandidate(existing), nil
	}
	input.ID, input.TenantID = s.id("model-candidate"), tenantID
	s.candidates[key] = cloneCandidate(input)
	return cloneCandidate(input), nil
}

func (s *MemoryStore) id(prefix string) string { s.next++; return fmt.Sprintf("%s-%d", prefix, s.next) }

func itemKey(tenantID, id string) string { return tenantID + "\x00" + id }
func sameAxis(left, right Axis) bool     { return left == right }
func cloneArtifact(value Artifact) Artifact {
	value.Bins = append([]Bin(nil), value.Bins...)
	value.RiskCoverageCurve = append([]RiskCoveragePoint(nil), value.RiskCoverageCurve...)
	return value
}
func cloneCalibration(value Calibration) Calibration {
	value.Artifact = cloneArtifact(value.Artifact)
	return value
}
func cloneCandidate(value Candidate) Candidate {
	if value.CalibratedConfidence != nil {
		confidence := *value.CalibratedConfidence
		value.CalibratedConfidence = &confidence
	}
	if value.TargetRisk != nil {
		risk := *value.TargetRisk
		value.TargetRisk = &risk
	}
	return value
}

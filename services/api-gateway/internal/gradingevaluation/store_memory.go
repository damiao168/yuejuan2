package gradingevaluation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

type MemoryStore struct {
	mu                sync.RWMutex
	next              int
	runs              map[string]Run
	observations      map[string][]Observation
	panelObservations map[string][]PanelObservation
	slices            map[string][]SliceMetric
	difficulty        map[string][]ResponseDifficulty
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs: map[string]Run{}, observations: map[string][]Observation{}, panelObservations: map[string][]PanelObservation{}, slices: map[string][]SliceMetric{}, difficulty: map[string][]ResponseDifficulty{},
	}
}

func (s *MemoryStore) AddPanelObservation(_ context.Context, tenantID, runID string, input PanelObservation) (PanelObservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, runID)
	run, ok := s.runs[key]
	if !ok {
		return PanelObservation{}, ErrNotFound
	}
	if run.Status != RunDraft {
		return PanelObservation{}, ErrStateConflict
	}
	if !validPersistedPanelObservation(input) {
		return PanelObservation{}, ErrInvalidInput
	}
	for _, item := range s.panelObservations[key] {
		if item.ResponseKey == input.ResponseKey {
			return PanelObservation{}, ErrConflict
		}
	}
	input.ID, input.RunID, input.ObservedAt = s.id("panel-observation"), runID, time.Now().UTC()
	input = clonePanelObservation(input)
	s.panelObservations[key] = append(s.panelObservations[key], input)
	return clonePanelObservation(input), nil
}

func (s *MemoryStore) ListPanelObservations(_ context.Context, tenantID, runID string) ([]PanelObservation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := itemKey(tenantID, runID)
	if _, ok := s.runs[key]; !ok {
		return nil, ErrNotFound
	}
	items := s.panelObservations[key]
	result := make([]PanelObservation, len(items))
	for index, item := range items {
		result[index] = clonePanelObservation(item)
	}
	return result, nil
}

func (s *MemoryStore) CreateRun(_ context.Context, tenantID, actorID string, input CreateRunInput) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.runs {
		if item.TenantID == tenantID && item.Key == input.Key {
			return Run{}, ErrConflict
		}
	}
	now := time.Now().UTC()
	run := Run{ID: s.id("eval-run"), TenantID: tenantID, Key: input.Key, DisplayName: input.DisplayName, ModelReference: input.ModelReference,
		PromptVersion: input.PromptVersion, RubricVersion: input.RubricVersion, DatasetReference: input.DatasetReference, DatasetSHA256: input.DatasetSHA256,
		Status: RunDraft, CreatedBy: actorID, CreatedAt: now}
	s.runs[itemKey(tenantID, run.ID)] = run
	return run, nil
}

func (s *MemoryStore) GetRun(_ context.Context, tenantID, runID string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.runs[itemKey(tenantID, runID)]
	if !ok {
		return Run{}, ErrNotFound
	}
	item.ObservationCount = len(s.observations[itemKey(tenantID, runID)]) + len(s.panelObservations[itemKey(tenantID, runID)])
	return item, nil
}

func (s *MemoryStore) ListRuns(_ context.Context, tenantID string, filter RunFilter) ([]Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := []Run{}
	for _, item := range s.runs {
		if item.TenantID == tenantID {
			item.ObservationCount = len(s.observations[itemKey(tenantID, item.ID)]) + len(s.panelObservations[itemKey(tenantID, item.ID)])
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *MemoryStore) AddObservation(_ context.Context, tenantID, runID string, input AddObservationInput) (Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, runID)
	run, ok := s.runs[key]
	if !ok {
		return Observation{}, ErrNotFound
	}
	if run.Status != RunDraft {
		return Observation{}, ErrStateConflict
	}
	for _, item := range s.observations[key] {
		if item.ResponseKey == input.ResponseKey {
			return Observation{}, ErrConflict
		}
	}
	item := Observation{ID: s.id("eval-observation"), RunID: runID, ResponseKey: input.ResponseKey, ResponseFingerprint: input.ResponseFingerprint,
		ReferenceKind: input.ReferenceKind, Subject: input.Subject, Archetype: input.Archetype, OCRQuality: input.OCRQuality,
		AnswerLength: input.AnswerLength, RubricComplexity: input.RubricComplexity, ReferenceScore: input.ReferenceScore, ModelScore: input.ModelScore,
		MaxScore: input.MaxScore, ReferenceScoreBand: scoreBand(input.ReferenceScore, input.MaxScore), PageMatchCorrect: input.PageMatchCorrect,
		CropIoU: input.CropIoU, TranscriptionCER: input.TranscriptionCER, FormulaExact: input.FormulaExact, RubricAgreement: input.RubricAgreement,
		ErrorSource: input.ErrorSource, NeedsHumanReview: input.NeedsHumanReview, ReferenceReviewers: input.ReferenceReviewers,
		ReferenceAdjudicated: input.ReferenceAdjudicated, ObservedAt: time.Now().UTC()}
	s.observations[key] = append(s.observations[key], item)
	return item, nil
}

func (s *MemoryStore) ListObservations(_ context.Context, tenantID, runID string) ([]Observation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.runs[itemKey(tenantID, runID)]; !ok {
		return nil, ErrNotFound
	}
	return append([]Observation(nil), s.observations[itemKey(tenantID, runID)]...), nil
}

func (s *MemoryStore) ReplaceComputed(_ context.Context, tenantID, runID string, expectedCount int, slices []SliceMetric, difficulty []ResponseDifficulty, at time.Time) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, runID)
	run, ok := s.runs[key]
	if !ok {
		return Run{}, ErrNotFound
	}
	if run.Status != RunDraft {
		return Run{}, ErrStateConflict
	}
	actualCount := len(s.observations[key]) + len(s.panelObservations[key])
	if actualCount != expectedCount {
		return Run{}, ErrConflict
	}
	completedAt := at.UTC()
	run.Status, run.CompletedAt, run.ObservationCount = RunCompleted, &completedAt, actualCount
	s.runs[key], s.slices[key], s.difficulty[key] = run, append([]SliceMetric(nil), slices...), append([]ResponseDifficulty(nil), difficulty...)
	return run, nil
}

func (s *MemoryStore) ListSliceMetrics(_ context.Context, tenantID, runID string) ([]SliceMetric, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.runs[itemKey(tenantID, runID)]; !ok {
		return nil, ErrNotFound
	}
	return append([]SliceMetric(nil), s.slices[itemKey(tenantID, runID)]...), nil
}

func (s *MemoryStore) ListResponseDifficulty(_ context.Context, tenantID, runID string) ([]ResponseDifficulty, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.runs[itemKey(tenantID, runID)]; !ok {
		return nil, ErrNotFound
	}
	return append([]ResponseDifficulty(nil), s.difficulty[itemKey(tenantID, runID)]...), nil
}

func (s *MemoryStore) InvalidateRun(_ context.Context, tenantID, runID, reason string, at time.Time) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := itemKey(tenantID, runID)
	run, ok := s.runs[key]
	if !ok {
		return Run{}, ErrNotFound
	}
	if run.Status == RunInvalid {
		return Run{}, ErrStateConflict
	}
	invalidated := at.UTC()
	run.Status, run.InvalidatedAt, run.InvalidationReason = RunInvalid, &invalidated, reason
	run.ObservationCount = len(s.observations[key]) + len(s.panelObservations[key])
	s.runs[key] = run
	return run, nil
}

func (s *MemoryStore) id(prefix string) string { s.next++; return fmt.Sprintf("%s-%d", prefix, s.next) }
func itemKey(tenantID, id string) string       { return tenantID + "\x00" + id }

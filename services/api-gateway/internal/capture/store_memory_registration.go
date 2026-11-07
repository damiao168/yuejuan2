package capture

import (
	"context"
	"strings"
)

func (s *MemoryStore) ConfirmRegistration(_ context.Context, tenantID, runID, actorID, reason string) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	if x.MatchStatus != "needs_review" || strings.TrimSpace(reason) == "" {
		return RegistrationRun{}, ErrInvalidTransition
	}
	x.MatchStatus = "matched"
	s.registrations[runID] = x
	return x, nil
}

func (s *MemoryStore) PrepareRegistrationRetry(_ context.Context, tenantID, runID string) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	x.ProcessingStatus = "processing"
	x.ErrorCode = ""
	s.registrations[runID] = x
	return x, nil
}

func (s *MemoryStore) QueueSubmissionPages(_ context.Context, tenantID, submissionID, actorID string) ([]RegistrationRun, error) {
	return nil, ErrInvalidTransition
}

func (s *MemoryStore) GetRegistrationRun(_ context.Context, tenantID, runID string) (RegistrationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) ListRegistrationRuns(_ context.Context, tenantID, submissionPageID string) ([]RegistrationRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []RegistrationRun{}
	for _, item := range s.registrations {
		if item.SubmissionPageID == submissionPageID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetTemplateMatchRun(_ context.Context, tenantID, runID string) (TemplateMatchRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, ErrNotFound
	}
	return item, nil
}

func (s *MemoryStore) ApplyTemplateMatchResult(_ context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, nil, ErrNotFound
	}
	item.ProcessingStatus = "completed"
	item.Decision = input.Decision
	item.SelectedTemplateID = input.SelectedTemplateID
	item.SelectedTemplateContentHash = input.SelectedTemplateContentHash
	item.Score = input.Score
	item.Margin = input.Margin
	item.Candidates = input.Candidates
	s.templateMatches[runID] = item
	return item, []RegistrationRun{}, nil
}

func (s *MemoryStore) ApplyTemplateMatchFailure(_ context.Context, tenantID, runID, errorCode string, _ map[string]any, retryable bool) (TemplateMatchRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.templateMatches[runID]
	if !ok {
		return TemplateMatchRun{}, ErrNotFound
	}
	item.ProcessingStatus = "terminal_error"
	if retryable {
		item.ProcessingStatus = "retryable_error"
	}
	item.ErrorCode = errorCode
	s.templateMatches[runID] = item
	return item, nil
}

func (s *MemoryStore) ApplyRegistrationResult(_ context.Context, tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyRegistrationResultLocked(tenantID, runID, input)
}

func (s *MemoryStore) applyRegistrationResultLocked(tenantID, runID string, input RegistrationResultInput) (RegistrationRun, error) {
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	item.ProcessingStatus = "completed"
	item.MatchStatus = "matched"
	item.Confidence = input.Confidence
	item.Method = input.Method
	item.GuardReport = input.GuardReport
	item.RegisteredFileAssetID = input.RegisteredFileAssetID
	s.registrations[runID] = item
	return item, nil
}

func (s *MemoryStore) ApplyRegistrationFailure(_ context.Context, tenantID, runID, errorCode string, errorDetail map[string]any, retryable bool) (RegistrationRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.registrations[runID]
	if !ok {
		return RegistrationRun{}, ErrNotFound
	}
	item.ProcessingStatus = "terminal_error"
	if retryable {
		item.ProcessingStatus = "retryable_error"
	}
	item.ErrorCode = errorCode
	s.registrations[runID] = item
	return item, nil
}

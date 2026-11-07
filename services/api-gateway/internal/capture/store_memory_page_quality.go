package capture

import (
	"context"
	"sort"
)

func (s *MemoryStore) ListPages(_ context.Context, tenantID, batchID string) ([]Page, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Page{}
	for _, x := range s.pages {
		if x.TenantID == tenantID && x.CaptureBatchID == batchID {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SequenceNo < out[j].SequenceNo })
	return out, nil
}

func (s *MemoryStore) GetPageBySubmissionPageID(_ context.Context, tenantID, submissionPageID string) (Page, error) {
	if submissionPageID == "" {
		return Page{}, ErrInvalidInput
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionPageID == submissionPageID {
			return page, nil
		}
	}
	return Page{}, ErrNotFound
}

func (s *MemoryStore) ApplyQualityOutcome(_ context.Context, tenantID, submissionPageID, qualityStatus string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "quality_rejected"
	if qualityStatus == "passed" {
		status = "normalized"
	} else if qualityStatus == "review" {
		status = "needs_review"
	} else if qualityStatus != "failed" {
		return ErrInvalidInput
	}
	for id, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionPageID == submissionPageID {
			page.Status = status
			if page.PageIdentity == nil {
				page.PageIdentity = map[string]any{}
			}
			page.PageIdentity["quality_status"] = qualityStatus
			page.Revision++
			s.pages[id] = page
			return nil
		}
	}
	return ErrNotFound
}

func (s *MemoryStore) UpdatePage(_ context.Context, tenantID, pageID, actorID string, input UpdatePageInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	batch := s.batches[x.CaptureBatchID]
	if batch.Status == "completed" || batch.Status == "cancelled" {
		return Page{}, ErrInvalidTransition
	}
	if input.Revision != x.Revision {
		return Page{}, ErrConflict
	}
	if input.RotationDegrees != nil {
		if !validRotation(*input.RotationDegrees) {
			return Page{}, ErrInvalidInput
		}
		x.RotationDegrees = *input.RotationDegrees
		x.Status = "needs_review"
	}
	if input.SequenceNo != nil {
		if *input.SequenceNo <= 0 {
			return Page{}, ErrInvalidInput
		}
		x.SequenceNo = *input.SequenceNo
	}
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}

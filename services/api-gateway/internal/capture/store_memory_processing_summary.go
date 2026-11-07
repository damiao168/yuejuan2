package capture

import (
	"context"
	"sort"
)

func (s *MemoryStore) GetProcessingSummary(_ context.Context, tenantID, submissionID string) (ProcessingSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.processingSummaryLocked(tenantID, submissionID)
}

func (s *MemoryStore) ListProcessingSummaries(_ context.Context, tenantID, batchID string) ([]ProcessingSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return nil, ErrNotFound
	}
	submissionIDs := map[string]struct{}{}
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.CaptureBatchID == batchID && page.SubmissionID != "" && page.Status != "deleted" {
			submissionIDs[page.SubmissionID] = struct{}{}
		}
	}
	ids := make([]string, 0, len(submissionIDs))
	for submissionID := range submissionIDs {
		ids = append(ids, submissionID)
	}
	sort.Strings(ids)
	out := make([]ProcessingSummary, 0, len(ids))
	for _, submissionID := range ids {
		summary, err := s.processingSummaryLocked(tenantID, submissionID)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

// 内存实现按页面和最近配准运行生成展示用阻塞摘要；它只模拟状态，不替代数据库聚合。
func (s *MemoryStore) processingSummaryLocked(tenantID, submissionID string) (ProcessingSummary, error) {
	out := ProcessingSummary{SubmissionID: submissionID, Blockers: []ProcessingBlocker{}}
	pages := make([]Page, 0)
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionID == submissionID && page.Status != "deleted" {
			pages = append(pages, page)
		}
	}
	if len(pages) == 0 {
		return out, ErrNotFound
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].SequenceNo < pages[j].SequenceNo })
	for _, page := range pages {
		out.TotalPages++
		if page.Status == "ready" {
			out.ReadyPages++
			continue
		}
		blocker := ProcessingBlocker{PageID: page.ID, PageNo: page.AssignedPageNo, Stage: "processing", Code: "pending", Action: "wait"}
		latest := s.latestPageRegistrationLocked(page.ID)
		blocker.RegistrationRunID = latest.ID
		switch {
		case page.Status == "quality_rejected" || page.Status == "needs_review":
			blocker.Stage = "quality"
			blocker.Code = "quality_review"
			blocker.Action = "review_quality"
			out.BlockedPages++
		case latest.ProcessingStatus == "terminal_error":
			blocker.Stage = "registration"
			blocker.Code = "registration_failed"
			blocker.Action = "retry_registration"
			out.BlockedPages++
		case latest.MatchStatus == "needs_review":
			blocker.Stage = "registration"
			blocker.Code = "low_confidence"
			blocker.Action = "confirm_registration"
			out.BlockedPages++
		default:
			out.PendingPages++
		}
		out.Blockers = append(out.Blockers, blocker)
	}
	out.CanComplete = out.ReadyPages == out.TotalPages
	return out, nil
}

func (s *MemoryStore) latestPageRegistrationLocked(pageID string) RegistrationRun {
	var latest RegistrationRun
	for _, run := range s.registrations {
		if run.CapturePageID == pageID && (latest.ID == "" || run.CreatedAt.After(latest.CreatedAt)) {
			latest = run
		}
	}
	return latest
}

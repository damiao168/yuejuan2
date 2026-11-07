package capture

import (
	"context"
	"strings"
)

func (s *MemoryStore) GetMatchingQueue(_ context.Context, tenantID, batchID string) (MatchingQueue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	batch, ok := s.batches[batchID]
	if !ok || batch.TenantID != tenantID {
		return MatchingQueue{}, ErrNotFound
	}
	queue := MatchingQueue{BatchID: batchID, ExamID: batch.ExamID, Candidates: []StudentCandidate{}, Submissions: []MatchingSubmission{}}
	for _, identity := range s.identities {
		pages := []Page{}
		for _, page := range s.pages {
			if page.TenantID == tenantID && page.CaptureBatchID == batchID && page.SubmissionID == identity.ID {
				pages = append(pages, page)
			}
		}
		if len(pages) > 0 {
			identity.Pages = pages
			queue.Submissions = append(queue.Submissions, identity)
		}
	}
	return queue, nil
}

func (s *MemoryStore) ConfirmStudentMatch(_ context.Context, tenantID, submissionID, actorID string, input ConfirmStudentMatchInput) (MatchingSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Revision <= 0 || input.StudentID == "" {
		return MatchingSubmission{}, ErrInvalidInput
	}
	// 内存身份记录没有独立租户字段，必须先由已采集页面确认答卷归属，不能凭客户端 ID 新建身份。
	if !s.ownsSubmissionLocked(tenantID, submissionID) {
		return MatchingSubmission{}, ErrNotFound
	}
	x, ok := s.identities[submissionID]
	if !ok {
		x = MatchingSubmission{ID: submissionID, IdentityStatus: "unassigned", IdentityRevision: 1, IdentityEvidence: map[string]any{}}
	}
	if x.IdentityRevision != input.Revision {
		return MatchingSubmission{}, ErrConflict
	}
	x.StudentID = input.StudentID
	x.IdentityStatus = "matched"
	x.IdentityRevision++
	x.IdentityEvidence = map[string]any{"method": "manual_confirmation", "actor_id": actorID, "reason": input.Reason}
	s.identities[submissionID] = x
	return x, nil
}

func (s *MemoryStore) MarkStudentUnknown(_ context.Context, tenantID, submissionID, actorID string, input MarkStudentUnknownInput) (MatchingSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return MatchingSubmission{}, ErrInvalidInput
	}
	// 内存身份记录没有独立租户字段，必须先由已采集页面确认答卷归属，不能凭客户端 ID 新建身份。
	if !s.ownsSubmissionLocked(tenantID, submissionID) {
		return MatchingSubmission{}, ErrNotFound
	}
	x, ok := s.identities[submissionID]
	if !ok {
		x = MatchingSubmission{ID: submissionID, IdentityStatus: "unassigned", IdentityRevision: 1}
	}
	if x.IdentityRevision != input.Revision {
		return MatchingSubmission{}, ErrConflict
	}
	x.StudentID = ""
	x.CandidateNo = ""
	x.IdentityStatus = "unknown"
	x.IdentityRevision++
	x.IdentityEvidence = map[string]any{"method": "manual_unknown", "actor_id": actorID, "reason": input.Reason}
	s.identities[submissionID] = x
	return x, nil
}

// 调用方持有采集锁；页面在解码时绑定答卷和租户，删除页面仍保留其答卷归属。
func (s *MemoryStore) ownsSubmissionLocked(tenantID, submissionID string) bool {
	for _, page := range s.pages {
		if page.TenantID == tenantID && page.SubmissionID == submissionID {
			return true
		}
	}
	return false
}

func (s *MemoryStore) ConfirmPageMatch(_ context.Context, tenantID, pageID, actorID string, input ConfirmPageMatchInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision <= 0 || input.PageNo <= 0 {
		return Page{}, ErrInvalidInput
	}
	if x.Revision != input.Revision {
		return Page{}, ErrConflict
	}
	x.AssignedPageNo = input.PageNo
	x.Revision++
	x.ManualOverride = map[string]any{"page_no": input.PageNo, "actor_id": actorID, "reason": input.Reason}
	s.pages[pageID] = x
	return x, nil
}

func (s *MemoryStore) DeletePage(_ context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision != x.Revision || strings.TrimSpace(input.Reason) == "" {
		return Page{}, ErrConflict
	}
	x.Status = "deleted"
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}

func (s *MemoryStore) RestorePage(_ context.Context, tenantID, pageID, actorID string, input PageLifecycleInput) (Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	x, ok := s.pages[pageID]
	if !ok || x.TenantID != tenantID {
		return Page{}, ErrNotFound
	}
	if input.Revision != x.Revision || x.Status != "deleted" {
		return Page{}, ErrConflict
	}
	x.Status = "needs_review"
	x.Revision++
	s.pages[pageID] = x
	return x, nil
}

func (s *MemoryStore) SplitSubmission(context.Context, string, string, string, SplitSubmissionInput) (MatchingQueue, error) {
	return MatchingQueue{}, ErrInvalidTransition
}

func (s *MemoryStore) MergeSubmissions(context.Context, string, string, string, MergeSubmissionsInput) (MatchingQueue, error) {
	return MatchingQueue{}, ErrInvalidTransition
}

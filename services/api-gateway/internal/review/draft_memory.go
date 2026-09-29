package review

import (
	"context"
	"fmt"
	"time"
)

var _ DraftStore = (*MemoryStore)(nil)

func (s *MemoryStore) GetDraft(_ context.Context, tenantID, taskID, reviewerID string) (ReviewDraft, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	draft, ok := s.drafts[draftKey(tenantID, taskID, reviewerID)]
	if !ok {
		return ReviewDraft{}, ErrNotFound
	}
	return cloneReviewDraft(draft), nil
}

func (s *MemoryStore) SaveDraft(_ context.Context, tenantID, taskID, reviewerID string, input SaveDraftInput) (ReviewDraft, error) {
	if input.ExpectedRevision < 0 || input.Score != nil && *input.Score < 0 {
		return ReviewDraft{}, ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[key(tenantID, taskID)]
	if !ok {
		return ReviewDraft{}, ErrNotFound
	}
	if task.AssignedTo != reviewerID {
		return ReviewDraft{}, ErrForbidden
	}
	if !isClaimableAssignedStatus(task.Status) {
		return ReviewDraft{}, ErrInvalidTransition
	}
	draftID := draftKey(tenantID, taskID, reviewerID)
	current, exists := s.drafts[draftID]
	// 首次保存传 0；已有草稿必须带上读到的草稿版本，避免多个窗口互相覆盖。
	if !exists && input.ExpectedRevision != 0 || exists && current.Revision != input.ExpectedRevision {
		return ReviewDraft{}, ErrRevisionConflict
	}
	now := time.Now().UTC()
	revision := 1
	id := fmt.Sprintf("review-draft-%d", s.next)
	if exists {
		revision = current.Revision + 1
		id = current.ID
	} else {
		s.next++
	}
	draft := ReviewDraft{
		ID: id, ReviewTaskID: taskID, ReviewerID: reviewerID, Score: cloneScore(input.Score),
		RubricSelections: append([]RubricSelection(nil), input.RubricSelections...),
		Comments:         input.Comments, PrivateNote: input.PrivateNote, StudentFeedback: input.StudentFeedback,
		ViewerState: cloneMap(input.ViewerState), Revision: revision, ClientUpdatedAt: input.ClientUpdatedAt, UpdatedAt: now,
	}
	s.drafts[draftID] = draft
	return cloneReviewDraft(draft), nil
}

func draftKey(tenantID, taskID, reviewerID string) string {
	return key(tenantID, taskID+"\x00"+reviewerID)
}

func cloneReviewDraft(draft ReviewDraft) ReviewDraft {
	draft.Score = cloneScore(draft.Score)
	draft.RubricSelections = append([]RubricSelection(nil), draft.RubricSelections...)
	draft.ViewerState = cloneMap(draft.ViewerState)
	return draft
}

func cloneScore(score *float64) *float64 {
	if score == nil {
		return nil
	}
	value := *score
	return &value
}

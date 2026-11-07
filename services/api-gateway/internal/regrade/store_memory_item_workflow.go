package regrade

import (
	"context"
)

func (s *MemoryStore) Claim(_ context.Context, tenantID, itemID, actorID string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	job := s.jobs[jobID]
	item := s.items[jobID][index]
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.AssignedTo != "" && item.AssignedTo != actorID {
		return Item{}, ErrAssignmentForbidden
	}
	if item.Status == ItemClaimed && item.ClaimedBy == actorID && item.AssignedTo != "" {
		return cloneItem(item), nil
	}
	if item.Status != ItemPending && !(item.Status == ItemClaimed && item.ClaimedBy == actorID) {
		return Item{}, ErrStateConflict
	}
	// 未分配条目允许首次领取；同锁固定分配人与领取人，后续队列、材料和图片使用同一身份。
	// 旧版本已领取但未保存分配人的条目也由本人重领补齐，其他阅卷人不能接管。
	if item.AssignedTo == "" {
		item.AssignedTo = actorID
	}
	item.Status, item.ClaimedBy, item.Revision, item.UpdatedAt = ItemClaimed, actorID, item.Revision+1, s.now().UTC()
	s.items[jobID][index] = item
	s.addEventLocked(jobID, item.ID, "item_claimed", actorID, map[string]any{})
	return cloneItem(item), nil
}

func (s *MemoryStore) RecordCandidate(_ context.Context, tenantID, itemID, actorID string, input CandidateInput) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	job := s.jobs[jobID]
	item := s.items[jobID][index]
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.AssignedTo != "" && item.AssignedTo != actorID {
		return Item{}, ErrAssignmentForbidden
	}
	if item.ClaimedBy != actorID || item.Status != ItemClaimed {
		return Item{}, ErrAssignmentForbidden
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, ErrRevisionConflict
	}
	if input.Score > item.MaxScore {
		return Item{}, ErrInvalidInput
	}
	candidate := input.Score
	// 候选结果统一进入人工复核，RequireManualReview 只记入事件，不会跳过这一步。
	// delta 表示已复核分差，须到 ReviewAccept 才产生；不能把候选差值提前存成正式差值。
	item.CandidateScore, item.CandidateGradeID, item.Delta = &candidate, input.CandidateGradeID, nil
	item.CandidateRubricSelections, item.CandidateComment = append([]RubricSelection(nil), input.RubricSelections...), input.Comment
	item.Status, item.Revision, item.UpdatedAt = ItemAwaitingReview, item.Revision+1, s.now().UTC()
	s.items[jobID][index] = item
	if job.Status == StatusRunning {
		job.Status, job.UpdatedAt = StatusDiffReview, s.now().UTC()
		s.jobs[jobID] = job
	}
	s.addEventLocked(jobID, item.ID, "candidate_recorded", actorID, map[string]any{"requires_manual_review": input.RequireManualReview})
	return cloneItem(item), nil
}

func (s *MemoryStore) Review(_ context.Context, tenantID, itemID, actorID string, input ReviewInput) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return Item{}, err
	}
	job := s.jobs[jobID]
	item := s.items[jobID][index]
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Item{}, ErrStateConflict
	}
	if item.Revision != input.ExpectedRevision {
		return Item{}, ErrRevisionConflict
	}
	if err := reviewableItem(item, input); err != nil {
		return Item{}, err
	}
	payload := reviewEventPayload(item, input)
	if input.Decision == ReviewAccept {
		score := item.CandidateScore
		if input.ReviewedScore != nil {
			score = input.ReviewedScore
		}
		if score == nil || *score > item.MaxScore {
			return Item{}, ErrInvalidInput
		}
		resolved, delta := *score, *score-item.OldScore
		item.ReviewedScore, item.Delta = &resolved, &delta
		item.ReviewedGradeID = input.ReviewedGradeID
		item.Status = ItemResolved
	} else if input.Decision == ReviewReject {
		item.Status = ItemResolved
		item.ReviewedScore, item.Delta = nil, nil
	} else {
		item.Status = ItemException
	}
	item.ReviewedBy, item.ReviewNote, item.Revision, item.UpdatedAt = actorID, input.Note, item.Revision+1, s.now().UTC()
	s.items[jobID][index] = item
	s.addEventLocked(jobID, item.ID, "item_reviewed", actorID, payload)
	return cloneItem(item), nil
}

package review

import (
	"context"
	"sort"
	"time"
)

var _ WorkbenchStore = (*MemoryStore)(nil)

const memoryTaskClaimTTL = 30 * time.Minute

func (s *MemoryStore) ClaimNextTask(_ context.Context, tenantID, reviewerID string, input NextTaskInput, options ClaimTaskOptions) (ReviewTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	assigned := make([]ReviewTask, 0)
	unassigned := make([]ReviewTask, 0)
	for _, task := range s.tasks {
		if task.TenantID != tenantID || !matchesNextTaskFilter(task, input) {
			continue
		}
		if task.AssignedTo == reviewerID && isClaimableAssignedStatus(task.Status) {
			assigned = append(assigned, task)
			continue
		}
		if options.AllowUnassigned && task.AssignedTo == "" && task.Status == "pending" {
			unassigned = append(unassigned, task)
		}
	}

	// 本人已分配的任务优先；内存实现的候补池只含未分配任务，不接管别人的过期租约。
	candidates := assigned
	if len(candidates) == 0 {
		candidates = unassigned
	}
	if len(candidates) == 0 {
		return ReviewTask{}, ErrNotFound
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Priority == candidates[j].Priority {
			if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
				return candidates[i].ID < candidates[j].ID
			}
			return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
		}
		return candidates[i].Priority > candidates[j].Priority
	})

	task := candidates[0]
	if task.AssignedTo != reviewerID || task.Status == "pending" {
		task.Status = "assigned"
	}
	task.AssignedTo = reviewerID
	task.Revision++
	now := time.Now().UTC()
	task.UpdatedAt = now
	s.tasks[key(tenantID, task.ID)] = task
	s.claims[key(tenantID, task.ID)] = memoryTaskClaim{OwnerID: reviewerID, ClaimedAt: now, ExpiresAt: now.Add(memoryTaskClaimTTL)}
	return cloneTask(task), nil
}

func (s *MemoryStore) GetWorkspace(_ context.Context, tenantID, taskID string) (Workspace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[key(tenantID, taskID)]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	contextValue, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	if !ok {
		return Workspace{}, ErrNotFound
	}
	contextValue.AISuggestion = cloneMap(contextValue.AISuggestion)
	return Workspace{
		Task:            cloneTask(task),
		Context:         contextValue,
		SegmentImageURL: "/api/v1/review-tasks/" + task.ID + "/segment-image",
		SegmentStatus:   "ready",
	}, nil
}

func (s *MemoryStore) RenewTaskClaim(_ context.Context, tenantID, taskID, reviewerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[key(tenantID, taskID)]
	if !ok || task.AssignedTo != reviewerID || !isClaimableAssignedStatus(task.Status) {
		return ErrForbidden
	}
	now := time.Now().UTC()
	task.UpdatedAt = now
	s.tasks[key(tenantID, taskID)] = task
	claim, claimed := s.claims[key(tenantID, taskID)]
	if !claimed || claim.OwnerID != reviewerID {
		claim = memoryTaskClaim{OwnerID: reviewerID, ClaimedAt: now}
	}
	claim.ExpiresAt = now.Add(memoryTaskClaimTTL)
	s.claims[key(tenantID, taskID)] = claim
	return nil
}

// ReleaseTaskClaim ends the browser session while retaining the manager's
// durable assignment and the task's business status.
func (s *MemoryStore) ReleaseTaskClaim(_ context.Context, tenantID, taskID, reviewerID string) (ReviewTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	task, ok := s.tasks[key(tenantID, taskID)]
	if !ok || task.AssignedTo != reviewerID || !isClaimableAssignedStatus(task.Status) {
		return ReviewTask{}, ErrForbidden
	}
	task.UpdatedAt = time.Now().UTC()
	task.Revision++
	s.tasks[key(tenantID, taskID)] = task
	delete(s.claims, key(tenantID, taskID))
	return cloneTask(task), nil
}

func matchesNextTaskFilter(task ReviewTask, input NextTaskInput) bool {
	return (input.ExamID == "" || task.ExamID == input.ExamID) &&
		(input.QuestionID == "" || task.QuestionID == input.QuestionID)
}

func isClaimableAssignedStatus(status string) bool {
	return status == "assigned" || status == "in_progress" || status == "returned"
}

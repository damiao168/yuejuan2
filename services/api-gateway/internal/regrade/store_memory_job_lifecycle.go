package regrade

import (
	"context"
)

func (s *MemoryStore) Approve(_ context.Context, tenantID, jobID, actorID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusApproved {
		return cloneJob(job), nil
	}
	if job.Status != StatusAwaitingApproval {
		return Job{}, ErrStateConflict
	}
	now := s.now().UTC()
	job.Status, job.ApprovedBy, job.ApprovedAt, job.UpdatedAt = StatusApproved, actorID, &now, now
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "approved", actorID, map[string]any{})
	return cloneJob(job), nil
}

func (s *MemoryStore) Start(_ context.Context, tenantID, jobID, actorID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusRunning {
		return cloneJob(job), nil
	}
	if job.Status != StatusApproved {
		return Job{}, ErrStateConflict
	}
	job.Status, job.UpdatedAt = StatusRunning, s.now().UTC()
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "started", actorID, map[string]any{})
	return cloneJob(job), nil
}

func (s *MemoryStore) Pause(_ context.Context, tenantID, jobID, actorID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusPaused {
		return cloneJob(job), nil
	}
	if job.Status != StatusRunning && job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	job.Status, job.UpdatedAt = StatusPaused, s.now().UTC()
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "paused", actorID, map[string]any{})
	return cloneJob(job), nil
}

func (s *MemoryStore) Resume(_ context.Context, tenantID, jobID, actorID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusRunning || job.Status == StatusDiffReview {
		return cloneJob(job), nil
	}
	if job.Status != StatusPaused {
		return Job{}, ErrStateConflict
	}
	job.Status, job.UpdatedAt = StatusRunning, s.now().UTC()
	// 暂停不丢弃候选和复核事实；出现过候选的作业恢复到复核阶段，全部已解决时仍可收尾。
	for _, item := range s.items[jobID] {
		if item.CandidateScore != nil || item.Status == ItemCandidateReady || item.Status == ItemAwaitingReview || item.Status == ItemResolved || item.Status == ItemException {
			job.Status = StatusDiffReview
			break
		}
	}
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "resumed", actorID, map[string]any{})
	return cloneJob(job), nil
}

func (s *MemoryStore) Finalize(_ context.Context, tenantID, jobID, actorID string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Job{}, err
	}
	if job.Status == StatusReadyForRelease {
		return cloneJob(job), nil
	}
	if job.Status != StatusDiffReview {
		return Job{}, ErrStateConflict
	}
	for _, item := range s.items[jobID] {
		if item.Status != ItemResolved {
			return Job{}, ErrStateConflict
		}
	}
	now := s.now().UTC()
	job.Status, job.FinalizedBy, job.FinalizedAt, job.UpdatedAt = StatusReadyForRelease, actorID, &now, now
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "finalized", actorID, map[string]any{"next": "create_new_score_release"})
	return cloneJob(job), nil
}

package regrade

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/examlock"
	"fmt"
	"sort"
	"sync"
	"time"
)

// MemoryStore 对应生产环境的不可变边界：来源发布事实独立预置，重评候选只存在于此存储的条目中。
// 它用于聚焦的流程和 handler 测试。
type MemoryStore struct {
	mu          sync.Mutex
	now         func() time.Time
	sequence    int
	sources     map[string]memoryRelease
	jobs        map[string]Job
	items       map[string][]Item
	events      map[string][]Event
	idempotency map[string]string
	evidence    map[string]MemoryGraderEvidence
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{now: func() time.Time { return time.Now().UTC() }, sources: map[string]memoryRelease{}, jobs: map[string]Job{}, items: map[string][]Item{}, events: map[string][]Event{}, idempotency: map[string]string{}, evidence: map[string]MemoryGraderEvidence{}}
}
func (s *MemoryStore) SetNow(now func() time.Time) { s.mu.Lock(); defer s.mu.Unlock(); s.now = now }

func (s *MemoryStore) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput, sources []SourceItem) (Job, []Item, error) {
	_ = ctx
	unlock := examlock.Memory(tenantID, examID)
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantID + ":" + examID + ":" + input.IdempotencyKey
	// 同一租户、考试下复用幂等键会返回原任务，不会按本次参数重建任务。
	if id := s.idempotency[key]; id != "" {
		return cloneJob(s.jobs[id]), cloneItems(s.items[id]), nil
	}
	// 再次检查冻结来源；调用方绝不能将过时预览变成包含不同当前最终事实的作业。
	fresh, err := s.sourceItemsLocked(tenantID, examID, questionID, input.SourceReleaseID, input.Selector)
	if err != nil {
		return Job{}, nil, err
	}
	if len(fresh) == 0 {
		return Job{}, nil, ErrNoAffectedItems
	}
	if len(sources) > 0 {
		fresh = sources
	} // 服务已取得来源快照；这里全部是发布事实。
	now := s.now().UTC()
	job := Job{ID: s.nextIDLocked("regrade-job"), TenantID: tenantID, ExamID: examID, QuestionID: questionID, SourceReleaseID: input.SourceReleaseID,
		ReasonCode: input.ReasonCode, ReasonText: input.ReasonText, Strategy: input.Strategy, Selector: cloneSelector(input.Selector), NewRubricSnapshotID: input.NewRubricSnapshotID,
		NewPolicyVersion: input.NewPolicyVersion, SeverityDelta: *input.SeverityDelta, IdempotencyKey: input.IdempotencyKey, Status: StatusAwaitingApproval,
		AffectedCount: len(fresh), CreatedBy: actorID, CreatedAt: now, UpdatedAt: now}
	items := make([]Item, 0, len(fresh))
	for _, source := range fresh {
		items = append(items, Item{ID: s.nextIDLocked("regrade-item"), JobID: job.ID, SubmissionID: source.SubmissionID, OldFinalGradeID: source.OldFinalGradeID, OldScore: source.OldScore, MaxScore: source.MaxScore, Status: ItemPending, AssignedTo: input.AssigneeID, Revision: 1, CreatedAt: now, UpdatedAt: now})
	}
	s.jobs[job.ID], s.items[job.ID], s.idempotency[key] = job, items, job.ID
	s.addEventLocked(job.ID, "", "created", actorID, map[string]any{"affected_count": len(items), "source_release_id": input.SourceReleaseID, "strategy": input.Strategy})
	return cloneJob(job), cloneItems(items), nil
}
func (s *MemoryStore) Get(_ context.Context, tenantID, jobID string) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.summaryLocked(tenantID, jobID)
}
func (s *MemoryStore) List(ctx context.Context, tenantID, examID, questionID string, accesses ...auth.ExamCollectionAccess) ([]Job, error) {
	access := jobCollectionAccess(accesses)
	if err := access.Validate(tenantID); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []Job{}
	for _, job := range s.jobs {
		if job.TenantID == tenantID && (examID == "" || job.ExamID == examID) && (questionID == "" || job.QuestionID == questionID) {
			allowed, err := access.Allows(ctx, tenantID, "regrade_job", job.ID, job.ExamID)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			jobs = append(jobs, cloneJob(job))
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}
func (s *MemoryStore) ListAssigned(_ context.Context, tenantID, reviewerID string) ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Item{}
	for jobID, items := range s.items {
		job := s.jobs[jobID]
		if job.TenantID != tenantID || (job.Status != StatusRunning && job.Status != StatusDiffReview) {
			continue
		}
		for _, item := range items {
			if item.AssignedTo == reviewerID && (item.Status == ItemPending || (item.Status == ItemClaimed && item.ClaimedBy == reviewerID)) {
				out = append(out, cloneItem(item))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *MemoryStore) summaryLocked(tenantID, jobID string) (Summary, error) {
	job, err := s.jobLocked(tenantID, jobID)
	if err != nil {
		return Summary{}, err
	}
	return Summary{Job: cloneJob(job), Items: cloneItems(s.items[jobID]), Events: cloneEvents(s.events[jobID])}, nil
}
func (s *MemoryStore) jobLocked(tenantID, jobID string) (Job, error) {
	job, ok := s.jobs[jobID]
	if !ok || job.TenantID != tenantID {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (s *MemoryStore) itemIndexLocked(tenantID, itemID string) (string, int, error) {
	for jobID, items := range s.items {
		job := s.jobs[jobID]
		if job.TenantID != tenantID {
			continue
		}
		for index, item := range items {
			if item.ID == itemID {
				return jobID, index, nil
			}
		}
	}
	return "", 0, ErrNotFound
}
func (s *MemoryStore) addEventLocked(jobID, itemID, kind, actorID string, payload map[string]any) {
	s.events[jobID] = append(s.events[jobID], Event{ID: s.nextIDLocked("regrade-event"), JobID: jobID, ItemID: itemID, Type: kind, ActorID: actorID, Payload: clonePayload(payload), CreatedAt: s.now().UTC()})
}
func (s *MemoryStore) nextIDLocked(prefix string) string {
	s.sequence++
	return fmt.Sprintf("%s-%d", prefix, s.sequence)
}

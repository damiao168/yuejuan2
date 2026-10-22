package regrade

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
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

type memoryRelease struct {
	TenantID  string
	ExamID    string
	ID        string
	Version   int
	Published bool
	Current   bool
	Items     map[string][]SourceItem
}

// MemoryGraderEvidence 是生产环境隔离答案证据投影的测试替代。
// 它以提交 ID 为键，绝不包含来源发布分数或之前的复核事实。
type MemoryGraderEvidence struct {
	Question        GraderQuestion
	FrozenRubric    paper.Rubric
	RawAnswer       string
	OCRText         string
	SegmentStatus   string
	AnswerSegmentID string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{now: func() time.Time { return time.Now().UTC() }, sources: map[string]memoryRelease{}, jobs: map[string]Job{}, items: map[string][]Item{}, events: map[string][]Event{}, idempotency: map[string]string{}, evidence: map[string]MemoryGraderEvidence{}}
}
func (s *MemoryStore) SetNow(now func() time.Time) { s.mu.Lock(); defer s.mu.Unlock(); s.now = now }

func (s *MemoryStore) SeedGraderEvidence(submissionID string, value MemoryGraderEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.Question.KnowledgePoints = append([]string(nil), value.Question.KnowledgePoints...)
	value.FrozenRubric.Points = append([]paper.RubricPoint(nil), value.FrozenRubric.Points...)
	s.evidence[submissionID] = value
}

// SeedPublishedRelease 提供不可变来源快照。
// 测试可预置非当前的历史已发布版本，Preview 同时报告当前发布，帮助操作人员理解影响。
func (s *MemoryStore) SeedPublishedRelease(tenantID, examID, releaseID string, version int, current bool, byQuestion map[string][]SourceItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if current {
		for id, value := range s.sources {
			if value.TenantID == tenantID && value.ExamID == examID {
				value.Current = false
				s.sources[id] = value
			}
		}
	}
	copyByQuestion := map[string][]SourceItem{}
	for questionID, values := range byQuestion {
		copyByQuestion[questionID] = append([]SourceItem(nil), values...)
	}
	s.sources[releaseID] = memoryRelease{TenantID: tenantID, ExamID: examID, ID: releaseID, Version: version, Published: true, Current: current, Items: copyByQuestion}
}

func (s *MemoryStore) SourceItems(_ context.Context, tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sourceItemsLocked(tenantID, examID, questionID, releaseID, selector)
}
func (s *MemoryStore) sourceItemsLocked(tenantID, examID, questionID, releaseID string, selector Selector) ([]SourceItem, error) {
	release, ok := s.sources[releaseID]
	if !ok || !release.Published || release.TenantID != tenantID || release.ExamID != examID {
		return nil, ErrSourceRelease
	}
	items := []SourceItem{}
	for _, item := range release.Items[questionID] {
		if matches(item, selector) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SubmissionID < items[j].SubmissionID })
	return items, nil
}
func (s *MemoryStore) Preview(ctx context.Context, tenantID, examID, questionID, releaseID string, selector Selector) (Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.sourceItemsLocked(tenantID, examID, questionID, releaseID, selector)
	if err != nil {
		return Preview{}, err
	}
	release := s.sources[releaseID]
	currentID, currentVersion := "", 0
	for _, candidate := range s.sources {
		if candidate.TenantID == tenantID && candidate.ExamID == examID && candidate.Current {
			currentID, currentVersion = candidate.ID, candidate.Version
			break
		}
	}
	return previewFromSources(examID, questionID, releaseID, release.Version, currentID, currentVersion, items), nil
}
func (s *MemoryStore) Create(ctx context.Context, tenantID, examID, questionID, actorID string, input CreateInput, sources []SourceItem) (Job, []Item, error) {
	_ = ctx
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
func (s *MemoryStore) List(_ context.Context, tenantID, examID, questionID string) ([]Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := []Job{}
	for _, job := range s.jobs {
		if job.TenantID == tenantID && (examID == "" || job.ExamID == examID) && (questionID == "" || job.QuestionID == questionID) {
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

func (s *MemoryStore) GetGraderContext(_ context.Context, tenantID, itemID, graderID string) (GraderContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return GraderContext{}, err
	}
	item := s.items[jobID][index]
	job := s.jobs[jobID]
	if item.AssignedTo != graderID || item.ClaimedBy != graderID || item.Status != ItemClaimed || (job.Status != StatusRunning && job.Status != StatusDiffReview) {
		return GraderContext{}, ErrAssignmentForbidden
	}
	return s.materialContextLocked(item)
}

func (s *MemoryStore) materialContextLocked(item Item) (GraderContext, error) {
	evidence, ok := s.evidence[item.SubmissionID]
	if !ok || evidence.AnswerSegmentID == "" {
		return GraderContext{}, ErrNotFound
	}
	// 材料含嵌套条件和解析步骤，深拷贝避免读者修改返回对象污染后续任务标准。
	raw, err := json.Marshal(evidence)
	if err != nil {
		return GraderContext{}, err
	}
	var frozen MemoryGraderEvidence
	if err := json.Unmarshal(raw, &frozen); err != nil {
		return GraderContext{}, err
	}
	question, rubric := frozen.Question, frozen.FrozenRubric
	return GraderContext{
		Item: workItem(item), ExpectedRevision: item.Revision, Question: question, FrozenRubric: rubric,
		Answer: GraderAnswer{RawAnswer: evidence.RawAnswer, OCRText: evidence.OCRText, SegmentStatus: evidence.SegmentStatus, SegmentImageURL: "/api/v1/regrade-items/" + item.ID + "/segment-image"},
	}, nil
}

func (s *MemoryStore) GetReviewContext(_ context.Context, tenantID, itemID string) (ReviewContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return ReviewContext{}, err
	}
	item, job := s.items[jobID][index], s.jobs[jobID]
	if item.Status != ItemAwaitingReview || (job.Status != StatusRunning && job.Status != StatusDiffReview) {
		return ReviewContext{}, ErrStateConflict
	}
	value, err := s.materialContextLocked(item)
	if err != nil {
		return ReviewContext{}, err
	}
	value.Answer.SegmentImageURL = "/api/v1/regrade-items/" + item.ID + "/review-segment-image"
	return ReviewContext{Item: cloneItem(item), Context: value}, nil
}

func (s *MemoryStore) GetReviewSegmentID(ctx context.Context, tenantID, itemID string) (string, error) {
	value, err := s.GetReviewContext(ctx, tenantID, itemID)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.evidence[value.Item.SubmissionID].AnswerSegmentID, nil
}

func (s *MemoryStore) GetSegmentID(_ context.Context, tenantID, itemID, graderID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID, index, err := s.itemIndexLocked(tenantID, itemID)
	if err != nil {
		return "", err
	}
	item := s.items[jobID][index]
	job := s.jobs[jobID]
	if item.AssignedTo != graderID || item.ClaimedBy != graderID || item.Status != ItemClaimed || (job.Status != StatusRunning && job.Status != StatusDiffReview) {
		return "", ErrAssignmentForbidden
	}
	evidence, ok := s.evidence[item.SubmissionID]
	if !ok || evidence.AnswerSegmentID == "" {
		return "", ErrNotFound
	}
	return evidence.AnswerSegmentID, nil
}
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
	if job.Status != StatusPaused {
		return Job{}, ErrStateConflict
	}
	job.Status, job.UpdatedAt = StatusRunning, s.now().UTC()
	s.jobs[jobID] = job
	s.addEventLocked(jobID, "", "resumed", actorID, map[string]any{})
	return cloneJob(job), nil
}
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
	candidate, delta := input.Score, input.Score-item.OldScore
	// 候选结果统一进入人工复核，RequireManualReview 只记入事件，不会跳过这一步。
	item.CandidateScore, item.CandidateGradeID, item.Delta = &candidate, input.CandidateGradeID, &delta
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
	if item.Status != ItemAwaitingReview || item.Revision != input.ExpectedRevision {
		if item.Revision != input.ExpectedRevision {
			return Item{}, ErrRevisionConflict
		}
		return Item{}, ErrStateConflict
	}
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
	s.addEventLocked(jobID, item.ID, "item_reviewed", actorID, map[string]any{"decision": input.Decision})
	return cloneItem(item), nil
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

func matches(item SourceItem, selector Selector) bool {
	if len(selector.SubmissionIDs) > 0 {
		found := false
		for _, id := range selector.SubmissionIDs {
			if item.SubmissionID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if selector.ScoreBand != nil {
		if selector.ScoreBand.Min != nil && item.OldScore < *selector.ScoreBand.Min {
			return false
		}
		if selector.ScoreBand.Max != nil && item.OldScore > *selector.ScoreBand.Max {
			return false
		}
	}
	return true
}
func previewFromSources(examID, questionID, releaseID string, version int, currentID string, currentVersion int, items []SourceItem) Preview {
	result := Preview{ExamID: examID, QuestionID: questionID, SourceReleaseID: releaseID, SourceReleaseVersion: version, CurrentReleaseID: currentID, CurrentReleaseVersion: currentVersion, ScoreBands: []ScoreBandCount{}}
	counts := map[float64]int{}
	var min, max float64
	for index, item := range items {
		result.AffectedCount++
		counts[item.OldScore]++
		low, high := -item.OldScore, item.MaxScore-item.OldScore
		// 这里只给出改成零分或满分时的差值范围，不预测实际会改多少分。
		if index == 0 || low < min {
			min = low
		}
		if index == 0 || high > max {
			max = high
		}
	}
	for score, count := range counts {
		result.ScoreBands = append(result.ScoreBands, ScoreBandCount{Score: score, Count: count})
	}
	sort.Slice(result.ScoreBands, func(i, j int) bool { return result.ScoreBands[i].Score < result.ScoreBands[j].Score })
	result.PotentialDelta = PotentialDelta{Min: min, Max: max}
	return result
}
func cloneItems(values []Item) []Item {
	out := append([]Item(nil), values...)
	for i := range out {
		out[i] = cloneItem(out[i])
	}
	return out
}
func cloneItem(value Item) Item {
	value.CandidateRubricSelections = append([]RubricSelection(nil), value.CandidateRubricSelections...)
	if value.CandidateScore != nil {
		v := *value.CandidateScore
		value.CandidateScore = &v
	}
	if value.ReviewedScore != nil {
		v := *value.ReviewedScore
		value.ReviewedScore = &v
	}
	if value.Delta != nil {
		v := *value.Delta
		value.Delta = &v
	}
	return value
}
func cloneJob(value Job) Job {
	value.Selector = cloneSelector(value.Selector)
	if value.ApprovedAt != nil {
		t := *value.ApprovedAt
		value.ApprovedAt = &t
	}
	if value.FinalizedAt != nil {
		t := *value.FinalizedAt
		value.FinalizedAt = &t
	}
	return value
}
func cloneSelector(value Selector) Selector {
	value.SubmissionIDs = append([]string(nil), value.SubmissionIDs...)
	if value.ScoreBand != nil {
		b := *value.ScoreBand
		if b.Min != nil {
			v := *b.Min
			b.Min = &v
		}
		if b.Max != nil {
			v := *b.Max
			b.Max = &v
		}
		value.ScoreBand = &b
	}
	return value
}
func cloneEvents(values []Event) []Event {
	out := append([]Event(nil), values...)
	for i := range out {
		out[i].Payload = clonePayload(out[i].Payload)
	}
	return out
}
func clonePayload(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}

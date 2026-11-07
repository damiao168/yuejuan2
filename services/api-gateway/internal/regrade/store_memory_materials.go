package regrade

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
)

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

func (s *MemoryStore) SeedGraderEvidence(submissionID string, value MemoryGraderEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value.Question.KnowledgePoints = append([]string(nil), value.Question.KnowledgePoints...)
	value.FrozenRubric.Points = append([]paper.RubricPoint(nil), value.FrozenRubric.Points...)
	s.evidence[submissionID] = value
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
	if (item.Status != ItemAwaitingReview && item.Status != ItemException) || (job.Status != StatusRunning && job.Status != StatusDiffReview) {
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

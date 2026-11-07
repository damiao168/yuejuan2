package score

import (
	"context"
	"time"
)

func (s *MemoryStore) ConfirmGrades(ctx context.Context, tenantID string, examID string, actorID string, input ConfirmInput) ([]SubmissionGrade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay []SubmissionGrade
	if found, err := s.receipts.Load(ctx, tenantID, actorID, "score.confirm", examID, input, &replay); err != nil || found {
		return replay, err
	}
	quality := s.qualityLocked(examID, false)
	if !quality.Passed {
		return nil, ErrQualityGateFailed
	}
	now := time.Now().UTC()
	found := false
	for id, grade := range s.submissions {
		if grade.ExamID != examID {
			continue
		}
		found = true
		if grade.Locked || grade.Status != "pending_confirmation" {
			return nil, ErrInvalidTransition
		}
		grade.Status = "confirmed"
		grade.Revision++
		grade.ConfirmedBy = actorID
		grade.ConfirmedAt = &now
		grade.UpdatedAt = now
		grade.TenantID = tenantID
		s.submissions[id] = grade
	}
	if !found {
		return nil, ErrNotFound
	}
	for id, grade := range s.finals {
		if grade.ExamID != examID {
			continue
		}
		grade.Status = "confirmed"
		grade.UpdatedAt = now
		grade.TenantID = tenantID
		s.finals[id] = grade
	}
	result := s.listSubmissionGradesLocked(examID, true)
	if err := s.receipts.Save(ctx, tenantID, actorID, "score.confirm", examID, input, result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *MemoryStore) PublishGrades(ctx context.Context, tenantID string, examID string, actorID string, input PublishInput) (PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var replay PublishResult
	if found, err := s.receipts.Load(ctx, tenantID, actorID, "score.publish", examID, input, &replay); err != nil || found {
		return replay, err
	}
	for _, grade := range s.submissions {
		if grade.ExamID == examID && grade.Locked {
			return PublishResult{}, ErrInvalidTransition
		}
	}
	quality := s.qualityLocked(examID, true)
	if !quality.Passed {
		return PublishResult{Status: "blocked", Quality: quality}, ErrQualityGateFailed
	}
	if !canPublishExamStatus(s.examStatuses[examID]) {
		return PublishResult{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	for id, grade := range s.submissions {
		if grade.ExamID != examID {
			continue
		}
		grade.Status = "published"
		grade.Locked = true
		grade.Revision++
		grade.PublishedBy = actorID
		grade.PublishedAt = &now
		grade.UpdatedAt = now
		grade.TenantID = tenantID
		s.submissions[id] = grade
	}
	for id, grade := range s.finals {
		if grade.ExamID != examID {
			continue
		}
		grade.Status = "locked"
		grade.Locked = true
		grade.UpdatedAt = now
		grade.TenantID = tenantID
		s.finals[id] = grade
	}
	s.examStatuses[examID] = "published"
	result := PublishResult{
		Status:           "published",
		SubmissionGrades: s.listSubmissionGradesLocked(examID, true),
		Quality:          QualityReport{Passed: true},
		PublishedAt:      now,
	}

	if err := s.receipts.Save(ctx, tenantID, actorID, "score.publish", examID, input, result); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

func canPublishExamStatus(status string) bool {
	switch status {
	case "draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized":
		return true
	default:
		return false
	}
}

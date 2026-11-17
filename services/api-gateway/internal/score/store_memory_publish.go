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

// PublishGrades 保留旧调用签名，正式发布和回执均交给不可变版本发布器。
func (s *MemoryStore) PublishGrades(ctx context.Context, tenantID, examID, actorID string, input PublishInput) (PublishResult, error) {
	return s.publications.publish(ctx, tenantID, examID, actorID, input)
}

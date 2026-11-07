package score

import (
	"context"
	"sort"
	"time"
)

func (s *MemoryStore) FinalizeExam(_ context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.formalReleases[examID] {
		return FinalizeResult{}, ErrInvalidTransition
	}
	for _, grade := range s.submissions {
		newerFinal := false
		if grade.Status == "confirmed" {
			for _, finalID := range s.finalBySeg {
				final := s.finals[finalID]
				newerFinal = newerFinal || (final.ExamID == examID && final.SubmissionID == grade.SubmissionID && final.Status == "pending_confirmation")
			}
		}
		if grade.ExamID == examID && (grade.Locked || (grade.Status != "pending_confirmation" && !newerFinal)) {
			return FinalizeResult{}, ErrInvalidTransition
		}
	}
	created := s.createAutomaticFinalsLocked(tenantID, examID, actorID)
	s.recalculateSubmissionGradesLocked(tenantID, examID, actorID, "pending_confirmation")
	grades := s.listSubmissionGradesLocked(examID, true)
	quality := s.qualityLocked(examID, false)
	return FinalizeResult{
		Status:            "pending_confirmation",
		CreatedFinals:     created,
		SubmissionGrades:  grades,
		Quality:           quality,
		AvailableStatuses: Statuses(),
	}, nil
}

// automaticFinalSeedLocked 对应 autoprep.FinalSourcesCTE：人工结果优先；
// 只有明确自动通过、无需复核且非 Mock 的规则结果才补为最终题分。调用方先排除已有终评的答题区。
func (s *MemoryStore) automaticFinalSeedLocked(answerSegmentID string) (GradeSeed, string, bool) {
	if human, ok := s.humanGrades[answerSegmentID]; ok {
		return human, "single_review", true
	}
	if rule, ok := s.ruleGrades[answerSegmentID]; ok && rule.AutoPass && !rule.NeedsReview && !rule.Mock {
		return rule, "rule_auto", true
	}
	return GradeSeed{}, "", false
}

// createAutomaticFinalsLocked 为尚无终评的答题区补齐可直接生成的终评，返回新建数量。
func (s *MemoryStore) createAutomaticFinalsLocked(tenantID, examID, actorID string) int {
	created := 0
	now := time.Now().UTC()
	for _, seg := range s.sortedSegmentsLocked(examID) {
		if _, ok := s.finalBySeg[seg.AnswerSegmentID]; ok {
			continue
		}
		seed, source, ok := s.automaticFinalSeedLocked(seg.AnswerSegmentID)
		if !ok {
			continue
		}
		grade := FinalGrade{
			ID:              s.id("final-grade"),
			TenantID:        tenantID,
			ExamID:          seg.ExamID,
			QuestionID:      seg.QuestionID,
			QuestionNo:      seg.QuestionNo,
			AnswerSegmentID: seg.AnswerSegmentID,
			SubmissionID:    seg.SubmissionID,
			AnonymousCode:   seg.AnonymousCode,
			Score:           seed.Score,
			MaxScore:        valueOr(seed.MaxScore, seg.MaxScore),
			Source:          source,
			Status:          "pending_confirmation",
			CreatedBy:       actorID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		s.finals[grade.ID] = grade
		s.finalBySeg[seg.AnswerSegmentID] = grade.ID
		created++
	}
	return created
}

func (s *MemoryStore) recalculateSubmissionGradesLocked(tenantID string, examID string, actorID string, status string) {
	now := time.Now().UTC()
	grouped := map[string][]FinalGrade{}
	for _, gradeID := range s.finalBySeg {
		grade := s.finals[gradeID]
		if grade.ExamID == examID {
			grouped[grade.SubmissionID] = append(grouped[grade.SubmissionID], grade)
		}
	}
	for submissionID, items := range grouped {
		var total, max float64
		anonymous := submissionID
		studentID := ""
		for _, item := range items {
			total += item.Score
			max += item.MaxScore
			anonymous = item.AnonymousCode
			if seg, ok := s.segments[item.AnswerSegmentID]; ok {
				studentID = seg.StudentID
			}
		}
		id := key(examID, submissionID)
		existing := s.submissions[id]
		// 与 PostgreSQL 一致：首发前确认之后新增了终评，才允许把已确认总分退回待确认。
		newerFinal := false
		if !s.formalReleases[examID] {
			for _, item := range items {
				newerFinal = newerFinal || item.Status == "pending_confirmation"
			}
		}
		if existing.ID != "" && (existing.Locked || (existing.Status != "pending_confirmation" && !(existing.Status == "confirmed" && newerFinal))) {
			continue
		}
		if existing.ID == "" {
			existing.ID = s.id("submission-grade")
			existing.CreatedAt = now
			existing.CreatedBy = actorID
			existing.Revision = 1
		} else {
			existing.Revision++
		}
		existing.TenantID = tenantID
		existing.ExamID = examID
		existing.SubmissionID = submissionID
		existing.StudentID = studentID
		existing.AnonymousCode = anonymous
		existing.TotalScore = total
		existing.MaxScore = max
		existing.Status = status
		existing.ConfirmedBy, existing.ConfirmedAt = "", nil
		existing.UpdatedAt = now
		s.submissions[id] = existing
	}
}

func (s *MemoryStore) sortedSegmentsLocked(examID string) []SegmentSeed {
	out := []SegmentSeed{}
	for _, seg := range s.segments {
		if seg.ExamID == examID {
			out = append(out, seg)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubmissionID == out[j].SubmissionID {
			return out[i].QuestionNo < out[j].QuestionNo
		}
		return out[i].SubmissionID < out[j].SubmissionID
	})
	return out
}

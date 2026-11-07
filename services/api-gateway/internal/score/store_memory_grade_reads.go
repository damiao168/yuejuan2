package score

import (
	"context"
	"sort"
	"strings"
)

func (s *MemoryStore) ListExamGrades(_ context.Context, tenantID string, examID string, filter GradeListFilter) (GradeListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grades := s.listSubmissionGradesLocked(examID, true)
	result := GradeListResult{Grades: []SubmissionGrade{}, Total: len(grades), AllLocked: len(grades) > 0}
	// 总数和锁定汇总基于全部成绩，筛选或分页不能把未锁定的学生隐藏成整场已锁定。
	result.AllLocked = prepareExamGradeList(grades, tenantID)
	filtered := filterExamGradeList(grades, filter)
	result.FilteredTotal = len(filtered)
	result.Grades = pageExamGradeList(filtered, filter)
	return result, nil
}

func prepareExamGradeList(grades []SubmissionGrade, tenantID string) bool {
	allLocked := len(grades) > 0
	for i := range grades {
		grades[i].TenantID = tenantID
		for j := range grades[i].Items {
			grades[i].Items[j].TenantID = tenantID
		}
		if !grades[i].Locked && grades[i].Status != "published" && grades[i].Status != "locked" {
			allLocked = false
		}
	}
	return allLocked
}

func filterExamGradeList(grades []SubmissionGrade, filter GradeListFilter) []SubmissionGrade {
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	filtered := make([]SubmissionGrade, 0, len(grades))
	for _, grade := range grades {
		if filter.Status != "" && grade.Status != filter.Status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(grade.AnonymousCode), query) &&
			!strings.Contains(strings.ToLower(grade.SubmissionID), query) {
			continue
		}
		filtered = append(filtered, grade)
	}
	return filtered
}

func pageExamGradeList(filtered []SubmissionGrade, filter GradeListFilter) []SubmissionGrade {
	if filter.CursorAnonymousCode != "" && filter.CursorID != "" {
		start := 0
		for start < len(filtered) && (filtered[start].AnonymousCode < filter.CursorAnonymousCode ||
			(filtered[start].AnonymousCode == filter.CursorAnonymousCode && filtered[start].ID <= filter.CursorID)) {
			start++
		}
		filtered = filtered[start:]
	}
	if filter.Limit > 0 && len(filtered) > filter.Limit {
		filtered = filtered[:filter.Limit]
	}
	return filtered
}

func (s *MemoryStore) GetStudentGrade(_ context.Context, tenantID string, studentID string, examID string) (SubmissionGrade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, grade := range s.submissions {
		if grade.ExamID == examID && grade.StudentID == studentID && grade.Status == "published" && grade.Locked {
			out := cloneSubmissionGrade(grade)
			out.TenantID = tenantID
			out.Items = s.itemsForSubmissionLocked(grade.SubmissionID)
			return out, nil
		}
	}
	return SubmissionGrade{}, ErrNotFound
}

func (s *MemoryStore) listSubmissionGradesLocked(examID string, withItems bool) []SubmissionGrade {
	out := []SubmissionGrade{}
	for _, grade := range s.submissions {
		if grade.ExamID != examID {
			continue
		}
		item := cloneSubmissionGrade(grade)
		if withItems {
			item.Items = s.itemsForSubmissionLocked(grade.SubmissionID)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AnonymousCode == out[j].AnonymousCode {
			return out[i].ID < out[j].ID
		}
		return out[i].AnonymousCode < out[j].AnonymousCode
	})
	return out
}

func (s *MemoryStore) itemsForSubmissionLocked(submissionID string) []FinalGrade {
	out := []FinalGrade{}
	for _, grade := range s.finals {
		if grade.SubmissionID == submissionID {
			out = append(out, grade)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].QuestionNo < out[j].QuestionNo
	})
	return out
}

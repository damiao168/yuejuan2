package score

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type SegmentSeed struct {
	ExamID          string
	SubmissionID    string
	StudentID       string
	AnonymousCode   string
	AnswerSegmentID string
	QuestionID      string
	QuestionNo      string
	MaxScore        float64
}

type GradeSeed struct {
	AnswerSegmentID string
	Score           float64
	MaxScore        float64
	Source          string
	Mock            bool
	NeedsReview     bool
	AutoPass        bool
}

type TaskSeed struct {
	ExamID string
	Status string
}

type MemoryStore struct {
	mu           sync.RWMutex
	next         int
	segments     map[string]SegmentSeed
	finals       map[string]FinalGrade
	finalBySeg   map[string]string
	humanGrades  map[string]GradeSeed
	ruleGrades   map[string]GradeSeed
	submissions  map[string]SubmissionGrade
	examStatuses map[string]string
	reviewTasks  []TaskSeed
	arbTasks     []TaskSeed
	ocrTasks     []TaskSeed
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		next:         1,
		segments:     map[string]SegmentSeed{},
		finals:       map[string]FinalGrade{},
		finalBySeg:   map[string]string{},
		humanGrades:  map[string]GradeSeed{},
		ruleGrades:   map[string]GradeSeed{},
		submissions:  map[string]SubmissionGrade{},
		examStatuses: map[string]string{},
	}
}

func (s *MemoryStore) AddSegment(seed SegmentSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seed.AnonymousCode == "" {
		seed.AnonymousCode = seed.SubmissionID
	}
	s.segments[seed.AnswerSegmentID] = seed
	if _, exists := s.examStatuses[seed.ExamID]; !exists {
		s.examStatuses[seed.ExamID] = "collecting"
	}
}

func (s *MemoryStore) SetExamStatusForTest(examID string, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.examStatuses[examID] = status
}

func (s *MemoryStore) ExamStatusForTest(examID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.examStatuses[examID]
}

func (s *MemoryStore) AddFinalGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seg := s.segments[seed.AnswerSegmentID]
	now := time.Now().UTC()
	grade := FinalGrade{
		ID:              s.id("final-grade"),
		TenantID:        "",
		ExamID:          seg.ExamID,
		QuestionID:      seg.QuestionID,
		QuestionNo:      seg.QuestionNo,
		AnswerSegmentID: seed.AnswerSegmentID,
		SubmissionID:    seg.SubmissionID,
		AnonymousCode:   seg.AnonymousCode,
		Score:           seed.Score,
		MaxScore:        seed.MaxScore,
		Source:          normalizeSource(seed.Source),
		Status:          "pending_confirmation",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	s.finals[grade.ID] = grade
	s.finalBySeg[seed.AnswerSegmentID] = grade.ID
}

func (s *MemoryStore) AddHumanGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.humanGrades[seed.AnswerSegmentID] = seed
}

func (s *MemoryStore) AddRuleGrade(seed GradeSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ruleGrades[seed.AnswerSegmentID] = seed
}

func (s *MemoryStore) AddReviewTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewTasks = append(s.reviewTasks, seed)
}

func (s *MemoryStore) AddArbitrationTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.arbTasks = append(s.arbTasks, seed)
}

func (s *MemoryStore) AddOCRTask(seed TaskSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ocrTasks = append(s.ocrTasks, seed)
}

func (s *MemoryStore) FinalizeExam(_ context.Context, tenantID string, examID string, actorID string) (FinalizeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, grade := range s.submissions {
		if grade.ExamID == examID && (grade.Locked || grade.Status != "pending_confirmation") {
			return FinalizeResult{}, ErrInvalidTransition
		}
	}
	created := 0
	now := time.Now().UTC()
	for _, seg := range s.sortedSegmentsLocked(examID) {
		if _, ok := s.finalBySeg[seg.AnswerSegmentID]; ok {
			continue
		}
		var seed GradeSeed
		source := ""
		if human, ok := s.humanGrades[seg.AnswerSegmentID]; ok {
			seed = human
			source = "single_review"
		} else if rule, ok := s.ruleGrades[seg.AnswerSegmentID]; ok && rule.AutoPass && !rule.NeedsReview && !rule.Mock {
			seed = rule
			source = "rule_auto"
		} else {
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

func (s *MemoryStore) ListExamGrades(_ context.Context, tenantID string, examID string) ([]SubmissionGrade, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grades := s.listSubmissionGradesLocked(examID, true)
	if len(grades) == 0 {
		return []SubmissionGrade{}, nil
	}
	for i := range grades {
		grades[i].TenantID = tenantID
		for j := range grades[i].Items {
			grades[i].Items[j].TenantID = tenantID
		}
	}
	return grades, nil
}

func (s *MemoryStore) CheckQuality(_ context.Context, _ string, examID string, requirePendingPublish bool) (QualityReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.qualityLocked(examID, requirePendingPublish), nil
}

func (s *MemoryStore) ConfirmGrades(_ context.Context, tenantID string, examID string, actorID string, _ ConfirmInput) ([]SubmissionGrade, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return s.listSubmissionGradesLocked(examID, true), nil
}

func (s *MemoryStore) PublishGrades(_ context.Context, tenantID string, examID string, actorID string, _ PublishInput) (PublishResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	return PublishResult{
		Status:           "published",
		SubmissionGrades: s.listSubmissionGradesLocked(examID, true),
		Quality:          QualityReport{Passed: true},
		PublishedAt:      now,
	}, nil
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

func (s *MemoryStore) ExportGradesCSV(_ context.Context, tenantID string, examID string, actorID string) (ExportResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grades := s.listSubmissionGradesLocked(examID, false)
	if len(grades) == 0 {
		return ExportResult{}, ErrNotFound
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	exportedAt := time.Now().UTC().Format(time.RFC3339)
	watermark := fmt.Sprintf("EduGrade export tenant=%s exam=%s actor=%s at=%s", tenantID, examID, actorID, exportedAt)
	_ = writer.Write([]string{"submission_id", "student_id", "anonymous_code", "total_score", "max_score", "status", "locked", "exported_by", "exported_at", "watermark"})
	for _, grade := range grades {
		_ = writer.Write([]string{
			grade.SubmissionID,
			grade.StudentID,
			grade.AnonymousCode,
			fmt.Sprintf("%.2f", grade.TotalScore),
			fmt.Sprintf("%.2f", grade.MaxScore),
			grade.Status,
			fmt.Sprintf("%t", grade.Locked),
			actorID,
			exportedAt,
			watermark,
		})
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return ExportResult{}, err
	}
	return ExportResult{
		Filename:    fmt.Sprintf("exam-%s-grades.csv", examID),
		ContentType: "text/csv; charset=utf-8",
		Content:     buf.Bytes(),
		RowCount:    len(grades),
		Watermark:   watermark,
	}, nil
}

func (s *MemoryStore) recalculateSubmissionGradesLocked(tenantID string, examID string, actorID string, status string) {
	now := time.Now().UTC()
	grouped := map[string][]FinalGrade{}
	for _, grade := range s.finals {
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
		if existing.Locked {
			continue
		}
		if existing.ID == "" {
			existing.ID = s.id("submission-grade")
			existing.CreatedAt = now
			existing.CreatedBy = actorID
		}
		existing.TenantID = tenantID
		existing.ExamID = examID
		existing.SubmissionID = submissionID
		existing.StudentID = studentID
		existing.AnonymousCode = anonymous
		existing.TotalScore = total
		existing.MaxScore = max
		existing.Status = status
		existing.UpdatedAt = now
		s.submissions[id] = existing
	}
}

func (s *MemoryStore) qualityLocked(examID string, requirePendingPublish bool) QualityReport {
	issues := []QualityIssue{}
	if count := countTasks(s.reviewTasks, examID, func(status string) bool { return status != "submitted" && status != "completed" }); count > 0 {
		issues = append(issues, QualityIssue{Code: "unfinished_review_tasks", Message: "there are unfinished review tasks", Blocking: true, Count: count})
	}
	if count := countTasks(s.arbTasks, examID, func(status string) bool { return status != "submitted" }); count > 0 {
		issues = append(issues, QualityIssue{Code: "unfinished_arbitration_tasks", Message: "there are unfinished arbitration tasks", Blocking: true, Count: count})
	}
	if count := countTasks(s.ocrTasks, examID, func(status string) bool { return status == "failed" }); count > 0 {
		issues = append(issues, QualityIssue{Code: "ocr_failed_unhandled", Message: "there are failed OCR tasks", Blocking: true, Count: count})
	}
	missing := 0
	for _, seg := range s.segments {
		if seg.ExamID == examID {
			if _, ok := s.finalBySeg[seg.AnswerSegmentID]; !ok {
				missing++
			}
		}
	}
	if missing > 0 {
		issues = append(issues, QualityIssue{Code: "missing_final_grades", Message: "there are answer segments without final grades", Blocking: true, Count: missing})
	}
	if requirePendingPublish {
		unconfirmed := 0
		for _, grade := range s.submissions {
			if grade.ExamID == examID && grade.Status != "confirmed" && grade.Status != "pending_publish" && grade.Status != "published" && grade.Status != "locked" {
				unconfirmed++
			}
		}
		if unconfirmed > 0 {
			issues = append(issues, QualityIssue{Code: "grades_not_confirmed", Message: "there are grades not ready for publish", Blocking: true, Count: unconfirmed})
		}
	}
	return QualityReport{Passed: len(issues) == 0, Issues: issues}
}

func canPublishExamStatus(status string) bool {
	switch status {
	case "draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized":
		return true
	default:
		return false
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

func countTasks(tasks []TaskSeed, examID string, pred func(string) bool) int {
	count := 0
	for _, task := range tasks {
		if task.ExamID == examID && pred(task.Status) {
			count++
		}
	}
	return count
}

func cloneSubmissionGrade(in SubmissionGrade) SubmissionGrade {
	in.ConfirmedAt = cloneTime(in.ConfirmedAt)
	in.PublishedAt = cloneTime(in.PublishedAt)
	in.Items = append([]FinalGrade(nil), in.Items...)
	return in
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func normalizeSource(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return "single_review"
	}
	return source
}

func valueOr(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
}

func key(left string, right string) string {
	return left + "|" + right
}

func (s *MemoryStore) id(prefix string) string {
	id := fmt.Sprintf("%s-%d", prefix, s.next)
	s.next++
	return id
}

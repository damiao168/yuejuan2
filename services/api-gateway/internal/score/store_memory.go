package score

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"encoding/csv"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/csvsafe"
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

type RosterSeed struct {
	ExamID      string
	StudentID   string
	StudentNo   string
	StudentName string
	ClassID     string
	ClassName   string
}

type RosterSubmissionSeed struct {
	ExamID           string
	SubmissionID     string
	StudentID        string
	CandidateNo      string
	ExpectedPages    int
	ActualPages      int
	QualityStatus    string
	SubmissionStatus string
}

type MemoryStore struct {
	receipts          commandreceipt.Memory
	mu                sync.RWMutex
	next              int
	segments          map[string]SegmentSeed
	finals            map[string]FinalGrade
	finalBySeg        map[string]string
	humanGrades       map[string]GradeSeed
	ruleGrades        map[string]GradeSeed
	submissions       map[string]SubmissionGrade
	examStatuses      map[string]string
	reviewTasks       []TaskSeed
	arbTasks          []TaskSeed
	ocrTasks          []TaskSeed
	roster            map[string]RosterSeed
	rosterSubmissions []RosterSubmissionSeed
	attendance        map[string]RosterEntry
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
		roster:       map[string]RosterSeed{},
		attendance:   map[string]RosterEntry{},
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

func (s *MemoryStore) AddRosterStudent(seed RosterSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roster[key(seed.ExamID, seed.StudentID)] = seed
	if _, exists := s.examStatuses[seed.ExamID]; !exists {
		s.examStatuses[seed.ExamID] = "collecting"
	}
}

func (s *MemoryStore) AddRosterSubmission(seed RosterSubmissionSeed) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rosterSubmissions = append(s.rosterSubmissions, seed)
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
		// 人工结果优先；只有明确自动通过、无需复核且非 Mock 的规则结果才补为最终题分。
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

func (s *MemoryStore) ListExamGrades(_ context.Context, tenantID string, examID string, filter GradeListFilter) (GradeListResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	grades := s.listSubmissionGradesLocked(examID, true)
	result := GradeListResult{Grades: []SubmissionGrade{}, Total: len(grades), AllLocked: len(grades) > 0}
	for i := range grades {
		grades[i].TenantID = tenantID
		for j := range grades[i].Items {
			grades[i].Items[j].TenantID = tenantID
		}
		if !grades[i].Locked && grades[i].Status != "published" && grades[i].Status != "locked" {
			result.AllLocked = false
		}
	}
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
	result.FilteredTotal = len(filtered)
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
	result.Grades = filtered
	return result, nil
}

func (s *MemoryStore) CheckQuality(_ context.Context, _ string, examID string, requirePendingPublish bool) (QualityReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.qualityLocked(examID, requirePendingPublish), nil
}

func (s *MemoryStore) ListRoster(_ context.Context, _ string, examID string) (RosterReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rosterReportLocked(examID), nil
}

func (s *MemoryStore) SetAttendance(_ context.Context, _ string, examID string, studentID string, actorID string, input AttendanceInput) (RosterReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var valid bool
	input, valid = normalizeAttendanceInput(input)
	if !valid {
		return RosterReport{}, ErrInvalidInput
	}
	if _, ok := s.roster[key(examID, studentID)]; !ok {
		return RosterReport{}, ErrNotFound
	}
	if status := s.examStatuses[examID]; status == "published" || status == "archived" {
		return RosterReport{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	s.attendance[key(examID, studentID)] = RosterEntry{
		Status: input.Status, AttendanceReason: input.Reason, MarkedBy: actorID, MarkedAt: &now,
	}
	return s.rosterReportLocked(examID), nil
}

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
		_ = writer.Write(csvsafe.Row([]string{
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
		}))
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
	roster := s.rosterReportLocked(examID)
	missingSubmissions := 0
	for _, entry := range roster.Entries {
		if entry.ResolutionCode == "missing_submission" {
			missingSubmissions++
		}
	}
	if missingSubmissions > 0 {
		issues = append(issues, QualityIssue{Code: "missing_submission_unresolved", Message: "expected students have no matched submission", Blocking: true, Count: missingSubmissions})
	}
	if roster.Summary.Unidentified > 0 {
		issues = append(issues, QualityIssue{Code: "unidentified_submission", Message: "submissions are not uniquely matched to an expected student", Blocking: true, Count: roster.Summary.Unidentified})
	}
	if roster.Summary.MissingPages > 0 {
		issues = append(issues, QualityIssue{Code: "missing_pages_unresolved", Message: "matched submissions have unresolved missing or rejected pages", Blocking: true, Count: roster.Summary.MissingPages})
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

func (s *MemoryStore) rosterReportLocked(examID string) RosterReport {
	report := RosterReport{Entries: []RosterEntry{}}
	rosterKeys := map[string]RosterSeed{}
	submissionCounts := map[string]int{}
	for rosterKey, seed := range s.roster {
		if seed.ExamID == examID {
			rosterKeys[rosterKey] = seed
			report.Summary.Expected++
		}
	}
	for _, submission := range s.rosterSubmissions {
		if submission.ExamID == examID {
			submissionCounts[submission.StudentID]++
			report.Summary.Received++
		}
	}
	for rosterKey, seed := range rosterKeys {
		decision := s.attendance[rosterKey]
		submissionCount := submissionCounts[seed.StudentID]
		entry := RosterEntry{
			Key: "student:" + seed.StudentID, StudentID: seed.StudentID, StudentNo: seed.StudentNo,
			StudentName: seed.StudentName, ClassID: seed.ClassID, ClassName: seed.ClassName,
			Status: "unmatched", ResolutionCode: "missing_submission",
			AttendanceReason: decision.AttendanceReason, MarkedBy: decision.MarkedBy, MarkedAt: cloneTime(decision.MarkedAt),
		}
		if decision.Status == "absent" && submissionCount == 0 {
			// 缺考只在没有答卷时消除缺交；已有答卷的缺考标记仍是待核对冲突。
			entry.Status = "absent"
			entry.ResolutionCode = "absent"
			report.Summary.Absent++
		} else if submissionCount == 1 && decision.Status != "absent" {
			for _, submission := range s.rosterSubmissions {
				if submission.ExamID != examID || submission.StudentID != seed.StudentID {
					continue
				}
				entry.SubmissionID = submission.SubmissionID
				entry.CandidateNo = submission.CandidateNo
				entry.ExpectedPageCount = submission.ExpectedPages
				entry.ActualPageCount = submission.ActualPages
				if submission.ActualPages < submission.ExpectedPages || submission.QualityStatus == "failed" || submission.SubmissionStatus == "rejected" {
					entry.Status = "missing_pages"
					entry.ResolutionCode = "missing_pages"
					report.Summary.MissingPages++
				} else if grade, ok := s.submissions[key(examID, submission.SubmissionID)]; ok {
					entry.Status = "graded"
					entry.ResolutionCode = "graded"
					total, max := grade.TotalScore, grade.MaxScore
					entry.TotalScore, entry.MaxScore = &total, &max
					report.Summary.Graded++
				} else {
					entry.ResolutionCode = "grading_incomplete"
				}
				break
			}
		} else if submissionCount > 1 {
			entry.ResolutionCode = "duplicate_submission"
		} else if decision.Status == "absent" {
			entry.ResolutionCode = "absent_has_submission"
		}
		if entry.Status == "unmatched" {
			report.Summary.Unresolved++
		}
		report.Entries = append(report.Entries, entry)
	}
	for _, submission := range s.rosterSubmissions {
		if submission.ExamID != examID {
			continue
		}
		rosterSeed, inRoster := rosterKeys[key(examID, submission.StudentID)]
		decision := s.attendance[key(examID, submission.StudentID)]
		if submission.StudentID != "" && inRoster && decision.Status != "absent" && submissionCounts[submission.StudentID] == 1 {
			continue
		}
		code := "student_not_in_roster"
		switch {
		case submission.StudentID == "":
			code = "student_unidentified"
		case inRoster && decision.Status == "absent":
			code = "absent_has_submission"
		case inRoster && submissionCounts[submission.StudentID] > 1:
			code = "duplicate_submission"
		}
		report.Entries = append(report.Entries, RosterEntry{
			Key: "submission:" + submission.SubmissionID, StudentID: submission.StudentID,
			StudentNo: rosterSeed.StudentNo, StudentName: rosterSeed.StudentName,
			ClassID: rosterSeed.ClassID, ClassName: rosterSeed.ClassName,
			SubmissionID: submission.SubmissionID, CandidateNo: submission.CandidateNo,
			Status: "unmatched", ResolutionCode: code, ExpectedPageCount: submission.ExpectedPages, ActualPageCount: submission.ActualPages,
		})
		report.Summary.Unidentified++
	}
	report.Summary.Unresolved += report.Summary.Unidentified + report.Summary.MissingPages
	sort.Slice(report.Entries, func(i, j int) bool {
		if report.Entries[i].ClassName == report.Entries[j].ClassName {
			return report.Entries[i].StudentNo < report.Entries[j].StudentNo
		}
		return report.Entries[i].ClassName < report.Entries[j].ClassName
	})
	return report
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

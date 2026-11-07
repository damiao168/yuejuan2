package score

import (
	"context"
)

// CheckQuality 与 PostgreSQL 一致：publish 阶段在自动准备前提成立时，在“准备之后”的视图上检查；
// confirmation 阶段和写操作内部仍用严格检查。
func (s *MemoryStore) CheckQuality(_ context.Context, _ string, examID string, requirePendingPublish bool) (QualityReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if requirePendingPublish && s.automaticPreparationLocked(examID) {
		return s.preparedViewLocked(examID).preparedPublishQualityLocked(examID), nil
	}
	return s.qualityLocked(examID, requirePendingPublish), nil
}

// automaticPreparationLocked 对应 autoprep.State.Automatic()：尚无正式发布、没有锁定总分、没有非法状态的总分。
// published、locked 及其它非法状态都不属于 pending_confirmation 和 confirmed。
func (s *MemoryStore) automaticPreparationLocked(examID string) bool {
	if s.formalReleases[examID] {
		return false
	}
	for _, grade := range s.submissions {
		if grade.ExamID == examID && (grade.Locked || (grade.Status != "pending_confirmation" && grade.Status != "confirmed")) {
			return false
		}
	}
	return true
}

// preparedViewLocked 复制本考试的终评和总分，再按创建草稿时的自动准备补齐终评、重算可恢复的总分。
// 视图只用于只读检查；调用方持有读锁，原存储不被修改，视图不使用自己的锁。
func (s *MemoryStore) preparedViewLocked(examID string) *MemoryStore {
	view := &MemoryStore{
		next: s.next, segments: s.segments, humanGrades: s.humanGrades, ruleGrades: s.ruleGrades,
		examStatuses: s.examStatuses, reviewTasks: s.reviewTasks, arbTasks: s.arbTasks, ocrTasks: s.ocrTasks,
		roster: s.roster, rosterSubmissions: s.rosterSubmissions, attendance: s.attendance, formalReleases: s.formalReleases,
		finals: make(map[string]FinalGrade, len(s.finals)), finalBySeg: make(map[string]string, len(s.finalBySeg)),
		submissions: make(map[string]SubmissionGrade, len(s.submissions)),
	}
	for id, grade := range s.finals {
		view.finals[id] = grade
	}
	for segmentID, id := range s.finalBySeg {
		view.finalBySeg[segmentID] = id
	}
	for id, grade := range s.submissions {
		view.submissions[id] = cloneSubmissionGrade(grade)
	}
	view.createAutomaticFinalsLocked("", examID, "")
	view.recalculateSubmissionGradesLocked("", examID, "", "pending_confirmation")
	return view
}

// preparedPublishQualityLocked 在准备后视图上做 publish 阶段检查。准备成功后会确认全部待确认总分，
// 所以不报 grades_not_confirmed；准备后仍没有任何总分时报 no_submission_grades（与 PostgreSQL 放宽读取相同）。
func (s *MemoryStore) preparedPublishQualityLocked(examID string) QualityReport {
	quality := s.qualityLocked(examID, false)
	for _, grade := range s.submissions {
		if grade.ExamID == examID {
			return quality
		}
	}
	quality.Issues = append(quality.Issues, QualityIssue{Code: "no_submission_grades", Message: "there are no submission grades to publish", Blocking: true, Count: 1})
	quality.Passed = false
	return quality
}

func (s *MemoryStore) qualityLocked(examID string, requirePendingPublish bool) QualityReport {
	// 质量检查只汇总事实；问题按任务、终分、名册、确认状态的原顺序输出。
	issues := s.taskAndFinalQualityIssuesLocked(examID)
	issues = appendRosterQualityIssues(issues, s.rosterReportLocked(examID))
	// 确认和发布共用完整性要求；只有确认状态检查属于发布阶段。
	issues = s.gradeCompletenessLocked(examID, issues)
	issues = s.totalIntegrityIssuesLocked(examID, issues)
	if requirePendingPublish {
		issues = s.appendUnconfirmedQualityIssuesLocked(issues, examID)
	}
	return QualityReport{Passed: len(issues) == 0, Issues: issues}
}

func (s *MemoryStore) taskAndFinalQualityIssuesLocked(examID string) []QualityIssue {
	issues := []QualityIssue{}
	// 已取消任务留作历史；与生产查询一致，只把可继续处理的阅卷和仲裁状态计为阻断。
	if count := countTasks(s.reviewTasks, examID, func(status string) bool {
		return status == "pending" || status == "assigned" || status == "in_progress" || status == "returned"
	}); count > 0 {
		issues = append(issues, QualityIssue{Code: "unfinished_review_tasks", Message: "there are unfinished review tasks", Blocking: true, Count: count})
	}
	if count := countTasks(s.arbTasks, examID, func(status string) bool { return status == "pending" || status == "assigned" }); count > 0 {
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
	return issues
}

func appendRosterQualityIssues(issues []QualityIssue, roster RosterReport) []QualityIssue {
	if roster.Summary.Unidentified > 0 {
		issues = append(issues, QualityIssue{Code: "unidentified_submission", Message: "submissions are not uniquely matched to an expected student", Blocking: true, Count: roster.Summary.Unidentified})
	}
	if roster.Summary.MissingPages > 0 {
		issues = append(issues, QualityIssue{Code: "missing_pages_unresolved", Message: "matched submissions have unresolved missing or rejected pages", Blocking: true, Count: roster.Summary.MissingPages})
	}
	return issues
}

func (s *MemoryStore) appendUnconfirmedQualityIssuesLocked(issues []QualityIssue, examID string) []QualityIssue {
	unconfirmed := 0
	for _, grade := range s.submissions {
		if grade.ExamID == examID && grade.Status != "confirmed" && grade.Status != "pending_publish" && grade.Status != "published" && grade.Status != "locked" {
			unconfirmed++
		}
	}
	if unconfirmed > 0 {
		issues = append(issues, QualityIssue{Code: "grades_not_confirmed", Message: "there are grades not ready for publish", Blocking: true, Count: unconfirmed})
	}
	return issues
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

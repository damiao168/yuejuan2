package score

import (
	"context"
	"sort"
)

// ListRoster 与 PostgreSQL 一致：自动准备前提成立时，只差自动汇总的答卷标为 awaiting_aggregation，不计入 unresolved。
func (s *MemoryStore) ListRoster(_ context.Context, _ string, examID string) (RosterReport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	report := s.rosterReportLocked(examID)
	if s.automaticPreparationLocked(examID) {
		s.markAwaitingAggregationLocked(examID, &report)
	}
	return report, nil
}

// markAwaitingAggregationLocked 只用于名册读取；质量检查仍用 rosterReportLocked 的原始结论。
func (s *MemoryStore) markAwaitingAggregationLocked(examID string, report *RosterReport) {
	for index, entry := range report.Entries {
		if entry.Status != "submitted" || entry.ResolutionCode != "grading_incomplete" || !s.awaitingAggregationLocked(examID, entry.SubmissionID) {
			continue
		}
		report.Entries[index].ResolutionCode = "awaiting_aggregation"
		report.Summary.Unresolved--
	}
}

// awaitingAggregationLocked 与 PostgreSQL 名册的 awaiting_submission 条件相同：每个答题区已有终评或会自动生成终评，
// 每道题都有答题区（内存夹具以本考试出现过的题目为题集），且本答卷没有进行中的阅卷或仲裁任务。
func (s *MemoryStore) awaitingAggregationLocked(examID, submissionID string) bool {
	questions, covered := map[string]bool{}, map[string]bool{}
	for _, segment := range s.segments {
		if segment.ExamID != examID {
			continue
		}
		questions[segment.QuestionID] = true
		if segment.SubmissionID != submissionID {
			continue
		}
		_, final := s.finalBySeg[segment.AnswerSegmentID]
		_, _, automatic := s.automaticFinalSeedLocked(segment.AnswerSegmentID)
		if !final && !automatic {
			return false
		}
		covered[segment.QuestionID] = true
	}
	return len(covered) > 0 && len(covered) == len(questions) && !s.hasActiveTaskLocked(examID, submissionID)
}

func (s *MemoryStore) hasActiveTaskLocked(examID, submissionID string) bool {
	matches := func(task TaskSeed) bool {
		return task.ExamID == examID && (task.SubmissionID == "" || task.SubmissionID == submissionID)
	}
	for _, task := range s.reviewTasks {
		if matches(task) && (task.Status == "pending" || task.Status == "assigned" || task.Status == "in_progress" || task.Status == "returned") {
			return true
		}
	}
	for _, task := range s.arbTasks {
		if matches(task) && (task.Status == "pending" || task.Status == "assigned") {
			return true
		}
	}
	return false
}

// SetAttendance 保留内部旧调用的显式失败，防止历史客户端继续写入已退役的人工出勤事实。
func (s *MemoryStore) SetAttendance(context.Context, string, string, string, string, AttendanceInput) (RosterReport, error) {
	return RosterReport{}, ErrManualAttendanceRetired
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
	s.appendRosterStudentEntriesLocked(examID, rosterKeys, submissionCounts, &report)
	s.appendUnidentifiedRosterEntriesLocked(examID, rosterKeys, submissionCounts, &report)
	// 学生未匹配、无法识别答卷和缺页分别计入原有汇总；缺页不在学生条目阶段重复计数。
	report.Summary.Unresolved += report.Summary.Unidentified + report.Summary.MissingPages
	sort.Slice(report.Entries, func(i, j int) bool {
		if report.Entries[i].ClassName == report.Entries[j].ClassName {
			return report.Entries[i].StudentNo < report.Entries[j].StudentNo
		}
		return report.Entries[i].ClassName < report.Entries[j].ClassName
	})
	return report
}

func (s *MemoryStore) appendRosterStudentEntriesLocked(examID string, rosterKeys map[string]RosterSeed, submissionCounts map[string]int, report *RosterReport) {
	for _, seed := range rosterKeys {
		entry := s.rosterStudentEntryLocked(examID, seed, submissionCounts[seed.StudentID])
		switch entry.Status {
		case "absent":
			report.Summary.Absent++
		case "missing_pages":
			report.Summary.MissingPages++
		case "graded":
			report.Summary.Graded++
		case "unmatched":
			fallthrough
		case "submitted":
			report.Summary.Unresolved++
		}
		report.Entries = append(report.Entries, entry)
	}
}

func (s *MemoryStore) rosterStudentEntryLocked(examID string, seed RosterSeed, submissionCount int) RosterEntry {
	entry := RosterEntry{
		Key: "student:" + seed.StudentID, StudentID: seed.StudentID, StudentNo: seed.StudentNo,
		StudentName: seed.StudentName, ClassID: seed.ClassID, ClassName: seed.ClassName,
		Status: "unmatched", ResolutionCode: "duplicate_submission",
	}
	switch {
	case submissionCount == 0:
		// 缺考只看本场冻结名册是否已有答卷，历史人工标记不再改变当前派生结果。
		entry.Status = "absent"
		entry.ResolutionCode = "absent"
	case submissionCount == 1:
		return s.matchedRosterStudentEntryLocked(examID, seed.StudentID, entry)
	case submissionCount > 1:
		entry.ResolutionCode = "duplicate_submission"
	}
	return entry
}

func (s *MemoryStore) matchedRosterStudentEntryLocked(examID, studentID string, entry RosterEntry) RosterEntry {
	for _, submission := range s.rosterSubmissions {
		if submission.ExamID != examID || submission.StudentID != studentID {
			continue
		}
		entry.SubmissionID = submission.SubmissionID
		entry.CandidateNo = submission.CandidateNo
		entry.ExpectedPageCount = submission.ExpectedPages
		entry.ActualPageCount = submission.ActualPages
		// 身份有疑问的已到卷保持待匹配；不能把它当缺考，也不能被旧成绩标成完成。
		if submission.IdentityStatus == "unknown" || submission.IdentityStatus == "conflict" {
			entry.Status = "unmatched"
			entry.ResolutionCode = "student_identity_unresolved"
		} else if submission.ActualPages < submission.ExpectedPages || submission.QualityStatus == "failed" || submission.SubmissionStatus == "rejected" {
			entry.Status = "missing_pages"
			entry.ResolutionCode = "missing_pages"
		} else if grade, ok := s.submissions[key(examID, submission.SubmissionID)]; ok {
			entry.Status = "graded"
			entry.ResolutionCode = "graded"
			total, max := grade.TotalScore, grade.MaxScore
			entry.TotalScore, entry.MaxScore = &total, &max
		} else {
			entry.Status = "submitted"
			entry.ResolutionCode = "grading_incomplete"
		}
		break
	}
	return entry
}

func (s *MemoryStore) appendUnidentifiedRosterEntriesLocked(examID string, rosterKeys map[string]RosterSeed, submissionCounts map[string]int, report *RosterReport) {
	for _, submission := range s.rosterSubmissions {
		if submission.ExamID != examID {
			continue
		}
		rosterSeed, inRoster := rosterKeys[key(examID, submission.StudentID)]
		identityUnresolved := submission.IdentityStatus == "unknown" || submission.IdentityStatus == "conflict"
		if submission.StudentID != "" && inRoster && submissionCounts[submission.StudentID] == 1 && !identityUnresolved {
			continue
		}
		code := "student_not_in_roster"
		switch {
		case submission.StudentID == "":
			code = "student_unidentified"
		case identityUnresolved:
			code = "student_identity_unresolved"
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
}

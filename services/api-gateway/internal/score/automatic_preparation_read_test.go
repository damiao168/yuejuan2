package score

import (
	"errors"
	"testing"
)

// 内存实现与 PostgreSQL 一致：首次草稿前只差自动汇总的答卷不算阅卷未完成；
// 准备做不到的问题、已有正式发布后的读取，以及所有写操作内部检查仍保持严格。
func automaticPreparationMemoryStore() *MemoryStore {
	store := NewMemoryStore()
	for _, student := range []string{"student-1", "student-2"} {
		store.AddRosterStudent(RosterSeed{ExamID: "exam-1", StudentID: student, StudentNo: student, ClassName: "Class 1"})
	}
	store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1", ExpectedPages: 1, ActualPages: 1, QualityStatus: "passed"})
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 6})
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnswerSegmentID: "segment-2", QuestionID: "question-2", QuestionNo: "Q2", MaxScore: 4})
	store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-1", Score: 5, MaxScore: 6})
	store.AddRuleGrade(GradeSeed{AnswerSegmentID: "segment-2", Score: 4, MaxScore: 4, AutoPass: true})
	return store
}

func memoryRosterEntry(t *testing.T, store *MemoryStore, submissionID string) (RosterEntry, RosterSummary) {
	t.Helper()
	report, err := store.ListRoster(t.Context(), tenantID, "exam-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range report.Entries {
		if entry.SubmissionID == submissionID {
			return entry, report.Summary
		}
	}
	t.Fatalf("roster entry for %s missing: %#v", submissionID, report)
	return RosterEntry{}, RosterSummary{}
}

func TestMemoryReadsBeforeFirstDraftTreatAutomaticAggregationAsDone(t *testing.T) {
	store := automaticPreparationMemoryStore()
	entry, summary := memoryRosterEntry(t, store, "submission-1")
	if entry.Status != "submitted" || entry.ResolutionCode != "awaiting_aggregation" || summary.Unresolved != 0 || summary.Absent != 1 {
		t.Fatalf("awaiting aggregation was reported as unfinished: entry=%#v summary=%#v", entry, summary)
	}
	if quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true); err != nil || !quality.Passed {
		t.Fatalf("publish read must pass after automatic preparation: %#v %v", quality, err)
	}
	// confirmation 阶段与写操作不放宽：此时确实还没有终评和总分。
	strict, err := store.CheckQuality(t.Context(), tenantID, "exam-1", false)
	if err != nil || strict.Passed || !hasIssue(strict.Issues, "missing_final_grades") || !hasIssue(strict.Issues, "missing_submission_grades") {
		t.Fatalf("confirmation stage must stay strict: %#v %v", strict, err)
	}
	if _, err := store.PublishGrades(t.Context(), tenantID, "exam-1", "admin", PublishInput{Reason: "legacy"}); !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("legacy publish must stay strict: %v", err)
	}
	if len(store.finals) != 0 || len(store.submissions) != 0 {
		t.Fatalf("read-only checks wrote grades: finals=%d totals=%d", len(store.finals), len(store.submissions))
	}
}

func TestMemoryReadsStillBlockWhatPreparationCannotResolve(t *testing.T) {
	t.Run("question without any grade", func(t *testing.T) {
		store := automaticPreparationMemoryStore()
		store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnswerSegmentID: "segment-3", QuestionID: "question-3", QuestionNo: "Q3", MaxScore: 2})
		entry, summary := memoryRosterEntry(t, store, "submission-1")
		if entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 {
			t.Fatalf("ungraded question hidden from roster: %#v %#v", entry, summary)
		}
		quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
		if err != nil || quality.Passed || !hasIssue(quality.Issues, "missing_final_grades") || !hasIssue(quality.Issues, "question_coverage_incomplete") {
			t.Fatalf("ungraded question must keep blocking: %#v %v", quality, err)
		}
	})
	t.Run("unfinished review task on the submission", func(t *testing.T) {
		store := automaticPreparationMemoryStore()
		store.AddReviewTask(TaskSeed{ExamID: "exam-1", Status: "pending", SubmissionID: "submission-1"})
		entry, summary := memoryRosterEntry(t, store, "submission-1")
		quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
		if entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 || err != nil || !hasIssue(quality.Issues, "unfinished_review_tasks") {
			t.Fatalf("unfinished review must keep blocking: %#v %#v %#v %v", entry, summary, quality, err)
		}
	})
	t.Run("new automatic finals reopen confirmed totals before publication", func(t *testing.T) {
		store := seededScoreStore()
		if _, err := store.FinalizeExam(t.Context(), tenantID, "exam-1", "manager"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ConfirmGrades(t.Context(), tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked"}); err != nil {
			t.Fatal(err)
		}
		// 确认后确有新的待确认终评，首发前可重新汇总；不能把尚未发布的正常续评分数永久卡住。
		store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnswerSegmentID: "segment-3", QuestionID: "question-1", QuestionNo: "Q1b", MaxScore: 2})
		store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-3", Score: 1, MaxScore: 2})
		quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
		if err != nil || !quality.Passed {
			t.Fatalf("new automatic final must be recoverable: %#v %v", quality, err)
		}
	})
	t.Run("confirmed total that disagrees with finals and has no new finals", func(t *testing.T) {
		store := seededScoreStore()
		if _, err := store.FinalizeExam(t.Context(), tenantID, "exam-1", "manager"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ConfirmGrades(t.Context(), tenantID, "exam-1", "manager", ConfirmInput{Reason: "checked"}); err != nil {
			t.Fatal(err)
		}
		store.mu.Lock()
		for id, grade := range store.submissions {
			grade.TotalScore--
			store.submissions[id] = grade
		}
		store.mu.Unlock()
		quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
		if err != nil || quality.Passed || !hasIssue(quality.Issues, "score_integrity_mismatch") {
			t.Fatalf("confirmed total mismatch must keep blocking: %#v %v", quality, err)
		}
	})
}

func TestMemoryReadsAfterFormalReleaseStayStrict(t *testing.T) {
	store := automaticPreparationMemoryStore()
	store.MarkFormalReleaseForTest("exam-1")
	entry, summary := memoryRosterEntry(t, store, "submission-1")
	if entry.ResolutionCode != "grading_incomplete" || summary.Unresolved != 1 {
		t.Fatalf("published exam must keep original roster result: %#v %#v", entry, summary)
	}
	quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
	if err != nil || quality.Passed || !hasIssue(quality.Issues, "missing_final_grades") {
		t.Fatalf("published exam must keep original quality result: %#v %v", quality, err)
	}
}

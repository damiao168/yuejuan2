package score

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const tenantID = "tenant-1"

func TestFinalizeAggregatesSubmissionGradesFromHumanAndRuleGrades(t *testing.T) {
	store := seededScoreStore()
	result, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1")
	if err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	if result.CreatedFinals != 2 || result.Status != "pending_confirmation" {
		t.Fatalf("finalize result mismatch: %#v", result)
	}
	if len(result.SubmissionGrades) != 1 {
		t.Fatalf("expected one submission grade, got %#v", result.SubmissionGrades)
	}
	grade := result.SubmissionGrades[0]
	if grade.TotalScore != 8 || grade.MaxScore != 10 || grade.Status != "pending_confirmation" {
		t.Fatalf("submission grade mismatch: %#v", grade)
	}
	if len(grade.Items) != 2 {
		t.Fatalf("expected two final grade items, got %#v", grade.Items)
	}
	if !containsStatus(result.AvailableStatuses, "calculating") || !containsStatus(result.AvailableStatuses, "locked") {
		t.Fatalf("available statuses missing required states: %#v", result.AvailableStatuses)
	}
}

func TestListExamGradesUsesStableCursorAndAccurateTotals(t *testing.T) {
	store := NewMemoryStore()
	for index, code := range []string{"ANON-001", "ANON-002", "ANON-003"} {
		suffix := string(rune('1' + index))
		store.AddSegment(SegmentSeed{
			ExamID: "exam-page", SubmissionID: "submission-" + suffix, StudentID: "student-" + suffix,
			AnonymousCode: code, AnswerSegmentID: "segment-" + suffix, QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5,
		})
		store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-" + suffix, Score: 4, MaxScore: 5})
	}
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-page", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	first, err := store.ListExamGrades(context.Background(), tenantID, "exam-page", GradeListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if first.Total != 3 || first.FilteredTotal != 3 || len(first.Grades) != 2 || first.AllLocked {
		t.Fatalf("unexpected first page: %#v", first)
	}
	last := first.Grades[len(first.Grades)-1]
	second, err := store.ListExamGrades(context.Background(), tenantID, "exam-page", GradeListFilter{
		Limit: 2, CursorAnonymousCode: last.AnonymousCode, CursorID: last.ID,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(second.Grades) != 1 || second.Grades[0].AnonymousCode != "ANON-003" {
		t.Fatalf("unexpected second page: %#v", second.Grades)
	}
	filtered, err := store.ListExamGrades(context.Background(), tenantID, "exam-page", GradeListFilter{Query: "003", Limit: 2})
	if err != nil || filtered.Total != 3 || filtered.FilteredTotal != 1 || len(filtered.Grades) != 1 {
		t.Fatalf("unexpected filtered page: %#v, err=%v", filtered, err)
	}
}

func TestConfirmationAndQualityRejectUnfinishedReview(t *testing.T) {
	store := seededScoreStore()
	store.AddReviewTask(TaskSeed{ExamID: "exam-1", Status: "assigned"})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	_, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"})
	if !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("confirm should block on unfinished review task, got %v", err)
	}
	quality, err := store.CheckQuality(context.Background(), tenantID, "exam-1", true)
	if err != nil || quality.Passed || !hasIssue(quality.Issues, "unfinished_review_tasks") {
		t.Fatalf("quality read lost unfinished review blocker: %#v %v", quality, err)
	}
}

// 发布前读取按“创建草稿自动准备之后”判断：待确认总分会在准备时确认，所以不阻断；
// 旧发布入口现在也委托正式发布器；此处只验证工作表的只读预检，不重演已移除的发布算法。
func TestCheckQualityReadOnlyForPublishGate(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	quality, err := store.CheckQuality(context.Background(), tenantID, "exam-1", true)
	if err != nil {
		t.Fatalf("check quality: %v", err)
	}
	if !quality.Passed {
		t.Fatalf("publish read should treat pending totals as confirmed by automatic preparation: %#v", quality)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	quality, err = store.CheckQuality(context.Background(), tenantID, "exam-1", true)
	if err != nil {
		t.Fatalf("check quality after confirm: %v", err)
	}
	if !quality.Passed {
		t.Fatalf("publish quality should pass after confirmation: %#v", quality)
	}
}

func TestRosterReconciliationDerivesAbsenceAndBlocksUnknownSubmissions(t *testing.T) {
	store := NewMemoryStore()
	for _, student := range []RosterSeed{
		{ExamID: "exam-roster", StudentID: "student-1", StudentNo: "001", StudentName: "Alice", ClassID: "class-1", ClassName: "Class 1"},
		{ExamID: "exam-roster", StudentID: "student-2", StudentNo: "002", StudentName: "Bob", ClassID: "class-1", ClassName: "Class 1"},
	} {
		store.AddRosterStudent(student)
	}
	store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam-roster", SubmissionID: "submission-1", StudentID: "student-1", CandidateNo: "001", ExpectedPages: 1, ActualPages: 1, QualityStatus: "passed"})
	store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam-roster", SubmissionID: "submission-unknown", CandidateNo: "UNKNOWN", ExpectedPages: 1, ActualPages: 1, QualityStatus: "passed"})
	store.AddSegment(SegmentSeed{ExamID: "exam-roster", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 10})
	store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-1", Score: 8, MaxScore: 10})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-roster", "manager-1"); err != nil {
		t.Fatalf("finalize roster exam: %v", err)
	}

	report, err := store.ListRoster(context.Background(), tenantID, "exam-roster")
	if err != nil {
		t.Fatalf("list roster: %v", err)
	}
	if report.Summary.Expected != 2 || report.Summary.Received != 2 || report.Summary.Graded != 1 || report.Summary.Unidentified != 1 || report.Summary.Absent != 1 {
		t.Fatalf("unexpected roster summary: %#v", report.Summary)
	}
	if _, err := store.SetAttendance(context.Background(), tenantID, "exam-roster", "student-2", "manager-1", AttendanceInput{Status: "absent", Reason: "old client"}); !errors.Is(err, ErrManualAttendanceRetired) {
		t.Fatalf("manual attendance must be retired, got %v", err)
	}
	quality, err := store.CheckQuality(context.Background(), tenantID, "exam-roster", true)
	if err != nil {
		t.Fatalf("quality check: %v", err)
	}
	if quality.Passed || !hasIssue(quality.Issues, "unidentified_submission") || hasIssue(quality.Issues, "missing_submission_unresolved") {
		t.Fatalf("absence should resolve the missing student but not the unidentified answer sheet: %#v", quality)
	}
}

func TestConfirmPublishStudentLookupAndCSVExport(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	confirmed, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"})
	if err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	if confirmed[0].Status != "confirmed" || confirmed[0].ConfirmedBy != "leader-1" {
		t.Fatalf("confirmed grade mismatch: %#v", confirmed[0])
	}
	_, releases, _ := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4, 4))
	published, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"})
	if err != nil {
		t.Fatalf("publish grades: %v", err)
	}
	if published.Status != "published" || !published.SubmissionGrades[0].Locked {
		t.Fatalf("published grade mismatch: %#v", published)
	}
	if current, err := releases.CurrentPublished(context.Background(), tenantID, "exam-1"); err != nil || published.ScoreRelease == nil || current.Release.ID != published.ScoreRelease.ID {
		t.Fatalf("publication did not expose the same frozen version: %+v %v", current, err)
	}
	quality, err := store.CheckQuality(context.Background(), tenantID, "exam-1", true)
	if err != nil {
		t.Fatalf("check quality after publish: %v", err)
	}
	if !quality.Passed {
		t.Fatalf("published grades must remain quality-passed: %#v", quality)
	}
	studentGrade, err := store.GetStudentGrade(context.Background(), tenantID, "student-1", "exam-1")
	if err != nil {
		t.Fatalf("student lookup: %v", err)
	}
	if studentGrade.TotalScore != 8 || studentGrade.Status != "published" || len(studentGrade.Items) != 2 {
		t.Fatalf("student grade mismatch: %#v", studentGrade)
	}
	export, err := store.ExportGradesCSV(context.Background(), tenantID, "exam-1", "admin-1")
	if err != nil {
		t.Fatalf("export grades: %v", err)
	}
	text := string(export.Content)
	for _, expected := range []string{"watermark", "exported_by", "admin-1", "EduGrade export", "submission-1"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("CSV missing %s: %s", expected, text)
		}
	}
}

func TestGradeStateTransitionsCannotMoveBackwardOrRepeat(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("finalize after confirmation must be rejected, got %v", err)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "repeat"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeated confirmation must be rejected, got %v", err)
	}
	_, releases, _ := attachScorePublication(store, "exam-1", scorePublicationFact("submission-1", "student-1", 4, 4))
	first, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"})
	if err != nil {
		t.Fatalf("publish grades: %v", err)
	}
	if replay, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "repeat"}); err != nil || replay.ScoreRelease == nil || replay.ScoreRelease.ID != first.ScoreRelease.ID {
		t.Fatalf("repeated legacy publish must retain the current version: %+v %v", replay, err)
	}
	if versions, err := releases.List(context.Background(), tenantID, "exam-1"); err != nil || len(versions) != 1 {
		t.Fatalf("repeated publish added a version: %+v %v", versions, err)
	}
}

func TestUnconfiguredPublishPreservesConfirmedWorkingGrades(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	if _, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"}); !errors.Is(err, ErrPublicationUnavailable) {
		t.Fatalf("publish without canonical assembly must fail closed, got %v", err)
	}
	result, err := store.ListExamGrades(context.Background(), tenantID, "exam-1", GradeListFilter{})
	if err != nil {
		t.Fatalf("list grades: %v", err)
	}
	grades := result.Grades
	if len(grades) != 1 || grades[0].Status != "confirmed" || grades[0].Locked {
		t.Fatalf("failed publish must preserve confirmed unlocked grades: %#v", grades)
	}
}

func TestQualityReportsArbitrationOCRAndMissingFinalGrade(t *testing.T) {
	store := NewMemoryStore()
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5})
	store.AddArbitrationTask(TaskSeed{ExamID: "exam-1", Status: "pending"})
	store.AddOCRTask(TaskSeed{ExamID: "exam-1", Status: "failed"})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	quality, err := store.CheckQuality(context.Background(), tenantID, "exam-1", true)
	if err != nil || quality.Passed {
		t.Fatalf("quality should report blockers, got %#v %v", quality, err)
	}
	codes := map[string]bool{}
	for _, issue := range quality.Issues {
		codes[issue.Code] = true
	}
	for _, expected := range []string{"unfinished_arbitration_tasks", "ocr_failed_unhandled", "missing_final_grades"} {
		if !codes[expected] {
			t.Fatalf("missing quality issue %s in %#v", expected, quality.Issues)
		}
	}
}

func seededScoreStore() *MemoryStore {
	store := NewMemoryStore()
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5})
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-2", QuestionID: "question-2", QuestionNo: "Q2", MaxScore: 5})
	store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-1", Score: 4, MaxScore: 5})
	store.AddRuleGrade(GradeSeed{AnswerSegmentID: "segment-2", Score: 4, MaxScore: 5, AutoPass: true, NeedsReview: false, Mock: false})
	return store
}

func containsStatus(statuses []string, target string) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}

func hasIssue(issues []QualityIssue, target string) bool {
	for _, issue := range issues {
		if issue.Code == target {
			return true
		}
	}
	return false
}

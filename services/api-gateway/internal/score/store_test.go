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

func TestPublishBlocksUntilGradesConfirmedAndQualityPasses(t *testing.T) {
	store := seededScoreStore()
	store.AddReviewTask(TaskSeed{ExamID: "exam-1", Status: "assigned"})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	_, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"})
	if !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("confirm should block on unfinished review task, got %v", err)
	}
	result, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "publish"})
	if !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("publish should block before confirmation and unfinished review, got %v", err)
	}
	if result.Quality.Passed || len(result.Quality.Issues) == 0 {
		t.Fatalf("publish should return blocking quality issues, got %#v", result)
	}
}

// 发布前读取按“创建草稿自动准备之后”判断：待确认总分会在准备时确认，所以不阻断；
// 旧版发布接口没有自动准备，仍必须拒绝未确认成绩。
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
	if result, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "too early"}); !errors.Is(err, ErrQualityGateFailed) || !hasIssue(result.Quality.Issues, "grades_not_confirmed") {
		t.Fatalf("legacy publish must still require confirmation: %#v %v", result, err)
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
	published, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"})
	if err != nil {
		t.Fatalf("publish grades: %v", err)
	}
	if published.Status != "published" || !published.SubmissionGrades[0].Locked {
		t.Fatalf("published grade mismatch: %#v", published)
	}
	if status := store.ExamStatusForTest("exam-1"); status != "published" {
		t.Fatalf("exam status after publish = %q, want published", status)
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
	if _, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"}); err != nil {
		t.Fatalf("publish grades: %v", err)
	}
	if _, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "repeat"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("repeated publish must be rejected, got %v", err)
	}
}

func TestPublishRejectsInvalidExamStateWithoutLockingGrades(t *testing.T) {
	store := seededScoreStore()
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	store.SetExamStatusForTest("exam-1", "archived")
	if _, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("publish from archived exam must be rejected, got %v", err)
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

func TestExamStatesEligibleForQualityGatedPublish(t *testing.T) {
	for _, status := range []string{"draft", "configured", "ready", "collecting", "grading", "reviewing", "finalized"} {
		if !canPublishExamStatus(status) {
			t.Errorf("active exam status %q should be eligible for quality-gated publish", status)
		}
	}
	for _, status := range []string{"", "published", "archived"} {
		if canPublishExamStatus(status) {
			t.Errorf("terminal or unknown exam status %q must not be publishable", status)
		}
	}
}

func TestPublishBlocksForArbitrationOCRAndMissingFinalGrade(t *testing.T) {
	store := NewMemoryStore()
	store.AddSegment(SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5})
	store.AddArbitrationTask(TaskSeed{ExamID: "exam-1", Status: "pending"})
	store.AddOCRTask(TaskSeed{ExamID: "exam-1", Status: "failed"})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	result, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{})
	if !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("publish should fail quality gate, got %v", err)
	}
	codes := map[string]bool{}
	for _, issue := range result.Quality.Issues {
		codes[issue.Code] = true
	}
	for _, expected := range []string{"unfinished_arbitration_tasks", "ocr_failed_unhandled", "missing_final_grades"} {
		if !codes[expected] {
			t.Fatalf("missing quality issue %s in %#v", expected, result.Quality.Issues)
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

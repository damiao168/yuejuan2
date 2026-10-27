package score

import (
	"errors"
	"testing"
)

func TestMemoryPublishBlocksMatchedSubmissionWithoutAnyGrades(t *testing.T) {
	store := seededScoreStore()
	for _, student := range []string{"student-1", "student-2"} {
		store.AddRosterStudent(RosterSeed{ExamID: "exam-1", StudentID: student})
		store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam-1", StudentID: student, SubmissionID: "submission-" + student[len(student)-1:], QualityStatus: "passed"})
	}
	if _, err := store.FinalizeExam(t.Context(), tenantID, "exam-1", "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmGrades(t.Context(), tenantID, "exam-1", "manager", ConfirmInput{Reason: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	result, err := store.PublishGrades(t.Context(), tenantID, "exam-1", "manager", PublishInput{Reason: "synthetic"})
	if !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("ungraded submission was published: %#v %v", result, err)
	}
	codes := map[string]int{}
	for _, issue := range result.Quality.Issues {
		codes[issue.Code] = issue.Count
	}
	if codes["missing_submission_grades"] != 1 || codes["question_coverage_incomplete"] != 1 {
		t.Fatalf("missing submission was not fully accounted for: %#v", codes)
	}
}

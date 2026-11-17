package score

import (
	"errors"
	"testing"
)

func TestMemoryConfirmationAndQualityBlockMatchedSubmissionWithoutAnyGrades(t *testing.T) {
	store := seededScoreStore()
	for _, student := range []string{"student-1", "student-2"} {
		store.AddRosterStudent(RosterSeed{ExamID: "exam-1", StudentID: student})
		store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam-1", StudentID: student, SubmissionID: "submission-" + student[len(student)-1:], QualityStatus: "passed"})
	}
	if _, err := store.FinalizeExam(t.Context(), tenantID, "exam-1", "manager"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfirmGrades(t.Context(), tenantID, "exam-1", "manager", ConfirmInput{Reason: "synthetic"}); !errors.Is(err, ErrQualityGateFailed) {
		t.Fatalf("ungraded submission allowed confirmation: %v", err)
	}
	grades, err := store.ListExamGrades(t.Context(), tenantID, "exam-1", GradeListFilter{})
	if err != nil || len(grades.Grades) != 1 || grades.Grades[0].Status != "pending_confirmation" {
		t.Fatalf("blocked confirmation changed existing grades: %#v %v", grades, err)
	}
	quality, err := store.CheckQuality(t.Context(), tenantID, "exam-1", true)
	if err != nil || quality.Passed {
		t.Fatalf("ungraded submission disappeared from pre-publication quality: %#v %v", quality, err)
	}
	codes := map[string]int{}
	for _, issue := range quality.Issues {
		codes[issue.Code] = issue.Count
	}
	if codes["missing_submission_grades"] != 1 || codes["question_coverage_incomplete"] != 1 {
		t.Fatalf("missing submission was not fully accounted for: %#v", codes)
	}
}

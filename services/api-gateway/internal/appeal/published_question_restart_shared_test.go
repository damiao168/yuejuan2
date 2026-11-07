package appeal

import (
	"errors"
	"reflect"
	"testing"
)

func TestRestartRefusesSharedRegradeWithoutChangingOtherStudentsWork(t *testing.T) {
	store, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	store.SeedRegradeJob("shared-job", item.TenantID, item.ExamID, item.QuestionID, item.SourceReleaseID,
		PublishedQuestionAppealRegradeItemSource{SubmissionID: item.SubmissionID, OldFinalGradeID: item.SourceFinalGradeID, Status: "resolved"},
		PublishedQuestionAppealRegradeItemSource{SubmissionID: "another-student-paper", OldFinalGradeID: "another-grade", Status: "resolved"})
	item, err := service.Decide(t.Context(), item.TenantID, item.ID, "manager", DecideQuestionAppealInput{Decision: QuestionAppealDecisionReferRegrade, RegradeJobID: "shared-job", PublicResponse: "pending shared regrade", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	current := publishedQuestionWorkflowSource()
	current.ReleaseID, current.ReleaseVersion, current.Score = "new-current", 2, 3
	store.SeedSource(current)
	before := store.regradeJobs["shared-job"]
	if _, err := service.RestartReview(t.Context(), item.TenantID, item.ID, "manager", RestartQuestionAppealReviewInput{Reason: "one appeal must not cancel other students", ExpectedRevision: item.Revision}); !errors.Is(err, ErrRestartReviewSharedJob) {
		t.Fatalf("shared job was not explicitly rejected: %v", err)
	}
	if !reflect.DeepEqual(before, store.regradeJobs["shared-job"]) {
		t.Fatal("rejected restart changed another student's work")
	}
	actual, err := service.Get(t.Context(), item.TenantID, item.ID)
	if err != nil || actual.Revision != item.Revision || actual.Status != item.Status {
		t.Fatalf("rejected restart changed appeal: %+v %v", actual, err)
	}
}

package apicontract

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestQuestionAppealReviewOpinionHandlerResponseValidatesAgainstOpenAPI(t *testing.T) {
	store := appeal.NewPublishedQuestionAppealMemoryStore()
	store.SeedSource(appeal.PublishedQuestionAppealSource{
		TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		AllowedReasonCodes: []string{appeal.ReasonMissingStepCredit}, QuestionID: "question-4", QuestionNo: "Q4",
		FinalGradeID: "original-grade", Score: 4, MaxScore: 8, ReviewerIDs: []string{"original-reviewer"},
	})
	service := appeal.NewPublishedQuestionAppealService(store)
	item, err := service.Create(context.Background(), "tenant-1", "student-1", "student-user", appeal.CreatePublishedQuestionAppealInput{
		ExamID: "exam-1", SourceReleaseID: "release-1", QuestionID: "question-4", ReasonCode: appeal.ReasonMissingStepCredit, Reason: "请核对第二步过程分",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.StartReview(context.Background(), "tenant-1", item.ID, "manager", appeal.StartQuestionAppealReviewInput{AssignedTo: "independent-reviewer", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	user := auth.User{ID: "independent-reviewer", TenantID: "tenant-1", Permissions: []string{"appeal:read", "appeal:work"}}
	request := httptest.NewRequest(http.MethodPost, "/opinion", strings.NewReader(fmt.Sprintf(`{"opinion":"support_adjustment","suggested_score":6,"explanation":"第二步满足过程分条件，建议管理员复评","expected_revision":%d}`, item.Revision)))
	request = request.WithContext(auth.WithUser(request.Context(), user))
	request.SetPathValue("id", item.ID)
	response := httptest.NewRecorder()
	appeal.NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore()).SubmitReviewOpinion(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("opinion: %d %s", response.Code, response.Body.String())
	}
	validateResponseBody(t, readOpenAPIDocument(t), "/api/v1/question-appeals/{id}/review-opinion", "post", "200", response.Body.Bytes())
	var result struct {
		Appeal appeal.PublishedQuestionAppeal `json:"appeal"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// 教师的支持意见仅完成交接；不能让成功回执看起来已经调分或结案。
	if result.Appeal.Status != appeal.QuestionAppealUnderReview || result.Appeal.SourceScore != 4 || result.Appeal.ReviewOpinion == nil || result.Appeal.RegradeJobID != "" || result.Appeal.Decision != "" {
		t.Fatalf("opinion changed grading or lost handoff: %#v", result.Appeal)
	}
}

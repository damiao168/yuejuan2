package apicontract

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type scoreCommandContractStore struct{ score.Store }

func (s *scoreCommandContractStore) ConfirmGrades(context.Context, string, string, string, score.ConfirmInput) ([]score.SubmissionGrade, error) {
	return []score.SubmissionGrade{{ID: "grade-1", Status: "confirmed"}}, nil
}
func (s *scoreCommandContractStore) PublishGrades(context.Context, string, string, string, score.PublishInput) (score.PublishResult, error) {
	return score.PublishResult{Status: "published", SubmissionGrades: []score.SubmissionGrade{}, Quality: score.QualityReport{Passed: true, Issues: []score.QualityIssue{}}}, nil
}

func TestBusinessCommandHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	doc := readOpenAPIDocument(t)
	store := review.NewMemoryStore()
	store.AddContext("tenant-1", "segment-1", review.Context{ExamID: "exam-1", AnswerSegmentID: "segment-1", Question: paper.Question{ID: "q1", Score: 5}})
	task, err := store.CreateTask(context.Background(), "tenant-1", "actor-1", review.CreateTaskInput{AnswerSegmentID: "segment-1", AssignedTo: "actor-1", Source: "manual_sample"})
	if err != nil {
		t.Fatal(err)
	}
	handler := review.NewHandler(store, auth.NewMemoryStore(), nil, nil)
	req := scopedContractRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"score":4,"expected_revision":1}`)))
	req.SetPathValue("id", task.ID)
	req.Header.Set("Idempotency-Key", "review-contract")
	rec := httptest.NewRecorder()
	handler.SubmitGrade(rec, req)
	if rec.Code != 201 {
		t.Fatalf("submit: %d %s", rec.Code, rec.Body)
	}
	validateResponseBody(t, doc, "/api/v1/review-tasks/{id}/submit", "post", "201", rec.Body.Bytes())
	req = scopedContractRequest(httptest.NewRequest(http.MethodGet, "/", nil))
	req.SetPathValue("commandId", "review-contract")
	rec = httptest.NewRecorder()
	handler.RecoverCommand(rec, req)
	if rec.Code != 200 {
		t.Fatalf("recover: %d %s", rec.Code, rec.Body)
	}
	validateResponseBody(t, doc, "/api/v1/review-commands/{commandId}", "get", "200", rec.Body.Bytes())
	// 还要校验操作专属结果；通用响应封装不足以覆盖该约束。
	receipt, err := store.RecoverCommand(commandreceipt.WithID(context.Background(), "review-contract"), "tenant-1", "actor-1", "review-contract")
	if err != nil {
		t.Fatal(err)
	}
	validateResponseBody(t, doc, "/api/v1/review-tasks/{id}/submit", "post", "201", receipt.Result)
	scores := score.NewHandler(&scoreCommandContractStore{}, auth.NewMemoryStore())
	for _, item := range []struct {
		path    string
		handler http.HandlerFunc
	}{{"/api/v1/exams/{examId}/confirm-grades", scores.ConfirmGrades}, {"/api/v1/exams/{examId}/publish", scores.PublishGrades}} {
		req := scopedContractRequest(httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"reason":"checked"}`)))
		req.SetPathValue("examId", "exam-1")
		rec := httptest.NewRecorder()
		item.handler(rec, req)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", item.path, rec.Code, rec.Body)
		}
		validateResponseBody(t, doc, item.path, "post", "200", rec.Body.Bytes())
	}
}

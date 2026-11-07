package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"github.com/google/uuid"
)

type seedIssueShapeStub struct {
	seedquality.ReviewHook
	questionNo string
	called     bool
}

func (h *seedIssueShapeStub) MaybeIssue(_ context.Context, _, examID, questionID, questionNo, graderID string) (seedquality.Task, bool, error) {
	h.called, h.questionNo = true, questionNo
	return seedquality.Task{ID: uuid.NewString(), ExamID: examID, QuestionID: questionID,
		QuestionNo: questionNo, Source: "manual_sample", Status: "in_progress", AssignedTo: graderID}, true, nil
}

func TestGraderHTTPViewsDoNotExposeCandidateNumberAsSeedSignal(t *testing.T) {
	store := NewMemoryStore()
	segmentID := uuid.NewString()
	value := reviewContext()
	value.AnonymousCode = "20260123"
	value.Question.QuestionNo = "12(2)"
	value.AssessmentSnapshot = taskContextSnapshot(assessment.RiskR2, assessment.ScoringAIAssist)
	store.AddContext(tenantID, segmentID, value)
	task, err := store.CreateTask(t.Context(), tenantID, "manager", CreateTaskInput{
		AnswerSegmentID: segmentID, Source: "manual_sample", AssignedTo: "grader",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /tasks", handler.ListTasks)
	mux.HandleFunc("GET /tasks/{id}", handler.GetTask)
	mux.HandleFunc("GET /tasks/{id}/workspace", handler.GetWorkspace)
	mux.HandleFunc("GET /tasks/{id}/context", handler.GetTaskContext)
	mux.HandleFunc("POST /tasks/next", handler.ClaimNextTask)
	claimBody := `{"exam_id":"` + task.ExamID + `","question_id":"` + task.QuestionID + `"}`
	var ordinary ReviewTask
	for _, target := range []struct{ method, path, body string }{
		{http.MethodGet, "/tasks", ""},
		{http.MethodGet, "/tasks/" + task.ID, ""},
		{http.MethodGet, "/tasks/" + task.ID + "/workspace", ""},
		{http.MethodGet, "/tasks/" + task.ID + "/context", ""},
		{http.MethodPost, "/tasks/next", claimBody},
	} {
		t.Run(target.path, func(t *testing.T) {
			req := httptest.NewRequest(target.method, target.path, strings.NewReader(target.body))
			req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "grader", TenantID: tenantID, Roles: []string{"grader"}}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), value.AnonymousCode) {
				t.Fatalf("candidate identity leaked or task failed: %d %s", rec.Code, rec.Body.String())
			}
			if target.method == http.MethodPost {
				var response struct{ Task ReviewTask }
				if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				ordinary = response.Task
			}
		})
	}
	// 比较真实普通领取与同一路由的盲投领取，不以测试中手写的状态常量代替普通任务行为。
	hook := &seedIssueShapeStub{}
	handler.WithSeedHook(hook)
	req := httptest.NewRequest(http.MethodPost, "/tasks/next", strings.NewReader(claimBody))
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "grader", TenantID: tenantID, Roles: []string{"grader"}}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var seeded struct{ Task ReviewTask }
	if err := json.Unmarshal(rec.Body.Bytes(), &seeded); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || !hook.called || hook.questionNo != value.Question.QuestionNo {
		t.Fatalf("seed did not use ordinary question number: called=%t no=%q response=%s", hook.called, hook.questionNo, rec.Body.String())
	}
	if seeded.Task.GradeRound != ordinary.GradeRound || seeded.Task.Status != ordinary.Status || seeded.Task.Source != ordinary.Source || seeded.Task.QuestionNo != ordinary.QuestionNo {
		t.Fatalf("seed metadata differs: ordinary=%+v seed=%+v", ordinary, seeded.Task)
	}
	for _, code := range []string{ordinary.AnonymousCode, seeded.Task.AnonymousCode} {
		parsed, err := uuid.Parse(code)
		if err != nil || parsed.Version() != 4 {
			t.Fatalf("task code differs from ordinary UUID shape: %q", code)
		}
	}
	stored, err := store.GetTask(t.Context(), tenantID, task.ID)
	if err != nil || stored.AnonymousCode != value.AnonymousCode {
		t.Fatalf("response projection changed stored identity: %+v %v", stored, err)
	}
}

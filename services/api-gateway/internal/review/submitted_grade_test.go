package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestSubmittedTaskContextReadsOwnFormalGradeAfterDraftIsGone(t *testing.T) {
	store, task := submissionQualificationFixture(t, assessment.RiskR2)
	submitted, err := store.SubmitGrade(context.Background(), tenantID, task.ID, "reviewer-1", SubmitGradeInput{AutoSubmit: true, Score: 0, ExpectedRevision: task.Revision, Comments: "本人正式评语"})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil, nil, nil)
	read := func(user auth.User) (TaskContext, int) {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+task.ID+"/context", nil)
		request.SetPathValue("taskId", task.ID)
		request = request.WithContext(auth.WithUser(request.Context(), user))
		response := httptest.NewRecorder()
		handler.GetTaskContext(response, request)
		var body struct {
			Context TaskContext `json:"context"`
		}
		if response.Code == http.StatusOK {
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		return body.Context, response.Code
	}
	result, status := read(auth.User{ID: "reviewer-1", TenantID: tenantID, Roles: []string{"grader"}})
	if status != http.StatusOK || result.Draft != nil || result.HumanGrade == nil || result.HumanGrade.ID != submitted.Grade.ID || result.HumanGrade.Score != 0 || result.HumanGrade.Comments != "本人正式评语" {
		t.Fatalf("reopening submitted answer lost the formal score: status=%d context=%#v", status, result)
	}
	manager, status := read(auth.User{ID: "manager-1", TenantID: tenantID, Roles: []string{"school_admin"}, Permissions: []string{"review:manage"}})
	if status != http.StatusOK || manager.HumanGrade != nil {
		t.Fatalf("manager received another reviewer's private score: status=%d context=%#v", status, manager)
	}
}

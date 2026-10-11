package appeal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type questionAppealSummaryStub struct {
	calls [][2]string
	err   error
}

func (reader *questionAppealSummaryStub) ReadQuestionAppealExamSummary(_ context.Context, tenantID, examID string) (QuestionAppealExamSummary, error) {
	reader.calls = append(reader.calls, [2]string{tenantID, examID})
	return QuestionAppealExamSummary{Name: "高一第一次月考", Subject: "english"}, reader.err
}

func questionAppealSummaryRequest(item PublishedQuestionAppeal, actor, tenantID string, permissions []string, body string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/question-appeals/"+item.ID, strings.NewReader(body))
	request.SetPathValue("id", item.ID)
	return request.WithContext(auth.WithUser(request.Context(), auth.User{ID: actor, TenantID: tenantID, Permissions: permissions}))
}

func TestAssignedQuestionAppealIncludesExamSummaryWithoutExamManagement(t *testing.T) {
	for _, route := range []string{"list", "get", "context", "opinion"} {
		t.Run(route, func(t *testing.T) {
			assertAssignedQuestionAppealSummary(t, route)
		})
	}
}

func TestQuestionAppealExamSummaryNeverReadsUnassignedOrOtherTenantTask(t *testing.T) {
	for _, route := range []string{"list", "get", "context"} {
		for _, otherTenant := range []bool{false, true} {
			t.Run(route+"-"+map[bool]string{false: "unassigned", true: "other-tenant"}[otherTenant], func(t *testing.T) {
				assertUnassignedQuestionAppealNeverReadsSummary(t, route, otherTenant)
			})
		}
	}
}

func TestQuestionAppealExamSummaryFailureKeepsEvidenceAndRereadsOnNextRequest(t *testing.T) {
	_, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	reader := &questionAppealSummaryStub{err: errors.New("exam reader unavailable")}
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore()).WithExamSummaryReader(reader)
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		handler.Context(response, questionAppealSummaryRequest(item, item.AssignedTo, item.TenantID, []string{appealWorkPermission}, ""))
		var result struct {
			Context PublishedQuestionAppealContext `json:"context"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Context.Appeal.SourceScore != item.SourceScore {
			t.Fatalf("summary error hid evidence: %d %s", response.Code, response.Body)
		}
		if attempt == 0 && result.Context.Appeal.ExamName != "" {
			t.Fatalf("failed summary presented as confirmed: %+v", result.Context.Appeal)
		}
		if attempt == 1 && result.Context.Appeal.ExamName != "高一第一次月考" {
			t.Fatalf("reread did not recover summary: %+v", result.Context.Appeal)
		}
		reader.err = nil
	}
	if len(reader.calls) != 2 {
		t.Fatalf("failure persisted across requests: %v", reader.calls)
	}
}

func TestStudentQuestionAppealDoesNotFetchStaffExamSummary(t *testing.T) {
	_, service, item := publishedQuestionWorkflowFixture(t)
	reader := &questionAppealSummaryStub{}
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore()).WithExamSummaryReader(reader)
	student := auth.User{ID: "student-user-1", TenantID: item.TenantID, Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": item.StudentID}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/question-appeals", nil)
	request = request.WithContext(auth.WithUser(request.Context(), student))
	response := httptest.NewRecorder()
	handler.List(response, request)
	if response.Code != http.StatusOK || len(reader.calls) != 0 || strings.Contains(response.Body.String(), "exam_name") {
		t.Fatalf("student triggered staff projection: %d %v %s", response.Code, reader.calls, response.Body)
	}
}

func assertAssignedQuestionAppealSummary(t *testing.T, route string) {
	t.Helper()
	_, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	reader := &questionAppealSummaryStub{}
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore()).WithExamSummaryReader(reader)
	request := questionAppealSummaryRequest(item, item.AssignedTo, item.TenantID, []string{appealWorkPermission}, `{"opinion":"needs_evidence","explanation":"请补充原卷","expected_revision":2}`)
	response := httptest.NewRecorder()
	switch route {
	case "list":
		handler.List(response, request)
	case "get":
		handler.Get(response, request)
	case "context":
		handler.Context(response, request)
	case "opinion":
		handler.SubmitReviewOpinion(response, request)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("response %d: %s", response.Code, response.Body)
	}
	var result struct {
		Appeal  PublishedQuestionAppeal        `json:"appeal"`
		Appeals []PublishedQuestionAppeal      `json:"appeals"`
		Context PublishedQuestionAppealContext `json:"context"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	actual := result.Appeal
	if route == "list" {
		if len(result.Appeals) != 1 {
			t.Fatalf("unexpected assigned queue: %+v", result.Appeals)
		}
		actual = result.Appeals[0]
	} else if route == "context" {
		actual = result.Context.Appeal
	}
	if actual.ExamName != "高一第一次月考" || actual.ExamSubject != "english" || actual.SourceScore != item.SourceScore || actual.SourceReleaseVersion != item.SourceReleaseVersion {
		t.Fatalf("missing summary or changed frozen source: %+v", actual)
	}
	if len(reader.calls) != 1 || reader.calls[0] != [2]string{item.TenantID, item.ExamID} {
		t.Fatalf("reader escaped authorized task: %v", reader.calls)
	}
	for _, secret := range []string{item.StudentID, item.SubmissionID, item.CreatedBy, "manager-1"} {
		if secret != "" && strings.Contains(response.Body.String(), secret) {
			t.Fatalf("worker identity leak %q: %s", secret, response.Body)
		}
	}
}

func assertUnassignedQuestionAppealNeverReadsSummary(t *testing.T, route string, otherTenant bool) {
	t.Helper()
	_, service, item := publishedQuestionWorkflowFixture(t)
	item = startPublishedQuestionWorkflowReview(t, service, item)
	actor, tenantID := "unassigned-teacher", item.TenantID
	if otherTenant {
		actor, tenantID = item.AssignedTo, "tenant-other"
	}
	reader := &questionAppealSummaryStub{}
	handler := NewPublishedQuestionAppealHandler(service, auth.NewMemoryStore()).WithExamSummaryReader(reader)
	request := questionAppealSummaryRequest(item, actor, tenantID, []string{appealWorkPermission}, "")
	response := httptest.NewRecorder()
	switch route {
	case "list":
		handler.List(response, request)
	case "get":
		handler.Get(response, request)
	case "context":
		handler.Context(response, request)
	}
	if route == "list" {
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"appeals":[]`) {
			t.Fatalf("unauthorized list: %d %s", response.Code, response.Body)
		}
	} else if response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
		t.Fatalf("unauthorized detail: %d %s", response.Code, response.Body)
	}
	if len(reader.calls) != 0 || strings.Contains(response.Body.String(), "高一第一次月考") {
		t.Fatalf("unauthorized summary lookup: %v %s", reader.calls, response.Body)
	}
}

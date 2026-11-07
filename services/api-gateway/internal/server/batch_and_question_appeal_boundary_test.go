package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
)

func TestSubjectiveBatchBodyCannotTargetForeignSchool(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	actor := auth.UserWithPassword{User: auth.User{
		ID: "admin", TenantID: "tenant", Username: "admin", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"grading:manage"},
		DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}},
	}, SchoolID: "school-a", SecurityEpoch: 1}
	backendRegressionSession(t, identities, actor, "batch-session")
	store := stores.Grading.Subjective.(*subjective.MemoryStore)
	for _, school := range []string{"a", "b"} {
		id := "segment-" + school
		identities.AddResourceBoundary("answer_segment", id, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-" + school, ExamID: "exam-" + school})
		store.AddContext("tenant", id, subjective.ContextForTest("short_answer", 10, "合成作答", nil))
	}
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	for _, tc := range []struct {
		name, ids string
		want      int
	}{
		{"managed", `["segment-a"]`, http.StatusCreated},
		{"foreign", `["segment-b"]`, http.StatusForbidden},
		{"mixed", `["segment-a","segment-b"]`, http.StatusForbidden},
		{"missing", `["missing"]`, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/subjective-grading-batches", strings.NewReader(fmt.Sprintf(`{"idempotency_key":%q,"segment_ids":%s}`, tc.name, tc.ids)))
			req.Header.Set("Authorization", "Bearer batch-session")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("batch=%d want=%d: %s", rec.Code, tc.want, rec.Body.String())
			}
			recovered, err := store.RecoverBatchCommand(t.Context(), "tenant", "admin", tc.name)
			if err != nil || (recovered.Batch != nil) != (tc.want == http.StatusCreated) {
				t.Fatalf("unauthorized batch persisted: %+v %v", recovered, err)
			}
		})
	}
}

func TestAssignedQuestionAppealTeacherNeedsNoExamOrClassScope(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	for _, id := range []string{"teacher", "other"} {
		backendRegressionSession(t, identities, auth.UserWithPassword{User: auth.User{
			ID: id, TenantID: "tenant", Username: id, Status: "active", Roles: []string{"teacher"}, Permissions: []string{"appeal:work"},
			DataScope: map[string]any{"teacher": map[string]any{"scope": "exam_task"}},
		}, SchoolID: "school", SecurityEpoch: 1}, id+"-session")
	}
	store := stores.Release.PublishedQuestionAppeal.(*appeal.PublishedQuestionAppealMemoryStore)
	store.SeedSource(appeal.PublishedQuestionAppealSource{TenantID: "tenant", ExamID: "exam", StudentID: "student", SubmissionID: "submission",
		ReleaseID: "release", ReleaseVersion: 1, Published: true, AppealEnabled: true, QuestionID: "question", QuestionNo: "1", FinalGradeID: "grade", Score: 4, MaxScore: 8})
	service := appeal.NewPublishedQuestionAppealService(store)
	item, err := service.Create(t.Context(), "tenant", "student", "student-user", appeal.CreatePublishedQuestionAppealInput{
		ExamID: "exam", SourceReleaseID: "release", QuestionID: "question", ReasonCode: appeal.ReasonMissingStepCredit, Reason: "合成过程分申请",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.StartReview(t.Context(), "tenant", item.ID, "admin", appeal.StartQuestionAppealReviewInput{AssignedTo: "teacher", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	identities.AddResourceBoundary("question_appeal", item.ID, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school", ExamID: "exam", AssignedTo: "teacher"})
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	request := func(method, suffix, body, token string, want int) {
		t.Helper()
		req := httptest.NewRequest(method, "/api/v1/question-appeals/"+item.ID+suffix, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s %s: status=%d want=%d %s", method, suffix, rec.Code, want, rec.Body.String())
		}
	}
	for _, suffix := range []string{"", "/context", "/events", "/private-draft"} {
		request(http.MethodGet, suffix, "", "teacher-session", http.StatusOK)
		request(http.MethodGet, suffix, "", "other-session", http.StatusForbidden)
	}
	// 无图夹具应到达图片处理器并返回图片不可用，而非被对象边界错误拒绝。
	request(http.MethodGet, "/answer-image", "", "teacher-session", http.StatusNotFound)
	request(http.MethodGet, "/answer-image", "", "other-session", http.StatusForbidden)
	request(http.MethodPut, "/private-draft", fmt.Sprintf(`{"source":"release","source_revision":%d,"expected_revision":0,"values":{"reviewExplanation":"独立核对"}}`, item.Revision), "teacher-session", http.StatusOK)
	request(http.MethodDelete, "/private-draft", `{"expected_revision":1}`, "teacher-session", http.StatusOK)
	request(http.MethodPut, "/private-draft", `{}`, "other-session", http.StatusForbidden)
	request(http.MethodDelete, "/private-draft", `{}`, "other-session", http.StatusForbidden)
	request(http.MethodPost, "/review-opinion", fmt.Sprintf(`{"opinion":"needs_evidence","explanation":"请补充作答证据","expected_revision":%d}`, item.Revision), "teacher-session", http.StatusOK)
	request(http.MethodPost, "/review-opinion", `{}`, "other-session", http.StatusForbidden)
	// 分派教师不能借由可写草稿取得管理裁定或重新分派的权限。
	request(http.MethodPost, "/start-review", `{}`, "teacher-session", http.StatusForbidden)
}

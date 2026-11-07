package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

// 同一真实路由验证学校/岗位隔离、CAS 和命令重放；重放收据不能越过当前权限。
func TestQuestionAppealRestartRequiresCurrentManagedSchoolAndPermission(t *testing.T) {
	stores := NewMemoryApplicationStores()
	identities := stores.Identity.Auth.(*auth.MemoryStore)
	for _, actor := range []struct{ id, tenant, school, role, permission string }{
		{"manager", "tenant", "school-a", "school_admin", "appeal:manage"},
		{"foreign-manager", "tenant", "school-b", "school_admin", "appeal:manage"},
		{"foreign-tenant", "other-tenant", "school-a", "school_admin", "appeal:manage"},
		{"teacher", "tenant", "school-a", "teacher", "appeal:work"},
		{"reader", "tenant", "school-a", "school_admin", "appeal:read"},
	} {
		scope := "school"
		if actor.role == "teacher" {
			scope = "exam_task"
		}
		backendRegressionSession(t, identities, auth.UserWithPassword{User: auth.User{
			ID: actor.id, TenantID: actor.tenant, Username: actor.id, Status: "active",
			Roles: []string{actor.role}, Permissions: []string{actor.permission},
			DataScope: map[string]any{actor.role: map[string]any{"scope": scope, "school_id": actor.school}},
		}, SchoolID: actor.school, SecurityEpoch: 1}, actor.id+"-session")
	}
	backend := stores.Release.PublishedQuestionAppeal.(*appeal.PublishedQuestionAppealMemoryStore)
	source := appeal.PublishedQuestionAppealSource{TenantID: "tenant", ExamID: "exam", StudentID: "student", SubmissionID: "submission",
		ReleaseID: "release-1", ReleaseVersion: 1, Published: true, AppealEnabled: true,
		QuestionID: "question", QuestionNo: "1", FinalGradeID: "grade-original", Score: 4, MaxScore: 8}
	backend.SeedSource(source)
	service := appeal.NewPublishedQuestionAppealService(backend)
	item, err := service.Create(t.Context(), "tenant", "student", "student-user", appeal.CreatePublishedQuestionAppealInput{
		ExamID: "exam", SourceReleaseID: source.ReleaseID, QuestionID: "question", ReasonCode: appeal.ReasonMissingStepCredit, Reason: "合成过程分申请",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err = service.StartReview(t.Context(), "tenant", item.ID, "manager", appeal.StartQuestionAppealReviewInput{AssignedTo: "teacher", ExpectedRevision: item.Revision})
	if err != nil {
		t.Fatal(err)
	}
	backend.SeedRegradeJob("old-job", "tenant", "exam", "question", source.ReleaseID, appeal.PublishedQuestionAppealRegradeItemSource{
		SubmissionID: "submission", OldFinalGradeID: source.FinalGradeID, OldScore: source.Score, MaxScore: source.MaxScore, Status: "pending",
	})
	item, err = service.Decide(t.Context(), "tenant", item.ID, "manager", appeal.DecideQuestionAppealInput{
		Decision: appeal.QuestionAppealDecisionReferRegrade, PublicResponse: "进入复评", RegradeJobID: "old-job", ExpectedRevision: item.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	source.ReleaseID, source.ReleaseVersion, source.FinalGradeID, source.Score = "release-2", 2, "grade-current", 6
	backend.SeedSource(source)
	identities.AddResourceBoundary("question_appeal", item.ID, auth.ResourceBoundary{TenantID: "tenant", SchoolID: "school-a", ExamID: "exam", AssignedTo: "teacher"})
	router := NewRouterWithApplicationStores(backendRegressionConfig(), logger.New(io.Discard, "error"), nil, nil, stores)
	request := func(token, key string, revision int64) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/question-appeals/"+item.ID+"/restart-review",
			strings.NewReader(fmt.Sprintf(`{"expected_revision":%d,"reason":"当前正式成绩已有另一轮更正"}`, revision)))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Idempotency-Key", key)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		token string
		want  int
	}{
		{"", http.StatusUnauthorized},
		{"foreign-manager-session", http.StatusForbidden},
		{"foreign-tenant-session", http.StatusForbidden},
		{"teacher-session", http.StatusForbidden},
		{"reader-session", http.StatusForbidden},
	} {
		rec := request(tc.token, "denied-"+tc.token, item.Revision)
		if rec.Code != tc.want {
			t.Fatalf("actor %q got %d want %d: %s", tc.token, rec.Code, tc.want, rec.Body.String())
		}
	}
	stale := request("manager-session", "stale", item.Revision-1)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale restart=%d %s", stale.Code, stale.Body.String())
	}
	first := request("manager-session", "restart-command", item.Revision)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"review_source_release_id":"release-2"`) || !strings.Contains(first.Body.String(), `"source_release_id":"release-1"`) {
		t.Fatalf("managed restart lost frozen/current source separation: %d %s", first.Code, first.Body.String())
	}
	replay := request("manager-session", "restart-command", item.Revision)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" || replay.Body.String() != first.Body.String() {
		t.Fatalf("restart receipt did not replay: %d %s", replay.Code, replay.Body.String())
	}
	manager, err := identities.FindUserBySession(t.Context(), auth.HashToken("manager-session"), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	manager.Permissions = nil
	identities.AddUser(auth.UserWithPassword{User: manager, SchoolID: "school-a", SecurityEpoch: 1})
	deniedReplay := request("manager-session", "restart-command", item.Revision)
	if deniedReplay.Code != http.StatusForbidden || deniedReplay.Header().Get("Idempotency-Replayed") == "true" {
		t.Fatalf("revoked permission replayed restart: %d %s", deniedReplay.Code, deniedReplay.Body.String())
	}
}

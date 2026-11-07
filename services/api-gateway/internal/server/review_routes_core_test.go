package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewTaskRoutesCreateAssignSubmitAndAudit(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
	addReviewManagedExam(authStore, "exam-1")
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks", `{"answer_segment_id":"segment-1","source":"evidence_verification_failed","priority":3}`, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"anonymous_code":"ANON-001"`) || strings.Contains(rec.Body.String(), "student_name") {
		t.Fatalf("create review task expected anonymous task, got %d %s", rec.Code, rec.Body.String())
	}
	taskID := decodeReviewTaskID(t, rec.Body.Bytes())

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks", "", managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"anonymous_code":"ANON-001"`) {
		t.Fatalf("list review tasks expected anonymous task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+taskID, "", managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+taskID+`"`) {
		t.Fatalf("get review task expected task detail, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", `{"assigned_to":"`+reviewGraderID+`","expected_revision":1}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"assigned"`) {
		t.Fatalf("assign review task expected assigned, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/return", `{"reason":"needs second look","expected_revision":2}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"returned"`) || !strings.Contains(rec.Body.String(), `"return_reason":"needs second look"`) {
		t.Fatalf("return before submission expected returned task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", `{"expected_revision":3,"score":4,"rubric_selections":[{"point_id":"p1","score":4}],"comments":"clear","reason":"manual review"}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"submitted"`) || !strings.Contains(rec.Body.String(), `"score":4`) {
		t.Fatalf("submit human grade expected submitted grade, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/return", `{"reason":"must not invalidate committed grade","expected_revision":4}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "invalid_review_transition") {
		t.Fatalf("submitted task return expected 409 without grade mutation, got %d %s", rec.Code, rec.Body.String())
	}

	reviewAssertAuditAction(t, authStore, "review.task_created")
	reviewAssertAuditAction(t, authStore, "review.task_assigned")
	reviewAssertAuditAction(t, authStore, "review.human_grade_submitted")
	reviewAssertAuditAction(t, authStore, "review.task_returned")
}

func TestReviewSubmitRejectsInconsistentRubric(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")
	taskID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	assignReviewTaskForRoute(t, router, managerToken, taskID, reviewGraderID)

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/submit", `{"expected_revision":2,"score":4,"rubric_selections":[{"point_id":"p1","score":3}]}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_review_input") {
		t.Fatalf("rubric total mismatch expected 400, got %d %s", rec.Code, rec.Body.String())
	}
	unchanged, err := reviewStore.GetTask(t.Context(), reviewTenantID, taskID)
	if err != nil || unchanged.Status != "assigned" {
		t.Fatalf("rejected grade must leave task assigned, task=%#v err=%v", unchanged, err)
	}
}

func TestReviewTaskRoutesRequirePermission(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"system:read"})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	token := reviewLogin(t, router, "review_manager")

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks", `{"answer_segment_id":"segment-1","source":"manual_sample"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("arbitration route expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestQualityManagementRoutesDoNotExposeGoldOrSeedEvidenceToGraders(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager": {"review:manage"},
		"review_grader":  {"review:work"},
	})
	router := reviewRouter(authStore, review.NewMemoryStore())
	graderToken := reviewLogin(t, router, "review_grader")

	// 即使底层存储为空，这些路径也要求 review:manage。
	// 403 证明权限边界在返回任何 Gold、Seed 或质量投影之前执行。
	for _, path := range []string{
		"/api/v1/gold-papers",
		"/api/v1/exams/exam-1/questions/question-1/answer-groups",
		"/api/v1/exams/exam-1/questions/question-1/answer-group-metrics",
		"/api/v1/seed-observations",
		"/api/v1/grading-quality-incidents",
		"/api/v1/regrade-items/item-1/review-context",
		"/api/v1/regrade-items/item-1/review-segment-image",
		"/api/v1/answer-groups/group-1/samples/segment-1/context",
		"/api/v1/answer-groups/group-1/samples/segment-1/segment-image",
		"/api/v1/exams/exam-1/review-actors",
		"/api/v1/exams/exam-1/processing/actors",
		"/api/v1/processing/exceptions/exception-1/context",
		"/api/v1/processing/exceptions/exception-1/page-image",
		"/api/v1/exams/exam-1/capture-targets?submission_id=submission-1",
	} {
		req := reviewAuthedRequest(http.MethodGet, path, "", graderToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("grader must not read quality-management route %s, got %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestReviewWorkPermissionIsScopedToAssignedTasks(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":          {"review:manage"},
		"review_grader":           {"review:work"},
		"review_second_grader":    {"review:work"},
		"review_arbitrator":       {"arbitration:work"},
		"review_other_arbitrator": {"evidence:manage"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewTeacherID, TenantID: reviewTenantID, TenantCode: "demo", Username: "review_teacher",
		DisplayName: "Review Teacher", Status: "active", Roles: []string{"teacher"}, Permissions: []string{
			"review:manage", "arbitration:manage", "segment:manage", "grading:manage", "evidence:manage",
		},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	reviewStore.AddContext(reviewTenantID, "segment-2", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")
	evidenceOnlyToken := reviewLogin(t, router, "review_other_arbitrator")
	teacherToken := reviewLogin(t, router, "review_teacher")

	firstID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	secondID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "score_anomaly")
	assignReviewTaskForRoute(t, router, managerToken, firstID, reviewGraderID)
	assignReviewTaskForRoute(t, router, managerToken, secondID, reviewSecondGraderID)

	reviewAssertAssignedTaskVisibility(t, router, firstID, secondID, graderToken, teacherToken)
	reviewAssertDraftAndImageAuthority(t, router, firstID, graderToken, teacherToken, managerToken, evidenceOnlyToken)
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/submit", `{"expected_revision":2,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("review worker should submit assigned task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/return", `{"reason":"worker cannot return"}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker should not return tasks, got %d %s", rec.Code, rec.Body.String())
	}
}

// 任务列表、读取和派发分别核对已分配范围；教师的管理权限仍不能绕过任务归属。
func reviewAssertAssignedTaskVisibility(t *testing.T, router http.Handler, firstID, secondID, graderToken, teacherToken string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks", "", graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), firstID) || strings.Contains(rec.Body.String(), secondID) {
		t.Fatalf("review worker list should contain only assigned tasks, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID, "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), firstID) {
		t.Fatalf("review worker should get assigned task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+secondID, "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker should not get another assignee task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks", "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), firstID) || strings.Contains(rec.Body.String(), secondID) {
		t.Fatalf("teacher with review:manage must still see only assigned tasks, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID, "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("teacher with review:manage must not read another reviewer's task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+secondID+"/assign", `{"assigned_to":"`+reviewGraderID+`"}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("teacher with review:manage must not assign review tasks, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+secondID+"/assign", `{"assigned_to":"`+reviewGraderID+`"}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker should not assign tasks, got %d %s", rec.Code, rec.Body.String())
	}

}

// 管理者不能读写他人的草稿；通用证据入口和带身份的原图分别按原权限断言，不把 404 当作内容存在。
func reviewAssertDraftAndImageAuthority(t *testing.T, router http.Handler, firstID, graderToken, teacherToken, managerToken, evidenceOnlyToken string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/draft", "", managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review manager must not read the assigned reviewer's draft, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPut, "/api/v1/review-tasks/"+firstID+"/draft", `{"score":1,"rubric_selections":[],"viewer_state":{},"expected_revision":0}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review manager must not write the assigned reviewer's draft, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/draft", "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assigned reviewer should read their own draft, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/answer-segments/segment-1/image", "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker should use task-scoped image route, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/answer-segments/segment-1/image", "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin teacher must not bypass task scoping through the generic segment image route, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/answer-segments/segment-1/evidence", "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("non-admin teacher must not read arbitrary segment evidence, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/original-image", "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("review worker must not access the identity-bearing original image, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/original-image", "", managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("review manager should pass original-image authorization, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/original-image", "", evidenceOnlyToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin evidence manager must not access the original image, got %d %s", rec.Code, rec.Body.String())
	}

}

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

const reviewTenantID = "00000000-0000-0000-0000-000000000002"
const reviewManagerID = "00000000-0000-0000-0000-000000000601"
const reviewGraderID = "00000000-0000-0000-0000-000000000602"
const reviewSecondGraderID = "00000000-0000-0000-0000-000000000603"
const reviewArbitratorID = "00000000-0000-0000-0000-000000000604"
const reviewOtherArbitratorID = "00000000-0000-0000-0000-000000000605"
const reviewInactiveGraderID = "00000000-0000-0000-0000-000000000606"
const reviewInactiveArbitratorID = "00000000-0000-0000-0000-000000000607"
const reviewCrossTenantUserID = "00000000-0000-0000-0000-000000000608"
const reviewTeacherID = "00000000-0000-0000-0000-000000000609"

func TestReviewTaskRoutesCreateAssignSubmitAndAudit(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
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

func TestReviewWorkerCanReturnOnlyOwnActiveClaim(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":       {"review:manage"},
		"review_grader":        {"review:work"},
		"review_second_grader": {"review:work"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	reviewStore.AddContext(reviewTenantID, "segment-2", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")
	secondGraderToken := reviewLogin(t, router, "review_second_grader")

	firstID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	secondID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "manual_sample")
	assignReviewTaskForRoute(t, router, managerToken, firstID, reviewGraderID)
	assignReviewTaskForRoute(t, router, managerToken, secondID, reviewSecondGraderID)

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/return", `{"reason":"not claimed","expected_revision":2}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("assigned but unclaimed task return expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), firstID) || !strings.Contains(rec.Body.String(), `"revision":3`) {
		t.Fatalf("claim next expected first task revision 3, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/release", "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("release claimed task expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/return", `{"reason":"lease released","expected_revision":4}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("released task return expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revision":5`) {
		t.Fatalf("reclaim expected revision 5, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/return", `{"reason":"needs reassignment","expected_revision":5}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"returned"`) {
		t.Fatalf("own active claim return expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+secondID+"/return", `{"reason":"another reviewer task","expected_revision":2}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("another reviewer's task return expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, secondGraderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), secondID) {
		t.Fatalf("second reviewer should still claim their own task, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReviewTaskClaimNextNeverImplicitlyAssignsPendingTask(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager": {"review:manage"},
		"review_grader":  {"review:work"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	reviewStore.AddContext(reviewTenantID, "segment-2", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")

	pendingID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("review worker must not claim an unassigned task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{"allow_unassigned":true}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("claim permission must not be client-controlled, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("review manager must explicitly assign a pending task instead of self-claiming it, got %d %s", rec.Code, rec.Body.String())
	}
	pendingTask, err := reviewStore.GetTask(t.Context(), reviewTenantID, pendingID)
	if err != nil || pendingTask.Status != "pending" || pendingTask.AssignedTo != "" {
		t.Fatalf("denied implicit claim must leave the pending task unchanged, task=%#v err=%v", pendingTask, err)
	}

	assignedID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "manual_sample")
	assignReviewTaskForRoute(t, router, managerToken, assignedID, reviewGraderID)
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), assignedID) {
		t.Fatalf("review worker should continue an explicitly assigned task, got %d %s", rec.Code, rec.Body.String())
	}
}

// 这是评分工作台所用的服务器端验收路径。
// 它在首个任务提交前读取下一个已分配任务的上下文：预取必须保持只读，不能取得下一任务的租约。
// 浏览器随后只持有 /next 返回的任务。
func TestReviewWorkbenchClaimDraftRefreshSubmitAndNext(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager": {"review:manage"},
		"review_grader":  {"review:work"},
	})
	reviewStore := review.NewMemoryStore()
	firstContext := reviewRouteContext()
	firstContext.AnswerSegmentID = "segment-1"
	secondContext := reviewRouteContext()
	secondContext.AnswerSegmentID = "segment-2"
	secondContext.SubmissionID = "submission-2"
	reviewStore.AddContext(reviewTenantID, "segment-1", firstContext)
	reviewStore.AddContext(reviewTenantID, "segment-2", secondContext)
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")

	firstID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	secondID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "manual_sample")
	assignReviewTaskForRoute(t, router, managerToken, firstID, reviewGraderID)
	assignReviewTaskForRoute(t, router, managerToken, secondID, reviewGraderID)

	reviewAssertNextContextPrefetchIsReadOnly(t, reviewStore, router, graderToken, secondID)
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+firstID+`"`) {
		t.Fatalf("claim first assigned task expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	claimed, err := reviewStore.GetTask(t.Context(), reviewTenantID, firstID)
	if err != nil || claimed.Revision != 3 {
		t.Fatalf("claim must establish the first task lease/version, task=%#v err=%v", claimed, err)
	}

	reviewAssertDraftRefreshAndVersionConflict(t, router, graderToken, firstID)
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/submit", `{"expected_revision":3,"score":4,"rubric_selections":[{"point_id":"p1","score":4}],"comments":"final grade","reason":"manual review"}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"submitted"`) {
		t.Fatalf("submit first task expected 201, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+secondID+`"`) {
		t.Fatalf("next task after submit expected second assigned task, got %d %s", rec.Code, rec.Body.String())
	}
}

// 预取下一任务只读上下文，读取前后的修订号和 assigned 状态必须保持一致，随后才领取当前任务。
func reviewAssertNextContextPrefetchIsReadOnly(t *testing.T, reviewStore *review.MemoryStore, router http.Handler, graderToken, secondID string) {
	t.Helper()
	beforePrefetch, err := reviewStore.GetTask(t.Context(), reviewTenantID, secondID)
	if err != nil {
		t.Fatalf("get next task before prefetch: %v", err)
	}
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+secondID+"/context", "", graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"task":{"id":"`+secondID+`"`) {
		t.Fatalf("prefetch next task context expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	afterPrefetch, err := reviewStore.GetTask(t.Context(), reviewTenantID, secondID)
	if err != nil || afterPrefetch.Revision != beforePrefetch.Revision || afterPrefetch.Status != "assigned" {
		t.Fatalf("context prefetch must not claim or mutate next task, before=%#v after=%#v err=%v", beforePrefetch, afterPrefetch, err)
	}

}

// 同一已领取任务依次保存草稿、刷新恢复和拒绝旧修订号，确认服务端草稿与租约版本没有混用。
func reviewAssertDraftRefreshAndVersionConflict(t *testing.T, router http.Handler, graderToken, firstID string) {
	t.Helper()
	draftBody := `{"score":4,"rubric_selections":[{"point_id":"p1","score":4}],"comments":"draft survives refresh","private_note":"teacher only","student_feedback":"good work","viewer_state":{"mode":"segment"},"expected_revision":0}`
	req := reviewAuthedRequest(http.MethodPut, "/api/v1/review-tasks/"+firstID+"/draft", draftBody, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"revision":1`) {
		t.Fatalf("initial draft save expected 200 revision 1, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/context", "", graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"comments":"draft survives refresh"`) || !strings.Contains(rec.Body.String(), `"expected_revision":3`) {
		t.Fatalf("refresh must restore the server draft and task revision, got %d %s", rec.Code, rec.Body.String())
	}

	staleDraftBody := `{"score":3,"rubric_selections":[{"point_id":"p1","score":3}],"comments":"stale tab","private_note":"","student_feedback":"","viewer_state":{},"expected_revision":0}`
	req = reviewAuthedRequest(http.MethodPut, "/api/v1/review-tasks/"+firstID+"/draft", staleDraftBody, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "resource_version_conflict") {
		t.Fatalf("stale draft revision must return conflict, got %d %s", rec.Code, rec.Body.String())
	}

}

func TestReviewTaskReleaseKeepsAssignmentAndAllowsResume(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":       {"review:manage"},
		"review_grader":        {"review:work"},
		"review_second_grader": {"review:work"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	graderToken := reviewLogin(t, router, "review_grader")
	secondGraderToken := reviewLogin(t, router, "review_second_grader")
	taskID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	assignReviewTaskForRoute(t, router, managerToken, taskID, reviewGraderID)

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), taskID) {
		t.Fatalf("assigned reviewer should claim task before release, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/release", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"assigned"`) || !strings.Contains(rec.Body.String(), `"assigned_to":"`+reviewGraderID+`"`) {
		t.Fatalf("release should keep assignment and status, got %d %s", rec.Code, rec.Body.String())
	}
	stored, err := reviewStore.GetTask(t.Context(), reviewTenantID, taskID)
	if err != nil || stored.AssignedTo != reviewGraderID || stored.Status != "assigned" {
		t.Fatalf("release must preserve durable assignment, task=%#v err=%v", stored, err)
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks", ``, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), taskID) {
		t.Fatalf("released task must remain in assigned reviewer's queue, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/release", `{}`, secondGraderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("different reviewer must not release assigned task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next", `{}`, graderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), taskID) {
		t.Fatalf("assigned reviewer should resume task after release, got %d %s", rec.Code, rec.Body.String())
	}
	reviewAssertAuditAction(t, authStore, "review.task_released")
}

func TestReviewTaskListFiltersByExam(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	secondContext := reviewRouteContext()
	secondContext.ExamID = "exam-2"
	secondContext.SubmissionID = "submission-2"
	secondContext.Question.ID = "question-2"
	secondContext.Question.ExamID = "exam-2"
	secondContext.Question.QuestionNo = "Q2"
	reviewStore.AddContext(reviewTenantID, "segment-2", secondContext)
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")

	firstID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	secondID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "manual_sample")
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks?exam_id=exam-2", "", managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), firstID) || !strings.Contains(rec.Body.String(), secondID) {
		t.Fatalf("exam-scoped task list should contain only the matching exam, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReviewTaskBatchAssignRoute(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	reviewStore.AddContext(reviewTenantID, "segment-2", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")

	firstID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")
	secondID := createReviewTaskForRoute(t, router, managerToken, "segment-2", "score_anomaly")

	body := `{"task_ids":["` + firstID + `","` + secondID + `"],"assigned_to":"` + reviewGraderID + `","expected_revisions":{"` + firstID + `":1,"` + secondID + `":1}}`
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/batch-assign", body, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"status":"assigned"`) != 2 {
		t.Fatalf("batch assign expected two assigned tasks, got %d %s", rec.Code, rec.Body.String())
	}
	reviewAssertAuditAction(t, authStore, "review.tasks_batch_assigned")
}

func TestReviewTaskAssignmentRequiresActiveGrader(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":    {"review:manage"},
		"review_grader":     {"review:work"},
		"review_arbitrator": {"arbitration:work"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	taskID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "ocr_low_confidence")

	for _, target := range []string{reviewManagerID, reviewArbitratorID, reviewOtherArbitratorID} {
		req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", `{"assigned_to":"`+target+`","expected_revision":1}`, managerToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-grader %s must not receive review task, got %d %s", target, rec.Code, rec.Body.String())
		}
	}

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", `{"assigned_to":"`+reviewGraderID+`","expected_revision":1}`, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"assigned_to":"`+reviewGraderID+`"`) {
		t.Fatalf("active grader should receive review task, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReviewOriginalImageRequiresAdministratorRole(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager": {"review:manage"},
	})
	users := []auth.User{
		{ID: reviewTeacherID, TenantID: reviewTenantID, TenantCode: "demo", Username: "privileged_teacher", DisplayName: "Privileged Teacher", Status: "active", Roles: []string{"teacher"}, Permissions: []string{"review:manage", "evidence:manage", "tenant:manage"}},
		{ID: "00000000-0000-0000-0000-000000000610", TenantID: reviewTenantID, TenantCode: "demo", Username: "privileged_grader", DisplayName: "Privileged Grader", Status: "active", Roles: []string{"grader"}, Permissions: []string{"review:manage", "evidence:manage", "tenant:manage"}},
		{ID: "00000000-0000-0000-0000-000000000611", TenantID: reviewTenantID, TenantCode: "demo", Username: "platform_admin", DisplayName: "Platform Admin", Status: "active", Roles: []string{"platform_admin"}, Permissions: []string{"review:manage"}},
		{ID: "00000000-0000-0000-0000-000000000612", TenantID: reviewTenantID, TenantCode: "demo", Username: "tenant_admin", DisplayName: "Tenant Admin", Status: "active", Roles: []string{"tenant_admin"}, Permissions: []string{"evidence:manage"}},
		{ID: "00000000-0000-0000-0000-000000000613", TenantID: reviewTenantID, TenantCode: "demo", Username: "school_admin", DisplayName: "School Admin", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"tenant:manage"}},
	}
	for _, user := range users {
		addReviewManagedUser(t, authStore, user)
	}
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	taskID := createReviewTaskForRoute(t, router, managerToken, "segment-1", "manual_sample")

	for _, username := range []string{"privileged_teacher", "privileged_grader"} {
		t.Run("deny "+username, func(t *testing.T) {
			token := reviewLogin(t, router, username)
			req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+taskID+"/original-image", "", token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
				t.Fatalf("%s must not access an original review image despite management permissions, got %d %s", username, rec.Code, rec.Body.String())
			}
		})
	}

	for _, username := range []string{"platform_admin", "tenant_admin", "school_admin"} {
		t.Run("allow "+username, func(t *testing.T) {
			token := reviewLogin(t, router, username)
			req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+taskID+"/original-image", "", token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s should pass original-image authorization, got %d %s", username, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDoubleMarkSessionRequiresActiveSameTenantGraders(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":       {"review:manage"},
		"review_grader":        {"review:work"},
		"review_second_grader": {"review:work"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewInactiveGraderID, TenantID: reviewTenantID, TenantCode: "demo",
		Username: "review_inactive_grader", DisplayName: "Inactive Grader", Status: "inactive", Roles: []string{"grader"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewCrossTenantUserID, TenantID: "00000000-0000-0000-0000-000000000099", TenantCode: "other",
		Username: "review_cross_tenant_grader", DisplayName: "Cross Tenant Grader", Status: "active", Roles: []string{"grader"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")

	req := reviewAuthedRequest(http.MethodPut, "/api/v1/exams/exam-1/double-mark-policy", `{"enabled":true,"threshold":1,"resolution_strategy":"average"}`, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set double mark policy expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	for name, reviewers := range map[string][2]string{
		"manager role":        {reviewManagerID, reviewSecondGraderID},
		"arbitrator role":     {reviewGraderID, reviewArbitratorID},
		"inactive grader":     {reviewGraderID, reviewInactiveGraderID},
		"cross tenant grader": {reviewGraderID, reviewCrossTenantUserID},
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"answer_segment_id":"segment-1","first_reviewer_id":"` + reviewers[0] + `","second_reviewer_id":"` + reviewers[1] + `"}`
			req := reviewAuthedRequest(http.MethodPost, "/api/v1/double-mark-sessions", body, managerToken)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("invalid double-mark reviewers expected 403, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}

	body := `{"answer_segment_id":"segment-1","first_reviewer_id":"` + reviewGraderID + `","second_reviewer_id":"` + reviewSecondGraderID + `"}`
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/double-mark-sessions", body, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("two active same-tenant graders expected 201, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestArbitrationTaskCreateRequiresActiveSameTenantArbitrator(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":    {"arbitration:manage"},
		"review_grader":     {"review:work"},
		"review_arbitrator": {"arbitration:work"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewInactiveArbitratorID, TenantID: reviewTenantID, TenantCode: "demo",
		Username: "review_inactive_arbitrator", DisplayName: "Inactive Arbitrator", Status: "inactive", Roles: []string{"arbitrator"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewCrossTenantUserID, TenantID: "00000000-0000-0000-0000-000000000099", TenantCode: "other",
		Username: "review_cross_tenant_arbitrator", DisplayName: "Cross Tenant Arbitrator", Status: "active", Roles: []string{"arbitrator"},
	})
	router := reviewRouter(authStore, review.NewMemoryStore())
	managerToken := reviewLogin(t, router, "review_manager")

	for name, assigneeID := range map[string]string{
		"manager role":            reviewManagerID,
		"grader role":             reviewGraderID,
		"inactive arbitrator":     reviewInactiveArbitratorID,
		"cross tenant arbitrator": reviewCrossTenantUserID,
	} {
		t.Run(name, func(t *testing.T) {
			body := `{"double_mark_session_id":"missing-session","assigned_to":"` + assigneeID + `"}`
			req := reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks", body, managerToken)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("invalid arbitration assignee expected 403, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}

	body := `{"double_mark_session_id":"missing-session","assigned_to":"` + reviewArbitratorID + `"}`
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks", body, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("active same-tenant arbitrator should pass role validation, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestArbitrationWorkPermissionIsScopedToAssignedTasks(t *testing.T) {
	authStore := reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":          {"review:manage", "arbitration:manage"},
		"review_grader":           {"review:manage"},
		"review_second_grader":    {"review:manage"},
		"review_arbitrator":       {"arbitration:work"},
		"review_other_arbitrator": {"arbitration:work"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewTeacherID, TenantID: reviewTenantID, TenantCode: "demo", Username: "review_teacher",
		DisplayName: "Review Teacher", Status: "active", Roles: []string{"teacher"}, Permissions: []string{"arbitration:manage"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewInactiveArbitratorID, TenantID: reviewTenantID, TenantCode: "demo",
		Username: "review_inactive_arbitrator", DisplayName: "Inactive Arbitrator", Status: "inactive", Roles: []string{"arbitrator"},
	})
	addReviewManagedUser(t, authStore, auth.User{
		ID: reviewCrossTenantUserID, TenantID: "00000000-0000-0000-0000-000000000099", TenantCode: "other",
		Username: "review_cross_tenant_arbitrator", DisplayName: "Cross Tenant Arbitrator", Status: "active", Roles: []string{"arbitrator"},
	})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	firstToken := reviewLogin(t, router, "review_grader")
	secondToken := reviewLogin(t, router, "review_second_grader")
	arbitratorToken := reviewLogin(t, router, "review_arbitrator")
	otherArbitratorToken := reviewLogin(t, router, "review_other_arbitrator")
	teacherToken := reviewLogin(t, router, "review_teacher")

	req := reviewAuthedRequest(http.MethodPut, "/api/v1/exams/exam-1/double-mark-policy", `{"enabled":true,"threshold":1,"resolution_strategy":"average"}`, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("set double mark policy expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	body := `{"answer_segment_id":"segment-1","first_reviewer_id":"` + reviewGraderID + `","second_reviewer_id":"` + reviewSecondGraderID + `"}`
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/double-mark-sessions", body, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create double mark session expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	session := decodeDoubleMarkSession(t, rec.Body.Bytes())

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+session.FirstReviewTaskID+"/submit", `{"expected_revision":1,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, firstToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit first mark expected 201, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+session.SecondReviewTaskID+"/submit", `{"expected_revision":1,"score":1,"rubric_selections":[{"point_id":"p1","score":1}]}`, secondToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("submit second mark expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	arbitrationID := decodeArbitrationIDFromSubmit(t, rec.Body.Bytes())

	reviewAssertArbitrationFilterAndUnassignedSubmission(t, router, managerToken, arbitrationID)
	reviewAssertInvalidArbitrationAssignees(t, router, managerToken, arbitrationID)
	reviewAssertUnassignedArbitrationAccess(t, router, arbitrationID, arbitratorToken, teacherToken)
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+reviewArbitratorID+`","expected_revision":1}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign arbitration expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	reviewAssertAssignedArbitrationMaterialsAndSubmission(t, router, arbitrationID, arbitratorToken, otherArbitratorToken)
}

// 考试筛选分别检查包含和排除；管理者提交不能隐式把未分配的仲裁任务指派给自己。
func reviewAssertArbitrationFilterAndUnassignedSubmission(t *testing.T, router http.Handler, managerToken, arbitrationID string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks?exam_id=exam-1", "", managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), arbitrationID) {
		t.Fatalf("matching exam filter should include arbitration task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks?exam_id=other-exam", "", managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), arbitrationID) {
		t.Fatalf("non-matching exam filter should exclude arbitration task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/submit", `{"final_score":3,"reason":"manager must assign an arbitrator first","expected_revision":1}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("manager must not implicitly self-assign an arbitration task while submitting, got %d %s", rec.Code, rec.Body.String())
	}

}

// 原有子测试分别保留角色、停用状态和租户边界检查，失败仍只中止对应子测试。
func reviewAssertInvalidArbitrationAssignees(t *testing.T, router http.Handler, managerToken, arbitrationID string) {
	t.Helper()
	for name, assigneeID := range map[string]string{
		"manager role":            reviewManagerID,
		"grader role":             reviewGraderID,
		"inactive arbitrator":     reviewInactiveArbitratorID,
		"cross tenant arbitrator": reviewCrossTenantUserID,
	} {
		t.Run("reject assignment to "+name, func(t *testing.T) {
			req := reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+assigneeID+`","expected_revision":1}`, managerToken)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("invalid arbitration assignee expected 403, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}

}

// 正确仲裁角色在未分配时仍不能读取；教师管理权限也不能替代管理员派发。
func reviewAssertUnassignedArbitrationAccess(t *testing.T, router http.Handler, arbitrationID, arbitratorToken, teacherToken string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", arbitratorToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unassigned arbitration worker should not get task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("teacher with arbitration:manage must not read an unassigned arbitration task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+reviewArbitratorID+`","expected_revision":1}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("teacher with arbitration:manage must not assign arbitration tasks, got %d %s", rec.Code, rec.Body.String())
	}

}

// 派发后只向负责人提供冻结标准；先拒绝其他仲裁者的读取和提交，再由负责人产生最终成绩。
func reviewAssertAssignedArbitrationMaterialsAndSubmission(t *testing.T, router http.Handler, arbitrationID, arbitratorToken, otherArbitratorToken string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", arbitratorToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), arbitrationID) {
		t.Fatalf("assigned arbitration worker should get task, got %d %s", rec.Code, rec.Body.String())
	}
	// 普通仲裁教师没有 exam:manage；自己的任务仍需提供冻结满分和评分标准。
	var materials struct {
		Task review.ArbitrationTask `json:"arbitration_task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &materials); err != nil {
		t.Fatal(err)
	}
	if materials.Task.Context.Question == nil || materials.Task.Context.Question.Score != 5 ||
		materials.Task.Context.Rubric == nil || materials.Task.Context.Rubric.MaxScore != 5 || len(materials.Task.Context.Rubric.Points) == 0 {
		t.Fatalf("assigned worker has no frozen standards: %s", rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", otherArbitratorToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other arbitration worker should not get task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/submit", `{"final_score":3,"reason":"not assigned","student_feedback":"Final score after arbitration.","expected_revision":2}`, otherArbitratorToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("other arbitration worker should not submit task, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/submit", `{"final_score":3,"reason":"rubric evidence supports middle score","student_feedback":"Final score after arbitration.","expected_revision":2}`, arbitratorToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("assigned arbitration worker should submit task, got %d %s", rec.Code, rec.Body.String())
	}

}

func TestDoubleMarkAndArbitrationRoutes(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage", "arbitration:manage"})
	reviewStore := review.NewMemoryStore()
	reviewStore.AddContext(reviewTenantID, "segment-1", reviewRouteContext())
	router := reviewRouter(authStore, reviewStore)
	managerToken := reviewLogin(t, router, "review_manager")
	firstToken := reviewLogin(t, router, "review_grader")
	secondToken := reviewLogin(t, router, "review_second_grader")
	arbitratorToken := reviewLogin(t, router, "review_arbitrator")

	req := reviewAuthedRequest(http.MethodPut, "/api/v1/exams/exam-1/double-mark-policy", `{"enabled":true,"threshold":1,"resolution_strategy":"average"}`, managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"resolution_strategy":"average"`) {
		t.Fatalf("set double mark policy expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	body := `{"answer_segment_id":"segment-1","first_reviewer_id":"` + reviewGraderID + `","second_reviewer_id":"` + reviewSecondGraderID + `"}`
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/double-mark-sessions", body, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || strings.Count(rec.Body.String(), `"review-task-`) != 2 {
		t.Fatalf("create double mark session expected two review tasks, got %d %s", rec.Code, rec.Body.String())
	}
	session := decodeDoubleMarkSession(t, rec.Body.Bytes())

	arbitrationID := reviewSubmitBlindDoubleMarks(t, router, session.FirstReviewTaskID, session.SecondReviewTaskID, firstToken, secondToken)
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+reviewGraderID+`","expected_revision":1}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("first reviewer should not be assignable as arbitrator by default, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+reviewArbitratorID+`","expected_revision":1}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"assigned"`) {
		t.Fatalf("assign arbitration expected assigned, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", arbitratorToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"first_score":4`) || !strings.Contains(rec.Body.String(), `"second_score":1`) || !strings.Contains(rec.Body.String(), `"raw_answer":"student wrote the main idea"`) {
		t.Fatalf("arbitration detail expected scores and context, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/submit", `{"final_score":3,"reason":"rubric evidence supports middle score","student_feedback":"Final score after arbitration.","expected_revision":2}`, arbitratorToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"source":"arbitration"`) || !strings.Contains(rec.Body.String(), `"score":3`) {
		t.Fatalf("submit arbitration expected final grade, got %d %s", rec.Code, rec.Body.String())
	}

	reviewAssertAuditAction(t, authStore, "review.double_mark_policy_set")
	reviewAssertAuditAction(t, authStore, "review.double_mark_session_created")
	reviewAssertAuditAction(t, authStore, "arbitration.task_created")
	reviewAssertAuditAction(t, authStore, "arbitration.task_assigned")
	reviewAssertAuditAction(t, authStore, "arbitration.submitted")
	reviewAssertAuditAction(t, authStore, "final_grade.created")
}

// 第一轮提交和任务详情均保持盲评；第二轮产生仲裁而不是直接终结成绩，然后才允许派发仲裁。
func reviewSubmitBlindDoubleMarks(t *testing.T, router http.Handler, firstID, secondID, firstToken, secondToken string) string {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/submit", `{"expected_revision":1,"score":4,"rubric_selections":[{"point_id":"p1","score":4}]}`, firstToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), `"second_score"`) {
		t.Fatalf("first mark should not expose second score, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID, "", firstToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"first_score"`) || strings.Contains(rec.Body.String(), `"second_score"`) {
		t.Fatalf("review task detail must remain blind, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+secondID+"/submit", `{"expected_revision":1,"score":1,"rubric_selections":[{"point_id":"p1","score":1}]}`, secondToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"arbitration_task"`) || strings.Contains(rec.Body.String(), `"final_grade"`) {
		t.Fatalf("second mark should create arbitration without final grade, got %d %s", rec.Code, rec.Body.String())
	}
	return decodeArbitrationIDFromSubmit(t, rec.Body.Bytes())

}

func reviewRouter(authStore *auth.MemoryStore, reviewStore review.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return NewMemoryRouter(cfg, loggerForReviewTest(), nil, files.NewMemoryObjectStorage(), func(stores *ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Grading.Review = reviewStore
	})
}

func createReviewTaskForRoute(t *testing.T, router http.Handler, token string, segmentID string, source string) string {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks", `{"answer_segment_id":"`+segmentID+`","source":"`+source+`"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create review task expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	return decodeReviewTaskID(t, rec.Body.Bytes())
}

func loggerForReviewTest() *logger.Logger {
	return logger.New(io.Discard, "error")
}

func reviewAuthStore(t *testing.T, permissions []string) *auth.MemoryStore {
	return reviewAuthStoreByUser(t, map[string][]string{
		"review_manager":          permissions,
		"review_grader":           permissions,
		"review_second_grader":    permissions,
		"review_arbitrator":       permissions,
		"review_other_arbitrator": permissions,
	})
}

func reviewAuthStoreByUser(t *testing.T, permissionsByUsername map[string][]string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	for _, user := range []auth.User{
		{
			ID:          reviewManagerID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_manager",
			DisplayName: "Review Manager",
			Status:      "active",
			Roles:       []string{"school_admin"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          reviewGraderID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_grader",
			DisplayName: "Review Grader",
			Status:      "active",
			Roles:       []string{"grader"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewSecondGraderID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_second_grader",
			DisplayName: "Review Second Grader",
			Status:      "active",
			Roles:       []string{"grader"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewArbitratorID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_arbitrator",
			DisplayName: "Review Arbitrator",
			Status:      "active",
			Roles:       []string{"arbitrator"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
		{
			ID:          reviewOtherArbitratorID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "review_other_arbitrator",
			DisplayName: "Review Other Arbitrator",
			Status:      "active",
			Roles:       []string{"arbitrator"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
	} {
		if scopedPermissions, ok := permissionsByUsername[user.Username]; ok {
			user.Permissions = scopedPermissions
		} else {
			user.Permissions = nil
		}
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}

func addReviewManagedUser(t *testing.T, store *auth.MemoryStore, user auth.User) {
	t.Helper()
	if len(user.DataScope) == 0 {
		switch {
		case slices.Contains(user.Roles, "tenant_admin"), slices.Contains(user.Roles, "platform_admin"):
			user.DataScope = map[string]any{"scope": "tenant"}
		case slices.Contains(user.Roles, "school_admin"), slices.Contains(user.Roles, "teacher"):
			user.DataScope = map[string]any{"scope": "school", "school_id": "school-1"}
		case slices.Contains(user.Roles, "grader"), slices.Contains(user.Roles, "arbitrator"):
			user.DataScope = map[string]any{"scope": "assigned"}
		default:
			user.DataScope = map[string]any{"scope": "none"}
		}
	}
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
}

func reviewLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": username, "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func reviewAuthedRequest(method string, path string, body string, token string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func decodeReviewTaskID(t *testing.T, raw []byte) string {
	t.Helper()
	var response struct {
		Task struct {
			ID string `json:"id"`
		} `json:"task"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if response.Task.ID == "" {
		t.Fatalf("missing task id in %s", string(raw))
	}
	return response.Task.ID
}

func assignReviewTaskForRoute(t *testing.T, router http.Handler, token string, taskID string, assignedTo string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+taskID+"/assign", `{"assigned_to":"`+assignedTo+`","expected_revision":1}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("assign review task expected 200, got %d %s", rec.Code, rec.Body.String())
	}
}

func decodeDoubleMarkSession(t *testing.T, raw []byte) struct {
	ID                 string `json:"id"`
	FirstReviewTaskID  string `json:"first_review_task_id"`
	SecondReviewTaskID string `json:"second_review_task_id"`
} {
	t.Helper()
	var response struct {
		DoubleMarkSession struct {
			ID                 string `json:"id"`
			FirstReviewTaskID  string `json:"first_review_task_id"`
			SecondReviewTaskID string `json:"second_review_task_id"`
		} `json:"double_mark_session"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode double mark session: %v", err)
	}
	if response.DoubleMarkSession.ID == "" || response.DoubleMarkSession.FirstReviewTaskID == "" || response.DoubleMarkSession.SecondReviewTaskID == "" {
		t.Fatalf("missing double mark session ids in %s", string(raw))
	}
	return response.DoubleMarkSession
}

func decodeArbitrationIDFromSubmit(t *testing.T, raw []byte) string {
	t.Helper()
	var response struct {
		ArbitrationTask struct {
			ID string `json:"id"`
		} `json:"arbitration_task"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode arbitration task: %v", err)
	}
	if response.ArbitrationTask.ID == "" {
		t.Fatalf("missing arbitration id in %s", string(raw))
	}
	return response.ArbitrationTask.ID
}

func reviewRouteContext() review.Context {
	return review.Context{
		ExamID:          "exam-1",
		SubmissionID:    "submission-1",
		AnswerSegmentID: "segment-1",
		AnonymousCode:   "ANON-001",
		Question: paper.Question{
			ID:           "question-1",
			TenantID:     reviewTenantID,
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: "short_answer",
			Score:        5,
		},
		Rubric: paper.Rubric{
			ID:         "rubric-1",
			QuestionID: "question-1",
			Version:    "v1",
			Status:     "approved",
			MaxScore:   5,
			Points: []paper.RubricPoint{
				{ID: "p1", Description: "main idea", Score: 5, Required: true},
			},
		},
		RawAnswer: "student wrote the main idea",
		OCRText:   "student wrote the main idea",
		AISuggestion: map[string]any{
			"suggested_score": 4,
			"mock":            true,
		},
	}
}

func reviewAssertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}

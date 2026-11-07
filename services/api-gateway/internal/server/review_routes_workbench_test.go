package server

import (
	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/segment"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
	// 上下文预取会核对作答范围；该夹具登记相同的单题范围，不虚构裁图或评估快照。
	router := reviewRouter(authStore, reviewStore, reviewRouteScopeSegments{Store: segment.NewMemoryStore()})
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

package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewTaskListFiltersByExam(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"review:manage"})
	addReviewManagedExam(authStore, "exam-1")
	addReviewManagedExam(authStore, "exam-2")
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

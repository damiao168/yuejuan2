package server

import (
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/review"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

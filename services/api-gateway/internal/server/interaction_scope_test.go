package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestInteractionMaterialRoutesRequireManagementAndSameSchoolScope(t *testing.T) {
	store := reviewAuthStoreByUser(t, map[string][]string{"review_grader": {"review:work"}})
	addReviewManagedUser(t, store, auth.User{
		ID: reviewManagerID, TenantID: reviewTenantID, TenantCode: "demo", Username: "review_manager",
		Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"review:manage", "capture:manage"},
		DataScope: map[string]any{"scope": "school", "school_id": "school-1"},
	})
	for _, resource := range []struct{ kind, id string }{{"exam", "other-exam"}, {"regrade_item", "other-item"}, {"answer_group", "other-group"}, {"processing_exception", "other-exception"}} {
		store.AddResourceBoundary(resource.kind, resource.id, auth.ResourceBoundary{TenantID: reviewTenantID, SchoolID: "school-2", ExamID: "other-exam"})
	}
	router := reviewRouter(store, review.NewMemoryStore())
	manager := reviewLogin(t, router, "review_manager")
	grader := reviewLogin(t, router, "review_grader")
	// 使用真实路由/认证/对象边界中间件；空业务存储下的 403 必须早于材料或图片读取。
	for _, path := range []string{
		"/api/v1/regrade-items/other-item/review-context",
		"/api/v1/regrade-items/other-item/review-segment-image",
		"/api/v1/answer-groups/other-group/samples/segment/context",
		"/api/v1/answer-groups/other-group/samples/segment/segment-image",
		"/api/v1/exams/other-exam/review-actors",
		"/api/v1/exams/other-exam/processing/actors",
		"/api/v1/processing/exceptions/other-exception/context",
		"/api/v1/processing/exceptions/other-exception/page-image",
		"/api/v1/exams/other-exam/capture-targets?submission_id=submission",
	} {
		for actor, token := range map[string]string{"other-school-manager": manager, "blind-grader": grader} {
			t.Run(actor+path, func(t *testing.T) {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, reviewAuthedRequest(http.MethodGet, path, "", token))
				if response.Code != http.StatusForbidden {
					t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
				}
			})
		}
	}
}

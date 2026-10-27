package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/review"
)

func TestRetiredReviewCommentTemplateRoutesReturnNotFound(t *testing.T) {
	router := reviewRouter(reviewAuthStore(t, []string{"review:work"}), review.NewMemoryStore())
	token := reviewLogin(t, router, "review_grader")
	// 同时验证匿名和已授权阅卷身份，防止仅隐藏界面或靠权限拒绝来代替删除路由。
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/review/comment-templates"},
		{http.MethodPost, "/api/v1/review/comment-templates"},
		{http.MethodGet, "/api/v1/review/comment-templates/template-1"},
		{http.MethodPut, "/api/v1/review/comment-templates/template-1"},
		{http.MethodDelete, "/api/v1/review/comment-templates/template-1"},
		{http.MethodPost, "/api/v1/review/comment-templates/good/use"},
	} {
		for _, identity := range []struct{ name, token string }{{"anonymous", ""}, {"reviewer", token}} {
			t.Run(identity.name+" "+route.method+" "+route.path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, reviewAuthedRequest(route.method, route.path, `{}`, identity.token))
				if recorder.Code != http.StatusNotFound {
					t.Fatalf("retired route returned %d, want 404: %s", recorder.Code, recorder.Body.String())
				}
			})
		}
	}
}

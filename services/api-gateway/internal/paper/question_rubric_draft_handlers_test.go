package paper_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestQuestionRubricDraftRouteRequiresExamManage(t *testing.T) {
	for _, item := range []struct {
		name        string
		permissions []string
		login       bool
		want        int
	}{
		{name: "anonymous", want: http.StatusUnauthorized},
		{name: "permission missing", login: true, permissions: []string{"report:read"}, want: http.StatusForbidden},
		{name: "scoped exam not found", login: true, permissions: []string{"exam:manage"}, want: http.StatusNotFound},
	} {
		t.Run(item.name, func(t *testing.T) {
			router := testRouter(authStoreWithPermissions(t, item.permissions), paper.NewMemoryStore())
			token := ""
			if item.login {
				token = login(t, router)
			}
			request := authedRequest(http.MethodPost, "/api/v1/exams/missing-exam/questions/missing-question/rubric-draft", bytes.NewBufferString(`{"expected_question":{}}`), token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != item.want || item.want == http.StatusNotFound && !strings.Contains(response.Body.String(), "paper_resource_not_found") {
				t.Fatalf("route guard returned %d %s, want %d", response.Code, response.Body.String(), item.want)
			}
		})
	}
}

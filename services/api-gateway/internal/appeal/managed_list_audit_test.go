package appeal

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAuditQuestionAppealListUsesManagedSchool(t *testing.T) {
	store := NewPublishedQuestionAppealMemoryStore()
	identities := auth.NewMemoryStore()
	for _, school := range []string{"school-a", "school-b"} {
		id := "appeal-" + school
		exam := "exam-" + school
		store.appeals[id] = PublishedQuestionAppeal{ID: id, TenantID: "tenant", ExamID: exam, StudentID: "student-" + school, Reason: "private " + school}
		identities.AddResourceBoundary("exam", exam, auth.ResourceBoundary{TenantID: "tenant", SchoolID: school, ExamID: exam})
	}
	actor := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, Permissions: []string{appealManagePermission}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	scope, err := auth.ResolveDeclaredAccessScope(actor)
	if err != nil {
		t.Fatal(err)
	}
	scope.SchoolIDs = append(scope.SchoolIDs, "school-b")
	scope.ExamIDs = []string{"exam-school-b"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/question-appeals", nil)
	req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), actor), scope))
	rec := httptest.NewRecorder()
	NewPublishedQuestionAppealHandler(NewPublishedQuestionAppealService(store), identities).List(rec, req)
	var body struct {
		Appeals []PublishedQuestionAppeal `json:"appeals"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body.Appeals) != 1 || body.Appeals[0].ID != "appeal-school-a" {
		t.Fatalf("foreign teaching school leaked: %d %s", rec.Code, rec.Body.String())
	}
}

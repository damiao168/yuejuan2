package org

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestAuditOrganizationWritesRejectTeachingOnlySchool(t *testing.T) {
	actor := auth.User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school-a"}}}
	scope, err := auth.ResolveDeclaredAccessScope(actor)
	if err != nil {
		t.Fatal(err)
	}
	scope.SchoolIDs = append(scope.SchoolIDs, "school-b")
	scope.ClassIDs = []string{"class-b"}
	store := NewMemoryStore()
	handler := NewHandler(store, auth.NewMemoryStore())
	for _, tc := range []struct {
		name, body string
		handler    http.HandlerFunc
		want       int
	}{
		{"grade", `{"school_id":"school-b","name":"Synthetic grade"}`, handler.CreateGrade, http.StatusForbidden},
		{"class", `{"school_id":"school-b","grade_id":"grade-b","name":"Synthetic class","code":"class"}`, handler.CreateClass, http.StatusForbidden},
		{"student", `{"school_id":"school-b","class_id":"class-b","student_no":"synthetic","name":"Synthetic student","password":"Synthetic123!","password_confirm":"Synthetic123!"}`, handler.CreateStudent, http.StatusForbidden},
		{"csv", "student_no,name,school_id,class_id\nsynthetic,Synthetic,school-b,class-b\n", handler.ImportStudentsCSV, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/organization", strings.NewReader(tc.body))
			req = req.WithContext(auth.WithAccessScope(auth.WithUser(req.Context(), actor), scope))
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
			}
			if tc.name == "csv" && !strings.Contains(rec.Body.String(), `"created":0`) {
				t.Fatalf("foreign import wrote students: %s", rec.Body.String())
			}
		})
	}
	if len(store.grades) != 0 || len(store.classes) != 0 || len(store.students) != 0 {
		t.Fatal("rejected organization changes mutated data")
	}
}

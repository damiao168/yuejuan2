package org_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/org"
)

func TestOrganizationDeleteRoutesKeepManagedSchoolBoundaryAndReportChineseConflict(t *testing.T) {
	ctx := context.Background()
	store := org.NewMemoryStore()
	schoolA, _ := store.CreateSchool(ctx, "tenant-org", org.School{Name: "A", Code: "a"})
	schoolB, _ := store.CreateSchool(ctx, "tenant-org", org.School{Name: "B", Code: "b"})
	gradeA, _ := store.CreateGrade(ctx, "tenant-org", org.Grade{SchoolID: schoolA.ID, Name: "高一", LevelNo: 10})
	gradeB, _ := store.CreateGrade(ctx, "tenant-org", org.Grade{SchoolID: schoolB.ID, Name: "高一", LevelNo: 10})
	cls, _ := store.CreateClass(ctx, "tenant-org", org.Class{SchoolID: schoolA.ID, GradeID: gradeA.ID, Name: "1班", Code: "c1"})
	hash, _ := auth.HashPassword("ChangeMe123!")
	identity := auth.NewMemoryStore()
	identity.AddUser(auth.UserWithPassword{User: auth.User{ID: "admin-a", TenantID: "tenant-org", TenantCode: "demo", Username: "school_admin", Status: "active", Roles: []string{"school_admin"}, Permissions: []string{"org:manage"}, DataScope: map[string]any{"scope": "school", "school_id": schoolA.ID}}, PasswordHash: hash})
	for _, grade := range []org.Grade{gradeA, gradeB} {
		identity.AddResourceBoundary("grade", grade.ID, auth.ResourceBoundary{TenantID: "tenant-org", SchoolID: grade.SchoolID, GradeID: grade.ID})
	}
	identity.AddResourceBoundary("school_class", cls.ID, auth.ResourceBoundary{TenantID: "tenant-org", SchoolID: schoolA.ID, GradeID: gradeA.ID, ClassIDs: []string{cls.ID}})
	router := testRouter(identity, store)
	token := login(t, router)
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{
		{"/api/v1/grades/" + gradeB.ID, 403, "access_scope_forbidden"},
		{"/api/v1/grades/" + gradeA.ID, 409, "grade_has_classes"},
		{"/api/v1/classes/" + cls.ID, 204, ""},
		{"/api/v1/grades/" + gradeA.ID, 204, ""},
	} {
		req := httptest.NewRequest(http.MethodDelete, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.contains) {
			t.Fatalf("%s: %d %s", tc.path, rec.Code, rec.Body.String())
		}
		if rec.Code == 409 && !strings.Contains(rec.Body.String(), "仍有班级") {
			t.Fatalf("conflict lacks actionable explanation: %s", rec.Body.String())
		}
	}
}

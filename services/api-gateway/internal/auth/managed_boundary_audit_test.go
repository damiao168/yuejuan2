package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditManagementBoundaryDoesNotUseTeachingNavigation(t *testing.T) {
	scope := AccessScope{TenantID: "tenant", ActorID: "admin", schoolWide: true, schoolManagementIDs: []string{"school-a"}, SchoolIDs: []string{"school-a", "school-b"}, ClassIDs: []string{"class-b"}, ExamIDs: []string{"exam-b"}}
	boundary := ResourceBoundary{TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b", ResourceType: "exam", ResourceID: "exam-b"}
	if !AllowsResourceBoundary(scope, boundary) || AllowsManagedResourceBoundary(scope, boundary) || scope.ManagesSchool("school-b") {
		t.Fatal("teaching visibility must not grant school management")
	}
	args, err := (ExamCollectionAccess{Scope: &scope, Management: true}).SQLArgs("tenant")
	if err != nil || len(args[2].([]string)) != 0 || len(args[3].([]string)) != 0 {
		t.Fatalf("management list retained navigation filters: %#v %v", args, err)
	}
	store := NewMemoryStore()
	store.AddResourceBoundary("exam", "exam-b", boundary)
	mux := http.NewServeMux()
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		mux.Handle(method+" /exams/{id}", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(method, "/exams/exam-b", nil)
		req = req.WithContext(WithAccessScope(req.Context(), scope))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		want := http.StatusForbidden
		if method == http.MethodGet {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Fatalf("%s returned %d: %s", method, rec.Code, rec.Body.String())
		}
	}
	boundary.SchoolID = "school-a"
	if !AllowsManagedResourceBoundary(scope, boundary) {
		t.Fatal("managed school was rejected")
	}
	boundary.TenantID = "other"
	if AllowsManagedResourceBoundary(scope, boundary) {
		t.Fatal("cross tenant boundary was accepted")
	}
}

func TestAuditWorkerGuardRejectsHumanPermission(t *testing.T) {
	for _, role := range []string{"school_admin", "teacher", "page_processing_worker", "subjective_grading_worker"} {
		req := httptest.NewRequest(http.MethodPost, "/internal/worker", nil)
		req = req.WithContext(WithUser(req.Context(), User{ID: "actor", Roles: []string{role}, Permissions: []string{"ocr:manage"}}))
		rec := httptest.NewRecorder()
		RequireServiceUser(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
		want := http.StatusForbidden
		if role == "page_processing_worker" || role == "subjective_grading_worker" {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Fatalf("%s returned %d", role, rec.Code)
		}
	}
}

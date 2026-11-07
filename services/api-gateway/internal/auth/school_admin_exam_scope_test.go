package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestSchoolAdministratorDiscardsForeignNavigationAndTaskWhitelists(t *testing.T) {
	user := User{ID: "mixed", TenantID: "tenant", Roles: []string{"school_admin", "grader", "arbitrator", "teacher"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school", "school_id": "own"},
		"grader":       map[string]any{"scope": "exam_task", "school_id": "foreign", "exam_id": "foreign-exam", "review_task_id": "foreign-review"},
		"arbitrator":   map[string]any{"scope": "exam_task", "arbitration_task_id": "foreign-arbitration"},
		"teacher":      map[string]any{"scope": "class", "class_id": "foreign-class", "grade_id": "foreign-grade"},
	}}
	scope, err := ResolveDeclaredAccessScope(user)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.RestrictsToManagedSchools() || !reflect.DeepEqual(scope.OrganizationScope().SchoolIDs, []string{"own"}) || scope.AllowsSchool("foreign") || scope.AllowsExam("foreign-exam") || scope.AllowsReviewTask("foreign-review") || scope.AllowsArbitrationTask("foreign-arbitration") || scope.AllowsClass("foreign-class") || scope.AllowsGrade("foreign-grade") {
		t.Fatalf("foreign role projection survived: %#v", scope)
	}
	// 即使下游错误保留旧白名单，可信学校边界仍须先于个人分派和显式资源放行。
	scope.ExamIDs, scope.SubmissionIDs, scope.FileIDs, scope.ReviewTaskIDs, scope.ArbitrationTaskIDs = []string{"foreign-exam"}, []string{"foreign-submission"}, []string{"foreign-file"}, []string{"foreign-review"}, []string{"foreign-arbitration"}
	for _, resource := range []string{"exam", "question", "submission", "submission_page", "answer_segment", "file_asset", "review_task", "arbitration_task", "appeal", "seed_task"} {
		boundary := ResourceBoundary{ResourceType: resource, ResourceID: "foreign-file", TenantID: "tenant", SchoolID: "foreign", ExamID: "foreign-exam", SubmissionID: "foreign-submission", AssignedTo: user.ID}
		if AllowsResourceBoundary(scope, boundary) {
			t.Fatalf("%s bypassed school boundary", resource)
		}
		boundary.SchoolID = "own"
		if !AllowsResourceBoundary(scope, boundary) {
			t.Fatalf("%s own school unexpectedly denied", resource)
		}
	}
	args, err := (ExamCollectionAccess{Scope: &scope}).SQLArgs("tenant")
	if err != nil || !reflect.DeepEqual(args, []any{false, []string{"own"}, []string{}, []string{}}) {
		t.Fatalf("foreign collection projection survived: %v %v", args, err)
	}
	ordinary := user
	ordinary.Roles, ordinary.DataScope = []string{"grader"}, map[string]any{"grader": map[string]any{"scope": "exam_task", "exam_id": "foreign-exam", "review_task_id": "foreign-review"}}
	workerScope, err := ResolveDeclaredAccessScope(ordinary)
	if err != nil || workerScope.RestrictsToManagedSchools() || !workerScope.AllowsExam("foreign-exam") || !workerScope.AllowsReviewTask("foreign-review") {
		t.Fatalf("ordinary assigned grader changed: %#v %v", workerScope, err)
	}
	user.Roles = append(user.Roles, "auditor")
	user.DataScope["auditor"] = map[string]any{"scope": "tenant"}
	mixedAuditScope, err := ResolveDeclaredAccessScope(user)
	if err != nil || !mixedAuditScope.RestrictsToManagedSchools() || mixedAuditScope.TenantWide || mixedAuditScope.AllowsSchool("foreign") {
		t.Fatalf("auditor role expanded school administrator access: %#v %v", mixedAuditScope, err)
	}
	auditor := User{ID: "auditor", TenantID: "tenant", Roles: []string{"auditor"}, DataScope: map[string]any{"auditor": map[string]any{"scope": "tenant"}}}
	auditScope, err := ResolveDeclaredAccessScope(auditor)
	if err != nil || !auditScope.TenantWide || auditScope.RestrictsToManagedSchools() {
		t.Fatalf("standalone institutional auditor changed: %#v %v", auditScope, err)
	}
	user.Roles = append(user.Roles, "tenant_admin")
	user.DataScope["tenant_admin"] = map[string]any{"scope": "tenant"}
	tenantScope, err := ResolveDeclaredAccessScope(user)
	if err != nil || tenantScope.RestrictsToManagedSchools() || !tenantScope.AllowsExam("any") {
		t.Fatalf("tenant administrator changed: %#v %v", tenantScope, err)
	}
}

type schoolHistoryScopeResolver struct {
	*MemoryStore
	calls int
}

func (s *schoolHistoryScopeResolver) CanReadSubmittedReviewTask(context.Context, string, string, string) (bool, error) {
	s.calls++
	return true, nil
}

func TestSchoolAdministratorCannotUseSubmittedHistoryFallbackOutsideSchool(t *testing.T) {
	resolver := &schoolHistoryScopeResolver{MemoryStore: NewMemoryStore()}
	for _, school := range []string{"own", "foreign"} {
		resolver.AddResourceBoundary("review_task", school, ResourceBoundary{TenantID: "tenant", SchoolID: school, ExamID: school + "-exam", AssignedTo: "mixed"})
	}
	scope, err := ResolveDeclaredAccessScope(User{ID: "mixed", TenantID: "tenant", Roles: []string{"school_admin", "grader"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "own"}, "grader": map[string]any{"scope": "exam_task"}}})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/review-tasks/{id}/segment-image", RequireRequestResourceBoundary(resolver)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	for _, tc := range []struct {
		school string
		want   int
	}{{"foreign", http.StatusForbidden}, {"own", http.StatusNoContent}} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/"+tc.school+"/segment-image", nil)
		req = req.WithContext(WithAccessScope(req.Context(), scope))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.school, rec.Code, rec.Body.String())
		}
	}
	if resolver.calls != 0 {
		t.Fatal("foreign historical read reached permissive fallback")
	}
	ordinary := AccessScope{TenantID: "tenant", ActorID: "mixed", AssignedOnly: true}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/review-tasks/foreign/segment-image", nil)
	req = req.WithContext(WithAccessScope(req.Context(), ordinary))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || resolver.calls != 1 {
		t.Fatalf("ordinary grader lost submitted history: %d calls=%d", rec.Code, resolver.calls)
	}
}

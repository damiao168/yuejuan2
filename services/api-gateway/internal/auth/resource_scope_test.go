package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRequestResourceBoundaryRejectsKnownCrossSchoolIDs(t *testing.T) {
	store := NewMemoryStore()
	store.AddResourceBoundary("exam", "exam-a", ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-a", ExamID: "exam-a"})
	store.AddResourceBoundary("exam", "exam-b", ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-b", ExamID: "exam-b"})
	mux := http.NewServeMux()
	mux.Handle("GET /exams/{id}", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	scope := AccessScope{TenantID: "tenant-1", ActorID: "admin-a", SchoolIDs: []string{"school-a"}, schoolWide: true}

	for path, want := range map[string]int{"/exams/exam-a": http.StatusOK, "/exams/exam-b": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(WithAccessScope(req.Context(), scope))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s expected %d, got %d: %s", path, want, rec.Code, rec.Body.String())
		}
	}
}

func TestAssignedTaskBoundaryRequiresConcreteAssignment(t *testing.T) {
	store := NewMemoryStore()
	store.AddResourceBoundary("review_task", "task-other", ResourceBoundary{TenantID: "tenant-1", ExamID: "exam-1", AssignedTo: "other"})
	mux := http.NewServeMux()
	mux.Handle("GET /review-tasks/{id}", RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })))
	req := httptest.NewRequest(http.MethodGet, "/review-tasks/task-other", nil)
	req = req.WithContext(WithAccessScope(req.Context(), AccessScope{TenantID: "tenant-1", ActorID: "grader-1", AssignedOnly: true}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unassigned task expected 403, got %d", rec.Code)
	}
}

func TestClassScopedTeacherCannotReadUnassignedTaskInRelatedExam(t *testing.T) {
	scope := AccessScope{TenantID: "tenant-1", ActorID: "teacher-1", ClassIDs: []string{"class-1"}, ExamIDs: []string{"exam-1"}}
	boundary := ResourceBoundary{ResourceType: "review_task", ResourceID: "task-other", TenantID: "tenant-1", ExamID: "exam-1", ClassIDs: []string{"class-1"}, AssignedTo: "other"}
	if AllowsResourceBoundary(scope, boundary) {
		t.Fatal("class/exam relationship must not replace concrete review-task assignment")
	}
}

func TestClassScopedTeacherCannotReadSameSchoolDifferentClassResource(t *testing.T) {
	scope := AccessScope{
		TenantID: "tenant-1", ActorID: "teacher-1",
		SchoolIDs: []string{"school-1"}, GradeIDs: []string{"grade-1"}, ClassIDs: []string{"class-1"},
	}
	otherClassStudent := ResourceBoundary{
		ResourceType: "student", ResourceID: "student-2", TenantID: "tenant-1",
		SchoolID: "school-1", GradeID: "grade-1", ClassIDs: []string{"class-2"},
	}
	if AllowsResourceBoundary(scope, otherClassStudent) {
		t.Fatal("navigation school/grade ids must not grant access to another class")
	}
	ownClassStudent := otherClassStudent
	ownClassStudent.ResourceID = "student-1"
	ownClassStudent.ClassIDs = []string{"class-1"}
	if !AllowsResourceBoundary(scope, ownClassStudent) {
		t.Fatal("assigned class should still grant access to its students")
	}
}

func TestSchoolWideManagerCanReadTaskOwnedByTheirSchool(t *testing.T) {
	scope := AccessScope{TenantID: "tenant-1", ActorID: "admin-1", SchoolIDs: []string{"school-1"}, schoolWide: true}
	boundary := ResourceBoundary{ResourceType: "review_task", ResourceID: "task-1", TenantID: "tenant-1", SchoolID: "school-1", AssignedTo: "grader-1"}
	if !AllowsResourceBoundary(scope, boundary) {
		t.Fatal("trusted school-wide manager should manage tasks in their school")
	}
}

func TestAssignedScopeCannotUseExamExpansionForSiblingSubmission(t *testing.T) {
	scope := AccessScope{
		TenantID:      "tenant-1",
		ActorID:       "grader-1",
		AssignedOnly:  true,
		ExamIDs:       []string{"exam-1"},
		SubmissionIDs: []string{"submission-assigned"},
	}
	assigned := ResourceBoundary{ResourceType: "answer_segment", TenantID: "tenant-1", ExamID: "exam-1", SubmissionID: "submission-assigned"}
	sibling := ResourceBoundary{ResourceType: "answer_segment", TenantID: "tenant-1", ExamID: "exam-1", SubmissionID: "submission-other"}
	if !AllowsResourceBoundary(scope, assigned) {
		t.Fatal("assigned submission descendant should be accessible")
	}
	if AllowsResourceBoundary(scope, sibling) {
		t.Fatal("exam expansion must not expose an unassigned sibling submission")
	}
}

func TestDomainDirectIDSegmentsHaveBoundaryResolvers(t *testing.T) {
	want := map[string]string{
		"exams": "exam", "questions": "question", "paper-imports": "paper_import",
		"answer-sheet-templates": "answer_sheet_template", "files": "file_asset",
		"submissions": "submission", "submission-pages": "submission_page",
		"capture-batches": "capture_batch", "capture-pages": "capture_page",
		"page-registration-runs": "page_registration_run", "page-registration-corrections": "page_registration_correction",
		"review-tasks": "review_task", "arbitration-tasks": "arbitration_task",
		"ocr-tasks": "ocr_task", "omr-calibrations": "omr_calibration", "ai-grades": "ai_grade",
		"subjective-grading-batches": "subjective_grading_batch", "annotations": "review_annotation",
		"orchestrations": "orchestration_run", "agent-tasks": "agent_task",
		"answer-groups": "answer_group", "backmark-batches": "backmark_batch", "backmark-items": "backmark_item",
		"calibration-sessions": "grader_calibration_session", "gold-papers": "gold_paper",
		"grading-quality-incidents": "grading_quality_incident", "exceptions": "processing_exception",
		"regrade-jobs": "regrade_job", "regrade-items": "regrade_item",
		"release-gate-evidence": "release_gate_evidence", "release-gate-waivers": "release_gate_waiver",
		"uploads": "capture_upload", "math-understanding": "math_understanding",
		"ai-human-disagreements": "ai_human_disagreement",
	}
	for segment, resourceType := range want {
		if got := resourceSegmentTypes[segment]; got != resourceType {
			t.Fatalf("direct-ID segment %q must resolve as %q, got %q", segment, resourceType, got)
		}
		if query := resourceBoundaryQuery(resourceType); query == "" {
			t.Fatalf("resource type %q has no PostgreSQL boundary query", resourceType)
		}
	}
}

func TestSchoolScopeRejectsKnownForeignDirectIDsAcrossCoreResources(t *testing.T) {
	store := NewMemoryStore()
	mux := http.NewServeMux()
	resources := []struct {
		segment, resourceType string
	}{
		{"exams", "exam"}, {"questions", "question"}, {"paper-imports", "paper_import"},
		{"files", "file_asset"}, {"submissions", "submission"}, {"capture-batches", "capture_batch"},
	}
	for _, resource := range resources {
		store.AddResourceBoundary(resource.resourceType, "resource-a", ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-a", ExamID: "exam-a"})
		store.AddResourceBoundary(resource.resourceType, "resource-b", ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-b", ExamID: "exam-b"})
		pattern := "GET /" + resource.segment + "/{id}"
		mux.Handle(pattern, RequireRequestResourceBoundary(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})))
	}
	scope := AccessScope{TenantID: "tenant-1", ActorID: "admin-a", SchoolIDs: []string{"school-a"}, schoolWide: true}
	for _, resource := range resources {
		for suffix, want := range map[string]int{"resource-a": http.StatusOK, "resource-b": http.StatusForbidden} {
			path := "/" + resource.segment + "/" + suffix
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req = req.WithContext(WithAccessScope(req.Context(), scope))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != want {
				t.Fatalf("%s expected %d, got %d: %s", path, want, rec.Code, rec.Body.String())
			}
		}
	}
}

func TestRegisteredDirectIDRoutesDeclareResourceBoundaryOrExplicitTenantScope(t *testing.T) {
	routePattern := regexp.MustCompile(`"(?:GET|POST|PUT|PATCH|DELETE) (/api/v1/[^" ]*\{[^"}]+\}[^" ]*)"`)
	tenantScopedPrefixes := []string{
		"/api/v1/auth/sessions/", "/api/v1/users/", "/api/v1/tenants/",
		"/api/v1/exam-sessions/commands/",
		// Command recovery resources are not target-resource IDs. Their stores
		// require the authenticated tenant and actor together with command ID.
		"/api/v1/subjective-grading-batch-commands/", "/api/v1/review-commands/",
		"/api/v1/score-commands/", "/api/v1/report-commands/",
		"/api/v1/review/comment-templates/", "/api/v1/model-", "/api/v1/grading-evaluations/",
		"/api/v1/ai-eligibility/", "/api/v1/platform/model-api-configs/",
		// Platform-school routes intentionally address another tenant. Their
		// dedicated guard requires a platform-tenant platform_admin plus
		// tenant:manage before the cross-tenant read model is queried.
		"/api/v1/platform/schools/",
		// Reusable items have no exam ancestry. questionbank stores check the
		// authenticated tenant, school scope and explicit bank action ACL on
		// every read/write AND durable receipt replay (real PostgreSQL E2E).
		"/api/v1/question-banks/", "/api/v1/question-bank/items/", "/api/v1/question-bank/versions/", "/api/v1/question-bank/rubric-templates",
	}
	var uncovered []string
	err := filepath.WalkDir("..", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range routePattern.FindAllStringSubmatch(string(raw), -1) {
			route := match[1]
			if strings.HasPrefix(route, "/api/v1/internal/") || hasAnyPrefix(route, tenantScopedPrefixes) || routeHasRecognizedBoundary(route) {
				continue
			}
			uncovered = append(uncovered, path+": "+route)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(uncovered) > 0 {
		t.Fatalf("direct-ID routes must declare a resource boundary or an explicit tenant-scoped classification:\n%s", strings.Join(uncovered, "\n"))
	}
}

func routeHasRecognizedBoundary(route string) bool {
	parts := strings.Split(strings.Trim(route, "/"), "/")
	for index, part := range parts {
		if index > 0 && strings.HasPrefix(part, "{") {
			if _, ok := resourceSegmentTypes[parts[index-1]]; ok {
				return true
			}
		}
	}
	return false
}

func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

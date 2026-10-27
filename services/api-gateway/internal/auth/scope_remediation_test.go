package auth

import (
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestSchoolManagementDoesNotExpandForeignAssignedSubmission(t *testing.T) {
	// 实际范围派生读取本校与外校分派。只读考试材料仍可用，答卷必须区分所属学校和具体分派。
	user := User{ID: "hybrid", TenantID: "tenant", Roles: []string{"school_admin", "grader", "teacher"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school", "school_id": "school-a"},
		"grader":       map[string]any{"scope": "exam_task"},
		"teacher":      map[string]any{"scope": "class", "class_id": "class-b"},
	}}
	store, script := openAuthBatch49Store(t, []authBatch49Step{
		{kind: "query", fragment: "WITH scoped_school", args: []any{"tenant", "hybrid", "school-a"}, columns: 4, rows: [][]driver.Value{{"{school-a}", "{}", "{class-a}", "{exam-a}"}}},
		{kind: "query", fragment: "WITH scoped_class", args: []any{"tenant", "hybrid", "class-b,class-a"}, columns: 4, rows: [][]driver.Value{{"{school-b}", "{grade-b}", "{class-b}", "{exam-b}"}}},
		{kind: "query", fragment: "FROM review_task rt", args: []any{"tenant", "hybrid"}, columns: 4, rows: [][]driver.Value{{"{task-b}", "{}", "{exam-b}", "{submission-assigned}"}}},
	})
	scope, err := store.ResolveAccessScope(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	assertAuthBatch49Script(t, script, "")
	if !reflect.DeepEqual(scope.ManagedSchoolIDs(), []string{"school-a"}) || !scope.AllowsSchool("school-b") || !scope.AllowsExam("exam-b") {
		t.Fatalf("management source or navigation scope lost: %#v", scope)
	}
	for _, resource := range []string{"submission", "submission_page", "answer_segment", "file_asset", "ai_human_disagreement"} {
		for _, tc := range []struct {
			name, school, exam, submission string
			want                           bool
		}{
			{"own school", "school-a", "exam-a", "own-school-unassigned", true},
			{"foreign assignment", "school-b", "exam-b", "submission-assigned", true},
			{"foreign sibling", "school-b", "exam-b", "submission-other", false},
		} {
			boundary := ResourceBoundary{ResourceType: resource, TenantID: "tenant", SchoolID: tc.school, ExamID: tc.exam, SubmissionID: tc.submission, ClassIDs: []string{"class-b"}}
			if got := AllowsResourceBoundary(scope, boundary); got != tc.want {
				t.Fatalf("%s %s allowed=%v want=%v", resource, tc.name, got, tc.want)
			}
		}
	}
	if !AllowsResourceBoundary(scope, ResourceBoundary{ResourceType: "question", TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b"}) {
		t.Fatal("assigned exam materials must stay readable")
	}
	if AllowsResourceBoundary(scope, ResourceBoundary{ResourceType: "review_task", ResourceID: "task-other", TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b", AssignedTo: "other"}) {
		t.Fatal("foreign unassigned task inherited local school management")
	}
	if !AllowsResourceBoundary(scope, ResourceBoundary{ResourceType: "review_task", ResourceID: "task-b", TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b", AssignedTo: user.ID}) {
		t.Fatal("concrete foreign assignment must stay readable")
	}
	managed := scope.ManagedSchoolIDs()
	managed[0] = "school-b"
	if scope.managesSchool("school-b") {
		t.Fatal("caller mutated management scope through accessor")
	}
}

func TestMemorySchoolScopeUsesStoredSchoolWithoutUpgradingOtherRoleNavigation(t *testing.T) {
	store := NewMemoryStore()
	user := User{ID: "manager", TenantID: "tenant", Roles: []string{"school_admin", "grader"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school"},
		"grader":       map[string]any{"scope": "exam_task", "school_id": "school-b", "exam_id": "exam-b"},
	}}
	store.AddUser(UserWithPassword{User: user, SchoolID: "school-a"})
	scope, err := store.ResolveAccessScope(t.Context(), user)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.managesSchool("school-a") || scope.managesSchool("school-b") || !scope.AllowsSchool("school-b") {
		t.Fatalf("navigation and school authority merged: %#v", scope)
	}
	args, err := (ExamCollectionAccess{Scope: &scope}).SQLArgs("tenant")
	if err != nil || !reflect.DeepEqual(args[1], []string{"school-a"}) {
		t.Fatalf("collection schools=%v err=%v", args, err)
	}
	boundary := ResourceBoundary{ResourceType: "submission", TenantID: "tenant", SchoolID: "school-b", ExamID: "exam-b", SubmissionID: "unassigned"}
	if AllowsResourceBoundary(scope, boundary) {
		t.Fatal("foreign exam navigation must not expose sibling submissions")
	}
	scope.TenantWide = true
	if !AllowsResourceBoundary(scope, boundary) {
		t.Fatal("tenant-wide control lost access")
	}
}

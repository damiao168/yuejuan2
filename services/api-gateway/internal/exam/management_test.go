package exam

import (
	"context"
	"errors"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func managementFixture(t *testing.T) (*MemoryStore, auth.AccessScope, ExamSession) {
	t.Helper()
	s := NewMemoryStore()
	scope := auth.AccessScope{TenantID: "tenant-management", TenantWide: true}
	parent, err := s.CreateExamSession(context.Background(), scope, "admin", CreateSessionInput{
		SchoolID: "school", GradeID: "grade", Name: "高一期中考试", ExamType: "midterm_exam", GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval",
		ClassIDs: []string{"class-a"}, Subjects: []SessionSubjectInput{
			{Subject: "math", TotalScore: 150, DurationMinutes: 120},
			{Subject: "chinese", TotalScore: 150, DurationMinutes: 120},
			{Subject: "english", TotalScore: 150, DurationMinutes: 120, CandidateRule: "subject_selected_classes", ClassIDs: []string{"class-b"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, scope, parent
}

func managementRevisions(item ManagementExam) map[string]int64 {
	values := map[string]int64{}
	for _, child := range item.Exams {
		values[child.ID] = child.Revision
	}
	return values
}

func TestManagementListPagesWholeGroupsAndFilterDoesNotTrimSubjects(t *testing.T) {
	s, scope, parent := managementFixture(t)
	standalone, err := s.CreateExam(context.Background(), scope, "admin", CreateInput{SchoolID: "school", Name: "历史单科考试", Subject: "math", ExamType: "unit_test", TotalScore: 100, GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval"})
	if err != nil {
		t.Fatal(err)
	}
	// 快速创建在 Windows 上可能获得相同时间；固定先后以验证分页而不是系统时钟精度。
	s.mu.Lock()
	standalone.CreatedAt = parent.CreatedAt.Add(time.Second)
	s.items[standalone.ID] = standalone
	s.mu.Unlock()
	if err := s.SetStatusForTest(scope.TenantID, parent.Exams[0].ID, "grading"); err != nil {
		t.Fatal(err)
	}
	items, err := s.ListManagementExams(context.Background(), scope, ManagementListFilter{Status: "ongoing", Limit: 1})
	if err != nil || len(items) != 1 || items[0].ID != parent.ID || len(items[0].Exams) != 3 {
		t.Fatalf("filtered whole group lost subjects: %#v %v", items, err)
	}
	all, err := s.ListManagementExams(context.Background(), scope, ManagementListFilter{Limit: 1})
	if err != nil || len(all) != 1 || all[0].Kind != "standalone" {
		t.Fatalf("first page: %#v %v", all, err)
	}
	second, err := s.ListManagementExams(context.Background(), scope, ManagementListFilter{Limit: 1, CursorAt: all[0].CreatedAt, CursorID: all[0].ID})
	if err != nil || len(second) != 1 || second[0].ID != parent.ID || len(second[0].Exams) != 3 {
		t.Fatalf("second page: %#v %v", second, err)
	}
}

func TestManagementPartialScopeCannotSeeSiblingsOrMutateWholeExam(t *testing.T) {
	s, scope, parent := managementFixture(t)
	// 学科身份携带解析后的学校 ID，也不能借此读取同场其他学科。
	restricted := auth.AccessScope{TenantID: scope.TenantID, SchoolIDs: []string{"school"}, ExamIDs: []string{parent.Exams[0].ID}}
	item, err := s.GetManagementExam(context.Background(), restricted, "session", parent.ID)
	if err != nil || len(item.Exams) != 1 || item.ScopeComplete || item.CanManage {
		t.Fatalf("partial scope: %#v %v", item, err)
	}
	name := "不应生效"
	_, err = s.UpdateManagementExam(context.Background(), restricted, "session", parent.ID, ManagementUpdateInput{Name: &name, ExpectedRevision: item.Revision, ExpectedSubjectRevisions: managementRevisions(item)})
	if !errors.Is(err, ErrScopeForbidden) {
		t.Fatalf("expected whole-scope denial, got %v", err)
	}
	_, err = s.GetManagementExam(context.Background(), auth.AccessScope{TenantID: "other", TenantWide: true}, "session", parent.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross tenant detail: %v", err)
	}
}

func TestManagementDerivedExamIDsDoNotGrantAllCandidateClasses(t *testing.T) {
	s, scope, parent := managementFixture(t)
	// 授权解析会把班级关联的考试加到 ExamIDs；能看到全部科目仍不等于能修改其余班级。
	ids := []string{}
	for _, child := range parent.Exams {
		ids = append(ids, child.ID)
	}
	classScope := auth.AccessScope{TenantID: scope.TenantID, SchoolIDs: []string{"school"}, ClassIDs: []string{"class-a"}, ExamIDs: ids}
	item, err := s.GetManagementExam(context.Background(), classScope, "session", parent.ID)
	if err != nil || !item.ScopeComplete || item.CanManage {
		t.Fatalf("derived exam IDs expanded mutation scope: %#v %v", item, err)
	}
}

func TestManagementClassScopeCannotAddUnassignedSchoolClass(t *testing.T) {
	s, scope, parent := managementFixture(t)
	classScope := auth.AccessScope{TenantID: scope.TenantID, SchoolIDs: []string{"school"}, ClassIDs: []string{"class-a", "class-b"}}
	item, err := s.GetManagementExam(context.Background(), classScope, "session", parent.ID)
	if err != nil || !item.CanManage {
		t.Fatalf("fixture class scope: %#v %v", item, err)
	}
	classes := []string{"class-outside"}
	_, err = s.UpdateManagementExam(context.Background(), classScope, "session", parent.ID, ManagementUpdateInput{ClassIDs: &classes, ExpectedRevision: item.Revision, ExpectedSubjectRevisions: managementRevisions(item)})
	if !errors.Is(err, ErrScopeForbidden) {
		t.Fatalf("derived SchoolIDs admitted unassigned class: %v", err)
	}
}

func TestManagementEditUpdatesInheritedScopesAndPreservesIndependentSubject(t *testing.T) {
	s, scope, parent := managementFixture(t)
	item, err := s.GetManagementExam(context.Background(), scope, "session", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	name, classes := "高一月考", []string{"class-c", "class-c"}
	updated, err := s.UpdateManagementExam(context.Background(), scope, "session", parent.ID, ManagementUpdateInput{Name: &name, ClassIDs: &classes, ExpectedRevision: item.Revision, ExpectedSubjectRevisions: managementRevisions(item)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != name || updated.Revision != item.Revision+1 || len(updated.InheritedSubjectIDs) != 2 {
		t.Fatalf("parent update: %#v", updated)
	}
	for _, child := range updated.Exams {
		want := []string{"class-c"}
		if child.Subject == "english" {
			want = []string{"class-b"}
		}
		if !classIDSetsEqual(child.ClassIDs, want) {
			t.Fatalf("%s classes changed incorrectly: %#v", child.Subject, child.ClassIDs)
		}
		if child.SessionName != name {
			t.Fatalf("child display uses stale parent name: %#v", child)
		}
	}
}

func TestManagementFrozenSubjectRollsBackAllScopesAndStaleChildVersion(t *testing.T) {
	s, scope, parent := managementFixture(t)
	before, err := s.GetManagementExam(context.Background(), scope, "session", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetStatusForTest(scope.TenantID, parent.Exams[1].ID, "ready"); err != nil {
		t.Fatal(err)
	}
	classes := []string{"class-c"}
	_, err = s.UpdateManagementExam(context.Background(), scope, "session", parent.ID, ManagementUpdateInput{ClassIDs: &classes, ExpectedRevision: before.Revision, ExpectedSubjectRevisions: managementRevisions(before)})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("unchanged parent must not hide child conflict: %v", err)
	}
	current, err := s.GetManagementExam(context.Background(), scope, "session", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateManagementExam(context.Background(), scope, "session", parent.ID, ManagementUpdateInput{ClassIDs: &classes, ExpectedRevision: current.Revision, ExpectedSubjectRevisions: managementRevisions(current)})
	if !errors.Is(err, ErrCandidatesFrozen) {
		t.Fatalf("expected frozen denial: %v", err)
	}
	after, err := s.GetManagementExam(context.Background(), scope, "session", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != current.Revision || !classIDSetsEqual(after.ClassIDs, current.ClassIDs) {
		t.Fatalf("failed edit partially committed: %#v", after)
	}
}

func TestManagementProgressUsesConfirmedFactsAndReportsMissingPublication(t *testing.T) {
	exams := []Exam{{ID: "a", Status: "ready"}, {ID: "b", Status: "configured"}}
	facts := map[string]managementSubjectFacts{"a": {Ready: true, Available: true}, "b": {Available: true}}
	p := managementProgress(exams, facts, false)
	if p.Status != "configured" || p.ReadyCount != 1 || p.Label != "准备中 · 1/2 科准备完成" {
		t.Fatalf("readiness aggregation: %#v", p)
	}
	exams[0].Status = "published"
	facts["a"] = managementSubjectFacts{Available: true}
	p = managementProgress(exams, facts, false)
	if p.Status != "unavailable" || p.PublishedCount != 0 {
		t.Fatalf("status-only publication fabricated a release: %#v", p)
	}
	facts["a"] = managementSubjectFacts{Published: true, Available: true}
	p = managementProgress(exams, facts, false)
	if p.Status != "partially_published" || p.PublishedCount != 1 {
		t.Fatalf("real publication: %#v", p)
	}
	p = managementProgress([]Exam{{ID: "x", Status: "ready"}}, nil, false)
	if p.Status != "unavailable" || p.FactsAvailable {
		t.Fatalf("unavailable facts fabricated readiness: %#v", p)
	}
}

func TestLegacyArchivedManagementCannotEditOrRestore(t *testing.T) {
	s, scope, parent := managementFixture(t)
	for _, child := range parent.Exams {
		if err := s.SetStatusForTest(scope.TenantID, child.ID, "archived"); err != nil {
			t.Fatal(err)
		}
	}
	item, err := s.GetManagementExam(context.Background(), scope, "session", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if item.CanEdit || item.CanRestore || item.Progress.Status != "archived" {
		t.Fatalf("legacy archive incorrectly editable: %#v", item)
	}
}

func TestGenericStatusStoreCannotCreateLegacySubjectArchive(t *testing.T) {
	s, scope, parent := managementFixture(t)
	child := parent.Exams[0]
	_, err := s.UpdateStatus(context.Background(), scope, child.ID, "archived", child.Revision)
	if !errors.Is(err, ErrManagementArchiveRequired) {
		t.Fatalf("generic status bypassed whole-exam archive: %v", err)
	}
	current, err := s.GetExam(context.Background(), scope, child.ID)
	if err != nil || current.Status != child.Status || current.Revision != child.Revision {
		t.Fatalf("rejected archive changed stage/version: %#v %v", current, err)
	}
}

package exam

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func examBatch49Row(id, name, status string, revision int64) []driver.Value {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	return []driver.Value{id, "tenant", "school", name, "math", "midterm_exam", float64(100), status, "ai_assisted", true, "manual", "actor", revision, now, now}
}

func examBatch49LockedSteps(status string) []examBatch29Step {
	return []examBatch29Step{
		{kind: "query", fragment: "FOR UPDATE", columns: 15, args: map[int]any{0: "tenant", 5: "exam"}, rows: [][]driver.Value{examBatch49Row("exam", "before", status, 9)}},
		{kind: "query", fragment: "SELECT class_id::text FROM exam_class", columns: 1, args: map[int]any{0: "tenant", 1: "exam"}, rows: [][]driver.Value{{"class"}}},
	}
}

func examBatch49SchoolStep() examBatch29Step {
	return examBatch29Step{kind: "query", fragment: "FROM school", columns: 1, args: map[int]any{0: "tenant", 1: "school"}, rows: [][]driver.Value{{"school"}}}
}

func TestBatch49ExamUpdateFieldsKeepsChangedColumnAndArgumentOrder(t *testing.T) {
	current := Exam{SchoolID: "school", Name: "before", Subject: "math", ExamType: "midterm_exam", TotalScore: 100, GradingMode: "ai_assisted", AppealEnabled: true, PublishPolicy: "manual"}
	merged := Exam{SchoolID: "next-school", Name: "after", Subject: "english", ExamType: "final_exam", TotalScore: 150, GradingMode: "manual", AppealEnabled: false, PublishPolicy: "automatic"}
	clauses, args := examUpdateFields(current, merged, "tenant", "exam", 9)
	wantClauses := []string{"school_id = $3", "name = $4", "subject = $5", "exam_type = $6", "total_score = $7", "grading_mode = $8", "appeal_enabled = $9", "publish_policy = $10", "revision = revision + 1", "updated_at = now()"}
	wantArgs := []any{"tenant", "exam", "next-school", "after", "english", "final_exam", float64(150), "manual", false, "automatic", int64(9)}
	if !reflect.DeepEqual(clauses, wantClauses) || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("clauses=%v args=%v", clauses, args)
	}
	clauses, args = examUpdateFields(current, current, "tenant", "exam", 9)
	if !reflect.DeepEqual(clauses, []string{"revision = revision + 1", "updated_at = now()"}) || !reflect.DeepEqual(args, []any{"tenant", "exam", int64(9)}) {
		t.Fatalf("unchanged fields were written: clauses=%v args=%v", clauses, args)
	}
}

func TestBatch49PostgresUpdateKeepsEquivalentFrozenRosterWithoutRefresh(t *testing.T) {
	steps := append(examBatch49LockedSteps("ready"), examBatch49SchoolStep(), examBatch29Step{
		kind: "query", fragment: "SET name = $3, revision = revision + 1, updated_at = now()", columns: 15,
		args: map[int]any{0: "tenant", 1: "exam", 2: "after", 3: int64(9)}, rows: [][]driver.Value{examBatch49Row("exam", "after", "ready", 10)},
	})
	store, script := openExamBatch29Store(t, steps)
	name, classes := "after", []string{"class", "class"}
	got, err := store.UpdateExam(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "exam", UpdateInput{Name: &name, ClassIDs: &classes, ExpectedRevision: 9})
	if err != nil || got.Name != name || got.Revision != 10 || got.Status != "ready" || !reflect.DeepEqual(got.ClassIDs, classes) {
		t.Fatalf("update=%+v error=%v", got, err)
	}
	classes[0] = "mutated"
	if got.ClassIDs[0] != "class" {
		t.Fatal("returned class slice aliases caller input")
	}
	assertExamBatch29Script(t, script, "commit")
}

func TestBatch49PostgresUpdateRejectsBeforeRosterWrites(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		revision int64
		classes  []string
		scope    auth.AccessScope
		late     bool
		want     error
	}{
		{"stale before update", "draft", 8, []string{"class"}, auth.AccessScope{TenantID: "tenant", TenantWide: true}, false, ErrRevisionConflict},
		{"frozen roster", "ready", 9, []string{"other"}, auth.AccessScope{TenantID: "tenant", TenantWide: true}, false, ErrCandidatesFrozen},
		{"merged classes outside scope", "draft", 9, []string{"other"}, auth.AccessScope{TenantID: "tenant", ClassIDs: []string{"class"}}, false, ErrScopeForbidden},
		{"stale update result", "draft", 9, []string{"class"}, auth.AccessScope{TenantID: "tenant", TenantWide: true}, true, ErrRevisionConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := examBatch49LockedSteps(tc.status)
			if tc.late {
				steps = append(steps, examBatch49SchoolStep(), examBatch29Step{kind: "query", fragment: "UPDATE exam", columns: 15, args: map[int]any{0: "tenant", 1: "exam", 2: "after", 3: int64(9)}})
			}
			store, script := openExamBatch29Store(t, steps)
			name := "after"
			_, err := store.UpdateExam(context.Background(), tc.scope, "exam", UpdateInput{Name: &name, ClassIDs: &tc.classes, ExpectedRevision: tc.revision})
			if !errors.Is(err, tc.want) {
				t.Fatalf("update error=%v, want %v", err, tc.want)
			}
			assertExamBatch29Script(t, script, "rollback")
		})
	}
}

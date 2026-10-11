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

func examBatch49Arguments(values ...any) map[int]any {
	args := make(map[int]any, len(values))
	for index, value := range values {
		args[index] = value
	}
	return args
}

func examBatch49SessionRow(commandID string) []driver.Value {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	return []driver.Value{"session", "tenant", "school", "grade", "", int64(0), "session-name", "midterm_exam", "draft", "ai_assisted", true, "manual", "actor", commandID, int64(1), now, now}
}

func examBatch49CommandLoad(commandID string, rows [][]driver.Value) examBatch29Step {
	return examBatch29Step{kind: "query", fragment: "FROM exam_session", columns: 17, args: examBatch49Arguments("tenant", "actor", commandID), rows: rows}
}

func examBatch49CommandReceipt(commandID, hash string) []examBatch29Step {
	return []examBatch29Step{
		examBatch49CommandLoad(commandID, [][]driver.Value{examBatch49SessionRow(commandID)}),
		{kind: "query", fragment: "LEFT JOIN exam_class ec", columns: 16, args: examBatch49Arguments("tenant", "session")},
		{kind: "query", fragment: "SELECT COALESCE(command_request_hash,'')", columns: 1, args: examBatch49Arguments("tenant", "actor", commandID), rows: [][]driver.Value{{hash}}},
	}
}

func examBatch49SessionInput(commandID string) CreateSessionInput {
	return CreateSessionInput{SchoolID: "school", GradeID: "grade", Name: "session-name", ExamType: "midterm_exam", GradingMode: "ai_assisted", PublishPolicy: "manual", CommandID: commandID}
}

func examBatch49SessionInsert(input CreateSessionInput, hash string, rows [][]driver.Value) examBatch29Step {
	return examBatch29Step{kind: "query", fragment: "INSERT INTO exam_session", columns: 17, args: examBatch49Arguments("tenant", "school", "grade", "", 0, "session-name", "midterm_exam", "ai_assisted", true, "manual", "actor", input.CommandID, hash), rows: rows}
}

func TestBatch49PostgresSessionReplayAndUniqueConflictCheckOriginalHash(t *testing.T) {
	for _, tc := range []examSessionReplayCase{
		{"initial replay", false, false},
		{"initial hash conflict", false, true},
		{"unique conflict recovery", true, false},
		{"unique conflict hash mismatch", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertExamSessionCommandReplayHash(t, tc)
		})
	}
}

func examBatch49SubjectSteps(id, classID, subject, name, rule string, duration int, scanErr error) []examBatch29Step {
	steps := []examBatch29Step{
		{kind: "query", fragment: "SELECT 1 FROM school_class", columns: 1, args: examBatch49Arguments("tenant", classID, "school", "grade"), rows: [][]driver.Value{{int64(1)}}},
		{kind: "query", fragment: "INSERT INTO exam (", columns: 15, args: examBatch49Arguments("tenant", "school", "session", name, subject, "midterm_exam", float64(100), "ai_assisted", true, "manual", "actor", duration, rule), rowsErr: scanErr},
	}
	if scanErr != nil {
		return steps
	}
	steps[1].rows = [][]driver.Value{examBatch49Row(id, name, "draft", 1)}
	return append(steps,
		examBatch29Step{kind: "exec", fragment: "DELETE FROM exam_class", args: examBatch49Arguments("tenant", id)},
		examBatch29Step{kind: "query", fragment: "SELECT id::text, school_id::text", columns: 2, args: examBatch49Arguments("tenant", classID), rows: [][]driver.Value{{classID, "school"}}},
		examBatch29Step{kind: "exec", fragment: "INSERT INTO exam_class", args: examBatch49Arguments("tenant", id, classID)},
		examBatch29Step{kind: "query", fragment: "SELECT status", columns: 1, args: examBatch49Arguments("tenant", id), rows: [][]driver.Value{{"draft"}}},
		examBatch29Step{kind: "exec", fragment: "DELETE FROM exam_candidate_snapshot", args: examBatch49Arguments("tenant", id)},
		examBatch29Step{kind: "exec", fragment: "INSERT INTO exam_candidate_snapshot", args: examBatch49Arguments("tenant", id)})
}

func examBatch49Section(id, title, kind string, count, order int) examBatch29Step {
	return examBatch29Step{kind: "query", fragment: "INSERT INTO exam_blueprint_section", columns: 1, args: examBatch49Arguments("tenant", id, title, kind, count, float64(2), order), rows: [][]driver.Value{{"section-id"}}}
}

func examBatch49Question(id, number, kind string, order int) examBatch29Step {
	return examBatch29Step{kind: "exec", fragment: "INSERT INTO question", args: examBatch49Arguments("tenant", id, number, kind, float64(2), order)}
}

func TestBatch49PostgresSessionKeepsChildRosterBlueprintOrderAndAtomicFailure(t *testing.T) {
	for _, failSecond := range []bool{false, true} {
		name := "all children commit"
		if failSecond {
			name = "later child rolls back entire session"
		}
		t.Run(name, func(t *testing.T) {
			assertExamSessionChildrenAtomicity(t, failSecond)
		})
	}
}

func TestBatch49PostgresSessionKeepsExplicitDisabledAppealForSessionAndChild(t *testing.T) {
	appealEnabled := false
	input := examBatch49SessionInput("")
	input.AppealEnabled = &appealEnabled
	input.ClassIDs = []string{"parent-class"}
	input.Subjects = []SessionSubjectInput{{Subject: "math", TotalScore: 100}}
	hash, err := examSessionCommandHash(input)
	if err != nil {
		t.Fatal(err)
	}
	// 显式 false 必须同时进入考次和子科；不能在解析默认值时改成 true 或改写调用方请求。
	sessionRow := examBatch49SessionRow("")
	sessionRow[10] = false
	insert := examBatch49SessionInsert(input, hash, [][]driver.Value{sessionRow})
	insert.args[8] = false
	childSteps := examBatch49SubjectSteps("math-exam", "parent-class", "math", "session-name · 数学", "all_selected_classes", 0, nil)
	childSteps[1].args[8] = false
	childSteps[1].rows[0][9] = false
	store, script := openExamBatch29Store(t, append([]examBatch29Step{insert}, childSteps...))
	got, err := store.CreateExamSession(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "actor", input)
	if err != nil || got.AppealEnabled || len(got.Exams) != 1 || got.Exams[0].AppealEnabled || input.AppealEnabled != &appealEnabled || appealEnabled {
		t.Fatalf("appeal option changed: session=%+v input=%+v error=%v", got, input, err)
	}
	assertExamBatch29Script(t, script, "commit")
}

type examSessionReplayCase struct {
	name     string
	race     bool
	conflict bool
}

func assertExamSessionCommandReplayHash(t *testing.T, tc examSessionReplayCase) {
	t.Helper()
	input := examBatch49SessionInput("command-49")
	input.Subjects = []SessionSubjectInput{{Subject: "math", TotalScore: 100}}
	hash, err := examSessionCommandHash(input)
	if err != nil {
		t.Fatal(err)
	}
	storedHash := hash
	if tc.conflict {
		storedHash = "another-request"
	}
	steps := []examBatch29Step{}
	if tc.race {
		steps = append(steps, examBatch49CommandLoad(input.CommandID, nil), examBatch49SessionInsert(input, hash, nil))
	}
	steps = append(steps, examBatch49CommandReceipt(input.CommandID, storedHash)...)
	store, script := openExamBatch29Store(t, steps)
	got, err := store.CreateExamSession(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "actor", input)
	if tc.conflict {
		if !errors.Is(err, ErrCommandConflict) || got.ID != "" {
			t.Fatalf("conflict result=%+v error=%v", got, err)
		}
		assertExamBatch29Script(t, script, "rollback")
		return
	}
	if err != nil || got.ID != "session" || len(got.Exams) != 0 || input.AppealEnabled != nil {
		t.Fatalf("replay created children or mutated request: %+v error=%v", got, err)
	}
	assertExamBatch29Script(t, script, "commit")
}

func assertExamSessionChildrenAtomicity(t *testing.T, failSecond bool) {
	t.Helper()
	input := examBatch49SessionInput("")
	input.ClassIDs = []string{"parent-class"}
	input.Subjects = []SessionSubjectInput{
		{Subject: "math", TotalScore: 100, DurationMinutes: 80, Sections: []BlueprintSectionInput{{Title: "first", QuestionType: "choice", QuestionCount: 2, ScorePerQuestion: 2}, {Title: "second", QuestionType: "essay", QuestionCount: 1, ScorePerQuestion: 2}}},
		{Subject: "english", TotalScore: 100, DurationMinutes: 90, CandidateRule: "explicit-rule", ClassIDs: []string{"subject-class"}, Sections: []BlueprintSectionInput{{Title: "third", QuestionType: "essay", QuestionCount: 1, ScorePerQuestion: 2}}},
	}
	hash, err := examSessionCommandHash(input)
	if err != nil {
		t.Fatal(err)
	}
	steps := []examBatch29Step{examBatch49SessionInsert(input, hash, [][]driver.Value{examBatch49SessionRow("")})}
	steps = append(steps, examBatch49SubjectSteps("math-exam", "parent-class", "math", "session-name · 数学", "all_selected_classes", 80, nil)...)
	steps = append(steps, examBatch49Section("math-exam", "first", "choice", 2, 1), examBatch49Question("math-exam", "1", "choice", 1), examBatch49Question("math-exam", "2", "choice", 2), examBatch49Section("math-exam", "second", "essay", 1, 2), examBatch49Question("math-exam", "3", "essay", 3))
	var childErr error
	if failSecond {
		childErr = errors.New("later child insert failed")
	}
	steps = append(steps, examBatch49SubjectSteps("english-exam", "subject-class", "english", "session-name · 英语", "explicit-rule", 90, childErr)...)
	if !failSecond {
		steps = append(steps, examBatch49Section("english-exam", "third", "essay", 1, 1), examBatch49Question("english-exam", "1", "essay", 1))
	}
	store, script := openExamBatch29Store(t, steps)
	got, err := store.CreateExamSession(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "actor", input)
	if failSecond {
		if !errors.Is(err, childErr) || got.ID != "" {
			t.Fatalf("partial session returned: %+v error=%v", got, err)
		}
		assertExamBatch29Script(t, script, "rollback")
		return
	}
	if err != nil || len(got.Exams) != 2 || input.AppealEnabled != nil {
		t.Fatalf("session=%+v error=%v", got, err)
	}
	assertExamSessionChildRosterMetadata(t, got.Exams)
	assertExamBatch29Script(t, script, "commit")
}

// 子科依次继承考次信息；第二科的显式班级覆盖父考次班级。
func assertExamSessionChildRosterMetadata(t *testing.T, children []Exam) {
	t.Helper()
	for index, child := range children {
		wantClasses := []string{"parent-class"}
		if index == 1 {
			wantClasses = []string{"subject-class"}
		}
		if !reflect.DeepEqual(child.ClassIDs, wantClasses) || child.SessionID != "session" || child.SessionName != "session-name" || child.SessionGradeID != "grade" || !child.AppealEnabled {
			t.Fatalf("child metadata=%+v", child)
		}
	}
}

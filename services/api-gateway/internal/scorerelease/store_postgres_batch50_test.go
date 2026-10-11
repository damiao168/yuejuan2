package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type studentBatch50Step struct {
	fragment string
	args     []any
	columns  int
	rows     [][]driver.Value
	err      error
}

type studentBatch50Script struct {
	steps []studentBatch50Step
	next  int
	err   error
}

type studentBatch50Connector struct {
	batch48ReleaseConnector
	script *studentBatch50Script
}

func (c studentBatch50Connector) Connect(context.Context) (driver.Conn, error) {
	return &studentBatch50Conn{script: c.script}, nil
}
func (c studentBatch50Connector) Driver() driver.Driver { return c }

type studentBatch50Conn struct {
	batch48ReleaseConn
	script *studentBatch50Script
}

func (c *studentBatch50Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.script
	if s.next == len(s.steps) {
		s.err = fmt.Errorf("unexpected query: %s", query)
		return nil, s.err
	}
	step := s.steps[s.next]
	s.next++
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if !strings.Contains(query, step.fragment) || !reflect.DeepEqual(values, step.args) {
		s.err = fmt.Errorf("query %d args=%v, want %s args=%v", s.next, values, step.fragment, step.args)
		return nil, s.err
	}
	if step.err != nil {
		return nil, step.err
	}
	return &studentBatch50Rows{step: step}, nil
}

type studentBatch50Rows struct {
	step studentBatch50Step
	next int
}

func (r *studentBatch50Rows) Columns() []string { return make([]string, r.step.columns) }
func (*studentBatch50Rows) Close() error        { return nil }
func (r *studentBatch50Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

func studentBatch50Store(t *testing.T, steps []studentBatch50Step) *PostgresStore {
	t.Helper()
	script := &studentBatch50Script{steps: steps}
	db := sql.OpenDB(studentBatch50Connector{script: script})
	t.Cleanup(func() {
		_ = db.Close()
		if script.err != nil || script.next != len(script.steps) {
			t.Errorf("SQL script error=%v steps=%d/%d", script.err, script.next, len(script.steps))
		}
	})
	return NewPostgresStore(db, nil)
}

func studentBatch50ReadSteps(t *testing.T, visible bool) []studentBatch50Step {
	t.Helper()
	// 即使允许优秀答卷，隐藏题目仍必须在读取图像和优秀答卷前返回。
	policy, err := json.Marshal(VisibilityPolicy{ShowQuestionScores: visible, ShowHighScorePaper: !visible})
	if err != nil {
		t.Fatal(err)
	}
	published := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	steps := []studentBatch50Step{
		{fragment: "SELECT release_id::text FROM score_release_current", args: []any{"tenant", "exam"}, columns: 1, rows: [][]driver.Value{{"frozen-release"}}},
		{fragment: "FROM score_release WHERE", args: []any{"tenant", "frozen-release"}, columns: 17, rows: [][]driver.Value{{"frozen-release", "tenant", "exam", int64(3), SourceInitial, "reason", StatusPublished, "key", policy, []byte(`{}`), []byte(`{}`), "", "", "actor", published, "actor", published}}},
		{fragment: "AND student_id=$3::uuid", args: []any{"tenant", "frozen-release", "student"}, columns: 7, rows: [][]driver.Value{{"frozen-release", "student", "submission", float64(7), float64(10), "confirmed", "snapshot"}}},
		{fragment: "CASE WHEN e.id=$2::uuid THEN $4::uuid", args: []any{"tenant", "exam", "student", "frozen-release"}, columns: 9},
		{fragment: "SELECT name, subject, exam_type", args: []any{"tenant", "exam"}, columns: 3},
	}
	if visible {
		steps = append(steps,
			studentBatch50Step{fragment: "AND submission_id = $3::uuid", args: []any{"tenant", "frozen-release", "submission"}, columns: 10},
			studentBatch50Step{fragment: "SELECT DISTINCT ON(seg.question_id)", args: []any{"tenant", "submission"}, columns: 4})
	}
	return steps
}

func TestBatch50StudentResultKeepsFrozenReleaseAndHiddenReadBoundary(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(fmt.Sprint(visible), func(t *testing.T) {
			store := studentBatch50Store(t, studentBatch50ReadSteps(t, visible))
			got, err := store.StudentResult(t.Context(), "tenant", "exam", "student")
			if err != nil || got.ReleaseID != "frozen-release" || got.ReleaseVersion != 3 || got.TotalScore != 7 || got.OverallTotalScore != 7 || got.Exam != nil || got.Rankings.ClassUnavailableReason != "not_public" {
				t.Fatalf("student result=%+v error=%v", got, err)
			}
			if (got.Questions != nil) != visible || len(got.Questions) != 0 {
				t.Fatalf("question visibility changed: visible=%t questions=%#v", visible, got.Questions)
			}
		})
	}
}

func TestBatch50StudentResultKeepsMissingStudentAndMetadataFailures(t *testing.T) {
	steps := studentBatch50ReadSteps(t, false)[:3]
	steps[2].rows = nil
	if _, err := studentBatch50Store(t, steps).StudentResult(t.Context(), "tenant", "exam", "student"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing student error=%v", err)
	}
	failure := errors.New("exam metadata unavailable")
	steps = studentBatch50ReadSteps(t, false)
	steps[4].err = failure
	if got, err := studentBatch50Store(t, steps).StudentResult(t.Context(), "tenant", "exam", "student"); !errors.Is(err, failure) || got.ReleaseID != "" {
		t.Fatalf("metadata failure returned result=%+v error=%v", got, err)
	}
}

func TestBatch50PostgresStudentQuestionProjectionKeepsPolicyAndOwnedCopies(t *testing.T) {
	question := ReleaseQuestion{SubmissionID: "mine", QuestionFact: QuestionFact{QuestionID: "q", QuestionNo: "Q2", Score: 7, MaxScore: 10, Explanation: StudentExplanation{Feedback: "feedback", RubricSummary: []string{"rubric"}}}}
	geometry := &StudentImageGeometry{Width: 0.5, Height: 0.25}
	presentation := map[string]studentQuestionPresentation{"q": {PageNo: 2, SubmissionPageID: "page", Geometry: geometry}}
	ref := &StudentQuestionReference{SampleSize: 12}
	for _, tc := range []studentQuestionProjectionCase{
		{"hidden", VisibilityPolicy{}, nil, "not_public"},
		{"small cohort", VisibilityPolicy{ShowQuestionStatistics: true}, nil, "small_cohort"},
		{"public", VisibilityPolicy{ShowQuestionStatistics: true, ShowFeedback: true, ShowRubricSummary: true}, map[string]*StudentQuestionReference{"q": ref}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertStudentQuestionVisibilityAndCopies(t, tc, question, presentation, geometry, ref)
		})
	}
	first := question
	first.QuestionNo = "Q1"
	other := question
	other.SubmissionID = "other"
	got := postgresStudentQuestions([]ReleaseQuestion{question, other, first}, "mine", VisibilityPolicy{}, nil, nil, nil)
	if len(got) != 2 || got[0].QuestionNo != "Q1" || got[1].QuestionNo != "Q2" {
		t.Fatalf("submission filter or order changed: %+v", got)
	}
}

type studentQuestionProjectionCase struct {
	name   string
	policy VisibilityPolicy
	stats  map[string]*StudentQuestionReference
	reason string
}

func assertStudentQuestionVisibilityAndCopies(t *testing.T, tc studentQuestionProjectionCase, question ReleaseQuestion, presentation map[string]studentQuestionPresentation, geometry *StudentImageGeometry, ref *StudentQuestionReference) {
	t.Helper()
	view := postgresStudentQuestion(question, tc.policy, &StudentExam{Subject: "math"}, presentation, tc.stats)
	if view.Subject != "math" || view.PageNo != 2 || view.SubmissionPageID != "page" || view.AnswerGeometry != geometry || view.CohortUnavailableReason != tc.reason {
		t.Fatalf("projected view=%+v", view)
	}
	if tc.policy.ShowFeedback {
		if view.Feedback != "feedback" || view.Cohort != ref || !reflect.DeepEqual(view.RubricSummary, []string{"rubric"}) {
			t.Fatalf("public projection=%+v", view)
		}
		view.RubricSummary[0] = "changed"
		if question.Explanation.RubricSummary[0] != "rubric" {
			t.Fatal("rubric aliases frozen source")
		}
	} else if view.Feedback != "" || view.RubricSummary != nil {
		t.Fatalf("private explanation leaked: %+v", view)
	}
}

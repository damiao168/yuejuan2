package paper

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

type paperBatch10Step struct {
	kind, fragment string
	columns        int
	rows           [][]driver.Value
	result         driver.Result
	err            error
}

// 只按顺序记录这批入口实际经过的 SQL；失败后的事务结束也必须进入证据。
type paperBatch10Script struct {
	steps     []paperBatch10Step
	next      int
	events    []string
	unmatched error
}

func (s *paperBatch10Script) take(kind, query string) (paperBatch10Step, error) {
	if s.next >= len(s.steps) {
		s.unmatched = fmt.Errorf("unexpected %s: %s", kind, query)
		return paperBatch10Step{}, s.unmatched
	}
	step := s.steps[s.next]
	s.next++
	s.events = append(s.events, kind+":"+step.fragment)
	if step.kind != kind || !strings.Contains(query, step.fragment) {
		s.unmatched = fmt.Errorf("SQL step %d: %s %q, want %s containing %q", s.next, kind, query, step.kind, step.fragment)
		return step, s.unmatched
	}
	return step, step.err
}

type paperBatch10Connector struct{ script *paperBatch10Script }

func (c paperBatch10Connector) Connect(context.Context) (driver.Conn, error) {
	return &paperBatch10Conn{script: c.script}, nil
}
func (c paperBatch10Connector) Driver() driver.Driver { return paperBatch10Driver{} }

type paperBatch10Driver struct{}

func (paperBatch10Driver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use test connector")
}

type paperBatch10Conn struct{ script *paperBatch10Script }

func (*paperBatch10Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*paperBatch10Conn) Close() error { return nil }
func (c *paperBatch10Conn) Begin() (driver.Tx, error) {
	c.script.events = append(c.script.events, "begin")
	return c, nil
}
func (c *paperBatch10Conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *paperBatch10Conn) Commit() error {
	c.script.events = append(c.script.events, "commit")
	return nil
}
func (c *paperBatch10Conn) Rollback() error {
	c.script.events = append(c.script.events, "rollback")
	return nil
}
func (c *paperBatch10Conn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query)
	if err != nil {
		return nil, err
	}
	return &paperBatch10Rows{columns: step.columns, values: step.rows}, nil
}
func (c *paperBatch10Conn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	step, err := c.script.take("exec", query)
	if step.result == nil {
		step.result = driver.RowsAffected(1)
	}
	return step.result, err
}

type paperBatch10Rows struct {
	columns int
	values  [][]driver.Value
	next    int
}

func (r *paperBatch10Rows) Columns() []string { return make([]string, r.columns) }
func (*paperBatch10Rows) Close() error        { return nil }
func (r *paperBatch10Rows) Next(dest []driver.Value) error {
	if r.next == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.next])
	r.next++
	return nil
}

type paperBatch10ResultError struct{ err error }

func (r paperBatch10ResultError) LastInsertId() (int64, error) { return 0, nil }
func (r paperBatch10ResultError) RowsAffected() (int64, error) { return 0, r.err }

func openPaperBatch10Store(t *testing.T, steps []paperBatch10Step) (*PostgresStore, *paperBatch10Script) {
	t.Helper()
	script := &paperBatch10Script{steps: steps}
	db := sql.OpenDB(paperBatch10Connector{script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func assertPaperBatch10Proof(t *testing.T, script *paperBatch10Script, end string, result any) {
	t.Helper()
	if script.unmatched != nil {
		t.Fatal(script.unmatched)
	}
	if script.next != len(script.steps) || (end != "" && script.events[len(script.events)-1] != end) {
		t.Fatalf("incomplete SQL or transaction end: %d/%d %#v", script.next, len(script.steps), script.events)
	}
	proof, err := json.Marshal(map[string]any{"events": script.events, "result": result})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("BATCH10_PROOF=" + string(proof))
}

func paperBatch10NewCommandSteps() []paperBatch10Step {
	return []paperBatch10Step{
		{kind: "exec", fragment: "SELECT pg_advisory_xact_lock"},
		{kind: "query", fragment: "FROM paper_import_run WHERE", columns: 2},
	}
}

func TestBatch10PaperCreateRejectsUnownedAssetsBeforeWriting(t *testing.T) {
	for _, invalid := range []string{"paper", "source"} {
		t.Run(invalid, func(t *testing.T) {
			steps := []paperBatch10Step{{kind: "query", fragment: "SELECT subject FROM exam", columns: 1, rows: [][]driver.Value{{"mathematics"}}}}
			steps = append(steps, paperBatch10NewCommandSteps()...)
			steps = append(steps,
				paperBatch10Step{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
				paperBatch10Step{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM exam_paper", columns: 1, rows: [][]driver.Value{{invalid != "paper"}}},
			)
			if invalid == "source" {
				steps = append(steps, paperBatch10Step{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM file_asset", columns: 1, rows: [][]driver.Value{{false}}})
			}
			store, script := openPaperBatch10Store(t, steps)
			_, err := store.CreatePaperImport(context.Background(), "tenant", "exam", "user", CreatePaperImportInput{CommandID: "command", Subject: "math", ExamPaperID: "paper", Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset", RoleHint: "auto"}}})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid asset error = %v", err)
			}
			assertPaperBatch10Proof(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch10PaperAddRetainsInsertAndRowsAffectedFailureMapping(t *testing.T) {
	rowsErr := errors.New("rows affected failure")
	for _, test := range []struct {
		name   string
		result driver.Result
		err    error
		want   error
	}{
		{name: "insert_error", err: errors.New("insert failure"), want: ErrInvalidInput},
		{name: "unowned_source", result: driver.RowsAffected(0), want: ErrInvalidInput},
		{name: "rows_error", result: paperBatch10ResultError{rowsErr}, want: rowsErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := paperBatch10NewCommandSteps()
			steps = append(steps,
				paperBatch10Step{kind: "query", fragment: "SELECT exam_id::text FROM paper_import_job", columns: 1, rows: [][]driver.Value{{"exam"}}},
				paperBatch10Step{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
				paperBatch10Step{kind: "query", fragment: "SELECT status,current_generation", columns: 2, rows: [][]driver.Value{{"failed", int64(1)}}},
				paperBatch10Step{kind: "exec", fragment: "INSERT INTO paper_import_source", result: test.result, err: test.err},
			)
			store, script := openPaperBatch10Store(t, steps)
			_, err := store.AddPaperImportSources(context.Background(), "tenant", "import", "user", AddPaperImportSourcesInput{CommandID: "command", ExpectedGeneration: 1, Sources: []CreatePaperImportSourceInput{{FileAssetID: "asset", RoleHint: "auto"}}})
			if !errors.Is(err, test.want) {
				t.Fatalf("add error = %v, want %v", err, test.want)
			}
			assertPaperBatch10Proof(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch10PaperReplaceChecksActiveSourcesBeforeSoftDelete(t *testing.T) {
	steps := paperBatch10NewCommandSteps()
	steps = append(steps,
		paperBatch10Step{kind: "query", fragment: "SELECT exam_id::text FROM paper_import_job", columns: 1, rows: [][]driver.Value{{"exam"}}},
		paperBatch10Step{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
		paperBatch10Step{kind: "query", fragment: "SELECT status,current_generation", columns: 2, rows: [][]driver.Value{{"processing", int64(1)}}},
		paperBatch10Step{kind: "query", fragment: "SELECT id::text FROM paper_import_source", columns: 1, rows: [][]driver.Value{{"active-source"}}},
	)
	store, script := openPaperBatch10Store(t, steps)
	_, err := store.ReplacePaperImportSources(context.Background(), "tenant", "import", "user", ReplacePaperImportSourcesInput{CommandID: "command", ExpectedGeneration: 1, Sources: []ReplacePaperImportSourceInput{{ID: "other-source", RoleHint: "auto"}}})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("replace error = %v", err)
	}
	assertPaperBatch10Proof(t, script, "rollback", err.Error())
}

func TestBatch10PaperApplyRetiresPublishedRulesBeforeLaterFailure(t *testing.T) {
	// 回执有效且没有阻断项后才更新题目；退休旧规则后注入失败，整笔事务仍回滚。
	draft := importedDraft("1", "fill_blank", 5)
	draft.Rubric = nil
	draft.HumanConfirmedFields = requiredHumanConfirmedFields(draft)
	questions, err := json.Marshal([]PaperImportDraftQuestion{draft})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1, 0).UTC()
	failure := errors.New("answer lookup failure")
	steps := []paperBatch10Step{
		{kind: "query", fragment: "FROM paper_import_job WHERE", columns: 15, rows: [][]driver.Value{{"import", "tenant", "exam", "paper", "", "", "review_required", "mathematics", questions, []byte(`[]`), "", "user", now, now, nil}}},
		{kind: "query", fragment: "FOR UPDATE OF run", columns: 9, rows: [][]driver.Value{{int64(1), int64(1), "task", "hash", "run", "review_required", "revision", "task", "hash"}}},
		{kind: "query", fragment: "SELECT COALESCE(structure_confirmed_generation", columns: 1, rows: [][]driver.Value{{false}}},
		{kind: "query", fragment: "SELECT structured_issues,question_candidates", columns: 2, rows: [][]driver.Value{{[]byte(`[]`), []byte(`[]`)}}},
		{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
		{kind: "query", fragment: "SELECT COUNT(*) FROM question WHERE", columns: 1, rows: [][]driver.Value{{int64(1)}}},
		{kind: "query", fragment: "SELECT total_score::float8 FROM exam", columns: 1, rows: [][]driver.Value{{float64(5)}}},
		{kind: "query", fragment: "SELECT id::text,question_no,question_type", columns: 5, rows: [][]driver.Value{{"question", "1", "fill_blank", float64(5), int64(1)}}},
		{kind: "exec", fragment: "UPDATE question SET exam_paper_id"},
		{kind: "exec", fragment: "UPDATE question SET score_source"},
		{kind: "exec", fragment: "UPDATE scoring_rule SET status='retired'"},
		{kind: "query", fragment: "SELECT COUNT(*) FROM question_answer_key", err: failure},
	}
	store, script := openPaperBatch10Store(t, steps)
	_, err = store.ApplyPaperImport(context.Background(), "tenant", "import", "user")
	if !errors.Is(err, failure) {
		t.Fatalf("apply error = %v", err)
	}
	assertPaperBatch10Proof(t, script, "rollback", err.Error())
}

func TestBatch10PaperBlueprintKeepsBestEffortAndIssueOrder(t *testing.T) {
	score := 3.0
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("query_failure_%v", failed), func(t *testing.T) {
			section := paperBatch10Step{kind: "query", fragment: "FROM exam_blueprint_section", columns: 4, rows: [][]driver.Value{{"选择", "single_choice", int64(2), float64(3)}, {"坏行", "fill_blank", "invalid count", float64(2)}, {"填空", "fill_blank", int64(1), float64(2)}}}
			if failed {
				section.err = errors.New("blueprint unavailable")
			}
			store, script := openPaperBatch10Store(t, []paperBatch10Step{
				{kind: "query", fragment: "SELECT exam_id::text,total_score::float8", columns: 2, rows: [][]driver.Value{{"exam", float64(8)}}}, section,
			})
			issues := store.appendPaperImportBlueprintIssues(context.Background(), "tenant", "import", []QuestionCandidate{{QuestionType: "single_choice", Score: &score}}, []PaperImportIssue{{Code: "BASE", Message: "keep"}})
			codes, messages := []string{}, []string{}
			for _, issue := range issues {
				codes = append(codes, issue.Code)
				messages = append(messages, issue.Message)
			}
			want := []string{"BASE", "SECTION_COUNT_MISMATCH", "SECTION_COUNT_MISMATCH", "QUESTION_COUNT_MISMATCH", "SCORE_TOTAL_MISMATCH"}
			if failed {
				want = []string{"BASE", "SCORE_TOTAL_MISMATCH"}
			} else if messages[1] != "填空预计1道，当前识别0道" || messages[2] != "选择预计2道，当前识别1道" {
				t.Fatalf("section title order or failed-row handling changed: %#v", messages)
			}
			if !reflect.DeepEqual(codes, want) {
				t.Fatalf("blueprint issue order = %#v, want %#v", codes, want)
			}
			assertPaperBatch10Proof(t, script, "", issues)
		})
	}
}

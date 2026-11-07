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

type structureBatch37Step struct {
	kind, fragment string
	args           []driver.Value
	columns        int
	rows           [][]driver.Value
	err            error
	rowErr         error
}

// 每步同时核对 SQL 顺序、调用方绑定值和结果集关闭；这是入口对照，不是数据库并发证明。
type structureBatch37Script struct {
	steps               []structureBatch37Step
	next, openRows      int
	events, queries     []string
	arguments           [][]driver.Value
	unmatched           error
	beginErr, commitErr error
}

func (s *structureBatch37Script) take(kind, query string, args []driver.NamedValue) (structureBatch37Step, error) {
	if s.openRows != 0 || s.next >= len(s.steps) {
		s.unmatched = fmt.Errorf("unexpected %s with %d open rows at %d: %s", kind, s.openRows, s.next, query)
		return structureBatch37Step{}, s.unmatched
	}
	step := s.steps[s.next]
	s.next++
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	s.events = append(s.events, kind+":"+step.fragment)
	s.queries = append(s.queries, query)
	s.arguments = append(s.arguments, values)
	if step.kind != kind || !strings.Contains(query, step.fragment) || !reflect.DeepEqual(step.args, values) {
		s.unmatched = fmt.Errorf("SQL step %d: %s %q args=%#v, want %s %q args=%#v", s.next, kind, query, values, step.kind, step.fragment, step.args)
		return step, s.unmatched
	}
	return step, step.err
}

type structureBatch37Connector struct{ script *structureBatch37Script }

func (c structureBatch37Connector) Connect(context.Context) (driver.Conn, error) {
	return &structureBatch37Conn{script: c.script}, nil
}

func (structureBatch37Connector) Driver() driver.Driver { return structureBatch37Driver{} }

type structureBatch37Driver struct{}

func (structureBatch37Driver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use Batch37 connector")
}

type structureBatch37Conn struct{ script *structureBatch37Script }

func (*structureBatch37Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

func (*structureBatch37Conn) Close() error { return nil }

func (c *structureBatch37Conn) Begin() (driver.Tx, error) {
	c.script.events = append(c.script.events, "begin")
	if c.script.beginErr != nil {
		return nil, c.script.beginErr
	}
	return c, nil
}

func (c *structureBatch37Conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}

func (c *structureBatch37Conn) Commit() error {
	c.script.events = append(c.script.events, "commit")
	return c.script.commitErr
}

func (c *structureBatch37Conn) Rollback() error {
	c.script.events = append(c.script.events, "rollback")
	return nil
}

func (c *structureBatch37Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.script.openRows++
	return &structureBatch37Rows{script: c.script, step: step}, nil
}

func (c *structureBatch37Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}

type structureBatch37Rows struct {
	script *structureBatch37Script
	step   structureBatch37Step
	next   int
	closed bool
}

func (r *structureBatch37Rows) Columns() []string { return make([]string, r.step.columns) }

func (r *structureBatch37Rows) Close() error {
	if !r.closed {
		r.closed = true
		r.script.openRows--
		r.script.events = append(r.script.events, "close:"+r.step.fragment)
	}
	return nil
}

func (r *structureBatch37Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		if r.step.rowErr != nil {
			return r.step.rowErr
		}
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

func openStructureBatch37Store(t *testing.T, steps []structureBatch37Step) (*PostgresStore, *structureBatch37Script) {
	t.Helper()
	script := &structureBatch37Script{steps: steps}
	db := sql.OpenDB(structureBatch37Connector{script: script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func logStructureBatch37Proof(t *testing.T, result any) {
	t.Helper()
	proof, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("BATCH37_PROOF=" + string(proof))
}

func assertStructureBatch37Script(t *testing.T, script *structureBatch37Script, end string, result any) {
	t.Helper()
	if script.unmatched != nil {
		t.Fatal(script.unmatched)
	}
	if script.next != len(script.steps) || script.openRows != 0 {
		t.Fatalf("incomplete read/write proof: %d/%d open=%d", script.next, len(script.steps), script.openRows)
	}
	if end != "" && (len(script.events) == 0 || script.events[len(script.events)-1] != end) {
		t.Fatalf("transaction events=%#v, want end=%s", script.events, end)
	}
	logStructureBatch37Proof(t, map[string]any{"events": script.events, "queries": script.queries, "args": script.arguments, "result": result})
}

func structureBatch37JSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func structureBatch37Fixture() (PaperImportJob, []Question) {
	now := time.Unix(1, 0).UTC()
	job := PaperImportJob{ID: "import", TenantID: "tenant", ExamID: "exam", ExamPaperID: "paper", Status: "review_required", Generation: 2, Subject: "mathematics", CreatedBy: "creator", CreatedAt: now, UpdatedAt: now, Issues: []string{}, Sources: []PaperImportSource{{ID: "source", DetectedRole: "question"}}, Questions: []PaperImportDraftQuestion{{CandidateID: "candidate", QuestionNo: "1", QuestionType: "calculation", Score: 5, ScoreSource: "material", Stem: "material stem"}}}
	qs := []Question{{ID: "question", TenantID: "tenant", ExamID: "exam", ExamPaperID: "paper", QuestionNo: "1", QuestionType: "calculation", Score: 5, ScoreSource: "material", Stem: "teacher stem", SortOrder: 1, Status: "draft", AssessmentArchetype: "short_constructed"}}
	return job, qs
}

func structureBatch37ReadSteps(t *testing.T, job PaperImportJob, qs []Question) []structureBatch37Step {
	t.Helper()
	steps := []structureBatch37Step{
		{kind: "query", fragment: "FROM paper_import_job WHERE", args: []driver.Value{"tenant", "import"}, columns: 15, rows: [][]driver.Value{{job.ID, job.TenantID, job.ExamID, job.ExamPaperID, job.PaperFileAssetID, job.AnswerFileAssetID, job.Status, job.Subject, structureBatch37JSON(t, job.Questions), structureBatch37JSON(t, job.Issues), job.ErrorCode, job.CreatedBy, job.CreatedAt, job.UpdatedAt, nil}}},
		{kind: "query", fragment: "FOR UPDATE OF r", args: []driver.Value{"tenant", "import"}, columns: 9, rows: [][]driver.Value{{job.Generation, job.Generation, "task", "hash", "review_required", "revision", "task", "hash", job.StructureConfirmedGeneration}}},
		{kind: "query", fragment: "SELECT status FROM exam", args: []driver.Value{"tenant", job.ExamID}, columns: 1, rows: [][]driver.Value{{"draft"}}},
		{kind: "query", fragment: "SELECT total_score::float8 FROM exam", args: []driver.Value{"tenant", job.ExamID}, columns: 1, rows: [][]driver.Value{{float64(10)}}},
		{kind: "query", fragment: "SELECT question_candidates,structured_issues", args: []driver.Value{"tenant", "import"}, columns: 2, rows: [][]driver.Value{{structureBatch37JSON(t, job.QuestionCandidates), structureBatch37JSON(t, job.StructuredIssues)}}},
		{kind: "query", fragment: "FROM paper_import_source WHERE", args: []driver.Value{"tenant", "import"}, columns: 2},
		{kind: "query", fragment: "ORDER BY id FOR UPDATE", args: []driver.Value{"tenant", job.ExamID}, columns: 1},
		{kind: "query", fragment: "ORDER BY sort_order, question_no", args: []driver.Value{"tenant", job.ExamID}, columns: 12},
	}
	for _, source := range job.Sources {
		steps[5].rows = append(steps[5].rows, []driver.Value{source.ID, source.DetectedRole})
	}
	for _, q := range qs {
		steps[6].rows = append(steps[6].rows, []driver.Value{q.ID})
		steps[7].rows = append(steps[7].rows, []driver.Value{q.ID, q.TenantID, q.ExamID, q.ExamPaperID, q.QuestionNo, q.QuestionType, q.Score, q.Stem, structureBatch37JSON(t, q.KnowledgePoints), structureBatch37JSON(t, q.AnswerArea), int64(q.SortOrder), q.Status})
		steps = append(steps, structureBatch37QuestionDetails(t, q)...)
	}
	for _, q := range qs {
		if q.ScoreSource == "preset" && q.Score > 0 {
			steps = append(steps, structureBatch37Step{kind: "query", fragment: "WITH sections AS (", args: []driver.Value{"tenant", job.ExamID, int64(q.SortOrder)}, columns: 1, rows: [][]driver.Value{{q.Score}}})
		}
		steps = append(steps, structureBatch37Step{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM scoring_rule", args: []driver.Value{"tenant", q.ID}, columns: 1, rows: [][]driver.Value{{false}}})
	}
	return steps
}

func structureBatch37QuestionDetails(t *testing.T, q Question) []structureBatch37Step {
	t.Helper()
	steps := []structureBatch37Step{
		{kind: "query", fragment: "SELECT source_type,COALESCE", args: []driver.Value{"tenant", q.ID}, columns: 9, rows: [][]driver.Value{{q.SourceType, q.SourceBankItemID, q.SourceBankItemVersionID, q.SourceContentHash, structureBatch37JSON(t, q.BankContent), q.ParentQuestionNo, q.SubquestionNo, structureBatch37JSON(t, q.Options), q.ScoreSource}}},
		{kind: "query", fragment: "SELECT COALESCE(paper_import_id::text", args: []driver.Value{"tenant", q.ID}, columns: 3, rows: [][]driver.Value{{q.PaperImportID, q.PaperImportCandidateID, structureBatch37JSON(t, q.PaperImportSourceRefs)}}},
		{kind: "query", fragment: "SELECT COALESCE((SELECT archetype_code", args: []driver.Value{"tenant", q.ID}, columns: 1, rows: [][]driver.Value{{q.AssessmentArchetype}}},
		{kind: "query", fragment: "FROM question_answer_key", args: []driver.Value{"tenant", q.ID}, columns: 9},
		{kind: "query", fragment: "FROM question_rubric qr", args: []driver.Value{"tenant", q.ID}, columns: 11},
		{kind: "query", fragment: "FROM question_solution", args: []driver.Value{"tenant", q.ID}, columns: 8},
		{kind: "query", fragment: "FROM question_reference_manifest", args: []driver.Value{"tenant", q.ExamID, q.ID, ""}, columns: 8},
	}
	if q.AnswerKey != nil {
		a := q.AnswerKey
		steps[3].rows = [][]driver.Value{{a.ID, a.QuestionID, a.AnswerVersion, structureBatch37JSON(t, a.StandardAnswer), structureBatch37JSON(t, a.EquivalentAnswers), structureBatch37JSON(t, a.Tolerance), a.PaperImportID, a.PaperImportCandidateID, structureBatch37JSON(t, a.PaperImportSourceRefs)}}
	}
	if q.Rubric != nil {
		r := q.Rubric
		steps[4].rows = [][]driver.Value{{r.ID, r.QuestionID, r.Version, r.Status, r.MaxScore, structureBatch37JSON(t, r.Points), structureBatch37JSON(t, r.Deductions), structureBatch37JSON(t, r.Examples), r.PaperImportID, r.PaperImportCandidateID, structureBatch37JSON(t, r.PaperImportSourceRefs)}}
	}
	if q.Solution != nil {
		s := q.Solution
		steps[5].rows = [][]driver.Value{{s.ID, s.QuestionID, s.PaperImportID, s.SolutionVersion, s.RawText, structureBatch37JSON(t, s.Steps), structureBatch37JSON(t, s.SourceRefs), s.VerificationStatus}}
	}
	return steps
}

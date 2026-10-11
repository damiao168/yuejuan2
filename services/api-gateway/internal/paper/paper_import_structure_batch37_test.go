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

func TestBatch37StructureSeenPrecedesSourceAndRemoval(t *testing.T) {
	job, _ := structureBatch37Fixture()
	job.Sources[0].DetectedRole = "answer"
	job.QuestionCandidates = []QuestionCandidate{{QuestionNoRaw: "9"}}
	job.Questions[0].ParentQuestionNo, job.Questions[0].SubquestionNo = "1", "1"
	job.Questions = append(job.Questions, PaperImportDraftQuestion{QuestionNo: "01", QuestionType: "calculation", Stem: "duplicate"})
	qs := []Question{{ID: "q3", QuestionNo: "3", SortOrder: 3}, {ID: "q1", QuestionNo: "1", SortOrder: 1}, {ID: "q2", QuestionNo: "2", SortOrder: 2, Stem: "teacher content"}}
	preview, drafts := planPaperStructure(job, qs, 10)
	wantReasons := []string{"尚未识别到试卷题目资料；仅有答案或解析时不能确认题目结构", "第 1 题是独立子题，请先合并到对应大题并核对结构", "题目缺少试卷识别来源：1", "识别题号、题型或题干需要核对：01", "第 2 题已有内容，不能直接移除", "没有可采用的试卷题目"}
	if !reflect.DeepEqual(preview.BlockedReasons, wantReasons) || len(drafts) != 0 || len(preview.Changes) != 2 {
		t.Fatalf("source/seen/removal order changed: %#v %#v", preview, drafts)
	}
	if preview.Changes[0].QuestionID != "q2" || preview.Changes[0].Action != "conflict" || preview.Changes[1].QuestionID != "q3" || preview.Changes[1].Action != "remove" {
		t.Fatalf("unseen questions changed: %#v", preview.Changes)
	}
	if qs[0].ID != "q1" || qs[1].ID != "q2" || qs[2].ID != "q3" {
		t.Fatalf("questions no longer sorted in place: %#v", qs)
	}
	wantHash := stableContentHash(struct {
		Job       PaperImportJob
		Questions []Question
		Total     float64
	}{job, qs, 10})
	if preview.PreviewHash != wantHash {
		t.Fatal("preview hash stopped covering raw job and sorted official questions")
	}
	logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts, "questions": qs})
}

func TestBatch37StructureScoreSourcesAndDuplicateFormalNumber(t *testing.T) {
	for _, test := range []struct {
		name, source, oldSource, wantSource string
		confirmed                           bool
		oldScore, wantScore                 float64
	}{
		{name: "material", source: "material", wantSource: "material", wantScore: 5},
		{name: "human_confirmed", source: "human", confirmed: true, wantSource: "human", wantScore: 5},
		{name: "human_unconfirmed", source: "human", wantSource: "missing"},
		{name: "preset_not_material", source: "blueprint", confirmed: true, wantSource: "missing"},
		{name: "formal_human_wins", source: "material", oldSource: "human", oldScore: 7, wantSource: "human", wantScore: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			job.Questions[0].ScoreSource = test.source
			if test.confirmed {
				job.Questions[0].HumanConfirmedFields = []string{"score"}
			}
			qs[0].ScoreSource, qs[0].Score = test.oldSource, test.oldScore
			preview, drafts := planPaperStructure(job, qs, 10)
			if drafts[0].Score != test.wantScore || drafts[0].ScoreSource != test.wantSource || preview.RecognizedTotal != test.wantScore {
				t.Fatalf("score source changed: %#v %#v", preview, drafts)
			}
			logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts})
		})
	}
	t.Run("formal_duplicate_last_item", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		qs[0].ID, qs[0].ScoreSource, qs[0].Score = "first", "human", 3
		last := qs[0]
		last.ID, last.QuestionNo, last.SortOrder, last.Score = "last", "01", 2, 7
		qs = append(qs, last)
		preview, drafts := planPaperStructure(job, qs, 10)
		if preview.Changes[0].QuestionID != "last" || drafts[0].Score != 7 || preview.BlockedReasons[0] != "正式题号不唯一：1" {
			t.Fatalf("duplicate preview stopped using last official item: %#v %#v", preview, drafts)
		}
		logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts})
	})
}

func TestBatch37StructureCandidateAndConflictBoundaries(t *testing.T) {
	for _, code := range []string{"CONFLICTING_ANSWERS", "DUPLICATE_QUESTION_NO", "HUMAN_CONFIRMED_CONFLICT"} {
		for _, no := range []string{"", "01"} {
			t.Run(code+"/"+no, func(t *testing.T) {
				job, _ := structureBatch37Fixture()
				job.QuestionCandidates = []QuestionCandidate{{QuestionNoNormalized: "01", Options: []string{"first"}}, {QuestionNoRaw: "1", Options: []string{"later"}}}
				job.StructuredIssues = []PaperImportIssue{{Code: code, QuestionNo: no}}
				job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "unresolved"}
				job.Questions[0].Solution = &SolutionInput{RawText: "unresolved"}
				job.Questions[0].Rubric = &RubricInput{}
				preview, drafts := planPaperStructure(job, nil, 10)
				if len(preview.BlockedReasons) != 0 || drafts[0].AnswerKey != nil || drafts[0].Solution != nil || drafts[0].Rubric != nil || !reflect.DeepEqual(drafts[0].Options, []string{"first"}) {
					t.Fatalf("structure inferred answer resolution or changed first candidate: %#v %#v", preview, drafts)
				}
				job.Questions[0].Options = []string{"authored"}
				_, preserved := planPaperStructure(job, nil, 10)
				if !reflect.DeepEqual(preserved[0].Options, []string{"authored"}) {
					t.Fatal("candidate overwrote existing draft options")
				}
				logStructureBatch37Proof(t, map[string]any{"preview": preview, "drafts": drafts, "preserved": preserved})
			})
		}
	}
}

func structureBatch37Memory(t *testing.T, job PaperImportJob, qs []Question) *MemoryStore {
	t.Helper()
	s := NewMemoryStore()
	s.imports["import"] = job
	s.examState["exam"] = memoryExamState{Total: 10, Status: "configured"}
	s.readiness["exam"] = ReadinessResult{Ready: true, ConfigurationHash: "cached"}
	for _, q := range qs {
		s.questions[q.ID] = q
	}
	return s
}

func TestBatch37MemoryStructureGuardPrecedence(t *testing.T) {
	for _, test := range []paperMemoryStructureGuardCase{
		{name: "tenant_before_every_guard", tenant: "other", examStatus: "ready", generationDelta: 1, emptyHash: true, want: ErrNotFound},
		{name: "generation_before_complete", generationDelta: 1, want: ErrConflict},
		{name: "status_before_complete", status: "failed", want: ErrConflict},
		{name: "complete_before_frozen", examStatus: "ready", want: ErrInvalidInput},
		{name: "frozen_before_hash", complete: true, examStatus: "ready", staleHash: true, want: ErrExamFrozen},
		{name: "hash_before_blocked", complete: true, staleHash: true, blocked: true, want: ErrConflict},
		{name: "blocked_before_confirmed", complete: true, confirmed: true, blocked: true, want: ErrInvalidInput},
		{name: "fresh_confirmed_does_not_write", complete: true, confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperMemoryStructureGuardPrecedence(t, test)
		})
	}
}

func TestBatch37MemoryStructureKeepsTeacherAndProtectedContent(t *testing.T) {
	job, qs := structureBatch37Fixture()
	qs[0].Options = []string{"teacher option"}
	qs[0].AnswerKey = &AnswerKey{StandardAnswer: "teacher answer"}
	qs[0].Solution = &QuestionSolution{RawText: "teacher solution"}
	bank := Question{ID: "bank", TenantID: "tenant", ExamID: "exam", QuestionNo: "2", QuestionType: "calculation", Score: 5, ScoreSource: "material", SourceType: "question_bank", SortOrder: 2, Status: "draft"}
	locked := Question{ID: "locked", TenantID: "tenant", ExamID: "exam", QuestionNo: "3", QuestionType: "calculation", Score: 5, ScoreSource: "material", SortOrder: 3, Status: "draft"}
	extra := Question{ID: "extra", TenantID: "tenant", ExamID: "exam", QuestionNo: "4", SortOrder: 4, Status: "draft"}
	foreign := Question{ID: "foreign", TenantID: "other", ExamID: "exam", QuestionNo: "9", Stem: "foreign"}
	qs = append(qs, bank, locked, extra, foreign)
	for _, no := range []string{"2", "3", "5"} {
		draft := job.Questions[0]
		draft.QuestionNo, draft.Stem = no, "material "+no
		job.Questions = append(job.Questions, draft)
	}
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "material answer"}
	job.Questions[0].Solution = &SolutionInput{RawText: "material solution"}
	s := structureBatch37Memory(t, job, qs)
	s.rubrics[locked.ID] = []Rubric{{Status: "locked"}}
	preview, err := s.PreviewPaperStructure(context.Background(), "tenant", "import")
	if err != nil {
		t.Fatal(err)
	}
	input := AdoptPaperStructureInput{ExpectedGeneration: job.Generation, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: true}
	out, err := s.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
	if err != nil || !out.Confirmed || s.questions[extra.ID].Status != "deleted" {
		t.Fatalf("adoption=%#v err=%v extra=%#v", out, err, s.questions[extra.ID])
	}
	assertPaperAdoptionPreservesProtectedQuestions(t, s, job, bank, locked, foreign)
	if _, err = s.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input); !errors.Is(err, ErrConflict) {
		t.Fatal("receipt change did not invalidate old preview hash")
	}
	confirmed := s.imports["import"]
	confirmed.UpdatedAt = time.Time{}
	logStructureBatch37Proof(t, map[string]any{"preview": out, "questions": s.questions, "receipt": confirmed})
}

func TestBatch37PostgresStructureReadFailuresStopAndRollback(t *testing.T) {
	job, qs := structureBatch37Fixture()
	base := structureBatch37ReadSteps(t, job, qs)
	for index := range base {
		t.Run(fmt.Sprintf("query_%02d", index), func(t *testing.T) {
			failure := errors.New("read failure")
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].err = failure
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if !errors.Is(err, failure) || !reflect.DeepEqual(out, PaperStructurePreview{}) {
				t.Fatalf("read error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
	for _, index := range []int{5, 6, 7} {
		t.Run(fmt.Sprintf("rows_err_%02d", index), func(t *testing.T) {
			failure := errors.New("rows iteration failure")
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].rowErr = failure
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if !errors.Is(err, failure) {
				t.Fatalf("rows error=%v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
	for _, index := range []int{5, 6} {
		t.Run(fmt.Sprintf("scan_err_%02d", index), func(t *testing.T) {
			steps := append([]structureBatch37Step{}, base[:index+1]...)
			steps[index].rows = [][]driver.Value{make([]driver.Value, steps[index].columns)}
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if err == nil || !strings.Contains(err.Error(), "Scan error") {
				t.Fatalf("nil source/lock Scan error: %v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch37PostgresStructureReceiptAndJSONPrecedence(t *testing.T) {
	for _, test := range []paperStructureReceiptCase{
		{name: "run_status", column: 4, value: "processing"},
		{name: "result_generation_missing", column: 1},
		{name: "result_generation_old", column: 1, value: int64(1)},
		{name: "job_task_missing", column: 2, value: ""},
		{name: "run_task_other", column: 6, value: "other"},
		{name: "job_hash_missing", column: 3, value: ""},
		{name: "run_hash_other", column: 7, value: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperStructureReceiptFailureOrder(t, test)
		})
	}
	t.Run("job_status", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		job.Status = "processing"
		steps := structureBatch37ReadSteps(t, job, qs)[:2]
		store, script := openStructureBatch37Store(t, steps)
		_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("job status error=%v", err)
		}
		assertStructureBatch37Script(t, script, "rollback", err.Error())
	})
	t.Run("legacy_without_receipts", func(t *testing.T) {
		job, qs := structureBatch37Fixture()
		steps := structureBatch37ReadSteps(t, job, qs)
		steps[1].rows[0] = []driver.Value{job.Generation, nil, "", "", "review_required", "legacy-unverified", "", "", int64(0)}
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
		if err != nil || out.RecognizedCount != 1 {
			t.Fatalf("legacy preview=%#v err=%v", out, err)
		}
		assertStructureBatch37Script(t, script, "rollback", out)
	})
	for _, candidatesBad := range []bool{true, false} {
		t.Run(fmt.Sprintf("candidate_json_first_%v", candidatesBad), func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			steps := structureBatch37ReadSteps(t, job, qs)[:5]
			steps[4].rows[0][1] = []byte(`{`)
			if candidatesBad {
				steps[4].rows[0][0] = []byte(`[`)
			}
			store, script := openStructureBatch37Store(t, steps)
			_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			var syntax *json.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("JSON error=%v", err)
			}
			assertStructureBatch37Script(t, script, "rollback", err.Error())
		})
	}
}

func TestBatch37PostgresStructurePresetAndPublishedBoundaries(t *testing.T) {
	for _, test := range []struct {
		name        string
		preset      float64
		missing     bool
		published   bool
		draftScore  float64
		wantScore   float64
		wantBlocked bool
	}{
		{name: "same_preset_is_not_human", preset: 7, draftScore: 5, wantScore: 5},
		{name: "changed_preset_is_human", preset: 5, draftScore: 5, wantScore: 7},
		{name: "missing_preset_is_not_zero", missing: true, draftScore: 5, wantScore: 5},
		{name: "published_change_blocks", preset: 7, published: true, draftScore: 5, wantScore: 5, wantBlocked: true},
		{name: "published_same_structure_keeps", preset: 7, published: true, draftScore: 7, wantScore: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			qs[0].ScoreSource, qs[0].Score = "preset", 7
			job.Questions[0].Score = test.draftScore
			steps := structureBatch37ReadSteps(t, job, qs)
			presetIndex := len(steps) - 2
			steps[presetIndex].rows = [][]driver.Value{{test.preset}}
			if test.missing {
				steps[presetIndex].rows = nil
			}
			steps[len(steps)-1].rows = [][]driver.Value{{test.published}}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
			if err != nil || out.RecognizedTotal != test.wantScore || (len(out.BlockedReasons) > 0) != test.wantBlocked {
				t.Fatalf("preset/published result=%#v err=%v", out, err)
			}
			assertStructureBatch37Script(t, script, "rollback", out)
		})
	}
}

func structureBatch37Input(job PaperImportJob, qs []Question) AdoptPaperStructureInput {
	preview, _ := planPaperStructure(job, append([]Question{}, qs...), 10)
	return AdoptPaperStructureInput{ExpectedGeneration: job.Generation, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: true}
}

func TestBatch37PostgresStructureAdoptionGuardsDoNotWrite(t *testing.T) {
	for _, test := range []paperStructureAdoptionGuardCase{
		{name: "invalid_before_begin", invalid: true, want: ErrInvalidInput},
		{name: "generation_before_blocked", oldGeneration: true, blocked: true, want: ErrConflict},
		{name: "hash_before_blocked", staleHash: true, blocked: true, want: ErrConflict},
		{name: "blocked_before_confirmed", blocked: true, confirmed: true, want: ErrInvalidInput},
		{name: "confirmed_fresh_rolls_back", confirmed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertPaperStructureAdoptionWithoutWrite(t, test)
		})
	}
	for _, beginFailure := range []bool{true, false} {
		t.Run(fmt.Sprintf("begin_or_missing_%v", beginFailure), func(t *testing.T) {
			job, qs := structureBatch37Fixture()
			steps := structureBatch37ReadSteps(t, job, qs)[:1]
			steps[0].rows = nil
			end, want := "rollback", ErrNotFound
			if beginFailure {
				steps = nil
				end, want = "begin", errors.New("begin failed")
			}
			store, script := openStructureBatch37Store(t, steps)
			if beginFailure {
				script.beginErr = want
			}
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", structureBatch37Input(job, qs))
			if !errors.Is(err, want) || !reflect.DeepEqual(out, PaperStructurePreview{}) {
				t.Fatalf("begin/missing error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, err.Error())
		})
	}
}

func structureBatch37FinalSteps(job PaperImportJob) []structureBatch37Step {
	return []structureBatch37Step{
		{kind: "exec", fragment: "UPDATE paper_import_job SET structure_confirmed_generation", args: []driver.Value{"tenant", "import", "user"}},
		{kind: "exec", fragment: "UPDATE exam_blueprint_section SET deleted_at", args: []driver.Value{"tenant", job.ExamID}},
	}
}

func structureBatch37NewQuestionSteps(t *testing.T, job PaperImportJob) []structureBatch37Step {
	t.Helper()
	draft := job.Questions[0]
	return []structureBatch37Step{
		{kind: "query", fragment: "INSERT INTO question(tenant_id,exam_id", args: []driver.Value{"tenant", job.ExamID, job.ExamPaperID, draft.QuestionNo, draft.QuestionType, draft.Score, draft.Stem, structureBatch37JSON(t, wireList(draft.Options)), structureBatch37JSON(t, wireList(draft.KnowledgePoints)), int64(1), "import", draft.CandidateID, structureBatch37JSON(t, wireList(draft.SourceRefs)), draft.ParentQuestionNo, draft.SubquestionNo}, columns: 1, rows: [][]driver.Value{{"new-question"}}},
		{kind: "exec", fragment: "UPDATE question SET score_source", args: []driver.Value{"tenant", "new-question", "material"}},
		{kind: "exec", fragment: "UPDATE question SET assessment_archetype", args: []driver.Value{"tenant", "new-question", "structured_steps"}},
		{kind: "exec", fragment: "SELECT assessment_apply_default_question_config", args: []driver.Value{"tenant", job.ExamID, "new-question"}},
		{kind: "exec", fragment: "UPDATE question_assessment_config c", args: []driver.Value{"tenant", job.ExamID, "new-question", "structured_steps"}},
	}
}

func TestBatch37PostgresStructureInsertKeepsCallerBindingsAndFailureOrder(t *testing.T) {
	// 回执字段故意与调用参数不同，保护 SQL 继续绑定原 tenant/import，而不是借用回执身份。
	job, _ := structureBatch37Fixture()
	job.ID, job.TenantID = "database-import", "database-tenant"
	qs := []Question{}
	read := structureBatch37ReadSteps(t, job, qs)
	writes := append(structureBatch37NewQuestionSteps(t, job), structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	t.Run("success", func(t *testing.T) {
		steps := append(append([]structureBatch37Step{}, read...), writes...)
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if err != nil || !out.Confirmed || out.ImportID != job.ID {
			t.Fatalf("insert result=%#v err=%v", out, err)
		}
		assertStructureBatch37Script(t, script, "commit", out)
	})
	for index := range writes {
		t.Run(fmt.Sprintf("write_failure_%02d", index), func(t *testing.T) {
			failure := errors.New("write failure")
			steps := append(append([]structureBatch37Step{}, read...), writes[:index+1]...)
			steps[len(steps)-1].err = failure
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, failure) || out.Confirmed {
				t.Fatalf("write failure=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, "rollback", map[string]any{"preview": out, "error": err.Error()})
		})
	}
	t.Run("upsert_no_rows_is_conflict", func(t *testing.T) {
		steps := append(append([]structureBatch37Step{}, read...), writes[0])
		steps[len(steps)-1].rows = nil
		store, script := openStructureBatch37Store(t, steps)
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if !errors.Is(err, ErrConflict) || out.Confirmed {
			t.Fatalf("upsert conflict=%v preview=%#v", err, out)
		}
		assertStructureBatch37Script(t, script, "rollback", err.Error())
	})
	t.Run("commit_failure_keeps_unconfirmed_result", func(t *testing.T) {
		// database/sql 在 Commit 调用后已结束事务；这里只观察返回错误，不宣称 driver 必然收到回滚。
		steps := append(append([]structureBatch37Step{}, read...), writes...)
		store, script := openStructureBatch37Store(t, steps)
		failure := errors.New("commit failure")
		script.commitErr = failure
		out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
		if !errors.Is(err, failure) || out.Confirmed {
			t.Fatalf("commit error=%v preview=%#v", err, out)
		}
		assertStructureBatch37Script(t, script, "commit", map[string]any{"preview": out, "error": err.Error()})
	})
}

func TestBatch37PostgresStructureRemoveUpdateRetireAndContentOrder(t *testing.T) {
	job, qs := structureBatch37Fixture()
	job.Questions[0].Score = 7
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "new answer"}
	job.Questions[0].Solution = &SolutionInput{RawText: "new solution"}
	qs[0].Options = []string{"teacher option"}
	qs[0].AnswerKey = &AnswerKey{ID: "key", QuestionID: "question", AnswerVersion: "v1", StandardAnswer: "teacher answer"}
	qs[0].Solution = &QuestionSolution{ID: "solution", QuestionID: "question", SolutionVersion: "v1", RawText: "teacher solution", VerificationStatus: "human_confirmed"}
	qs = append(qs, Question{ID: "extra", TenantID: "tenant", ExamID: "exam", QuestionNo: "2", QuestionType: "calculation", SortOrder: 2, Status: "draft"})
	read := structureBatch37ReadSteps(t, job, qs)
	writes := []structureBatch37Step{
		{kind: "exec", fragment: "UPDATE question SET status='deleted'", args: []driver.Value{"tenant", "extra"}},
		{kind: "exec", fragment: "UPDATE question SET question_type", args: []driver.Value{"tenant", "question", "calculation", float64(7), "teacher stem", []byte(`["teacher option"]`), int64(1), "import", "candidate", []byte(`[]`), "", ""}},
		{kind: "exec", fragment: "UPDATE question SET score_source", args: []driver.Value{"tenant", "question", "material"}},
		{kind: "exec", fragment: "UPDATE scoring_rule SET status='retired'", args: []driver.Value{"tenant", "question"}},
	}
	writes = append(writes, structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	for failed := -1; failed < len(writes); failed++ {
		t.Run(fmt.Sprintf("failure_index_%d", failed), func(t *testing.T) {
			end := "commit"
			var want error
			steps := append(append([]structureBatch37Step{}, read...), writes...)
			if failed >= 0 {
				end, want = "rollback", errors.New("update failure")
				steps = steps[:len(read)+failed+1]
				steps[len(steps)-1].err = want
			}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, want) || out.Confirmed != (failed < 0) {
				t.Fatalf("update error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
		})
	}
}

func TestBatch37PostgresStructureImportedAnswerAndSolutionStayMachine(t *testing.T) {
	job, _ := structureBatch37Fixture()
	job.Questions[0].AnswerKey = &AnswerKeyInput{StandardAnswer: "A"}
	job.Questions[0].AnswerCandidateID = "answer-candidate"
	job.Questions[0].Solution = &SolutionInput{RawText: "material solution"}
	job.Questions[0].HumanConfirmedFields = []string{"solution"}
	job.Questions[0].Rubric = &RubricInput{Status: "locked"}
	qs := []Question{}
	read := structureBatch37ReadSteps(t, job, qs)
	writes := structureBatch37NewQuestionSteps(t, job)
	writes = append(writes,
		structureBatch37Step{kind: "query", fragment: "SELECT COUNT(*) FROM question_answer_key", args: []driver.Value{"tenant", "new-question"}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
		structureBatch37Step{kind: "query", fragment: "INSERT INTO question_answer_key", args: []driver.Value{"tenant", "new-question", "v1", []byte(`"A"`), []byte(`null`), []byte(`null`), "user"}, columns: 1, rows: [][]driver.Value{{"answer-key"}}},
		structureBatch37Step{kind: "exec", fragment: "UPDATE question_answer_key SET paper_import_id", args: []driver.Value{"tenant", "answer-key", "import", "answer-candidate", []byte(`null`)}},
		structureBatch37Step{kind: "query", fragment: "SELECT COUNT(*) FROM question_solution", args: []driver.Value{"tenant", "new-question"}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
		structureBatch37Step{kind: "exec", fragment: "INSERT INTO question_solution", args: []driver.Value{"tenant", "new-question", "import", "v1", "material solution", []byte(`null`), []byte(`null`), "machine", "user"}},
	)
	writes = append(writes, structureBatch37FinalSteps(job)...)
	input := structureBatch37Input(job, qs)
	for failed := -1; failed < 5; failed++ {
		t.Run(fmt.Sprintf("content_failure_%d", failed), func(t *testing.T) {
			end := "commit"
			var want error
			steps := append(append([]structureBatch37Step{}, read...), writes...)
			if failed >= 0 {
				end, want = "rollback", errors.New("content failure")
				steps = steps[:len(read)+5+failed+1]
				steps[len(steps)-1].err = want
			}
			store, script := openStructureBatch37Store(t, steps)
			out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
			if !errors.Is(err, want) || out.Confirmed != (failed < 0) {
				t.Fatalf("content error=%v preview=%#v", err, out)
			}
			assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
		})
	}
}

type paperMemoryStructureGuardCase struct {
	name, tenant, status, examStatus string
	complete, emptyHash, staleHash   bool
	confirmed, blocked               bool
	generationDelta                  int64
	want                             error
}

func assertPaperMemoryStructureGuardPrecedence(t *testing.T, test paperMemoryStructureGuardCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	if test.status != "" {
		job.Status = test.status
	}
	if test.confirmed {
		job.StructureConfirmedGeneration = job.Generation
	}
	if test.blocked {
		job.Sources[0].DetectedRole = "answer"
	}
	s := structureBatch37Memory(t, job, qs)
	if test.examStatus != "" {
		state := s.examState["exam"]
		state.Status = test.examStatus
		s.examState["exam"] = state
	}
	preview, err := s.PreviewPaperStructure(context.Background(), "tenant", "import")
	if err != nil {
		t.Fatal(err)
	}
	input := AdoptPaperStructureInput{ExpectedGeneration: job.Generation + test.generationDelta, PreviewHash: preview.PreviewHash, CompletePaperConfirmed: test.complete}
	if test.emptyHash {
		input.PreviewHash = ""
	}
	if test.staleHash {
		input.PreviewHash = "stale"
	}
	tenant := "tenant"
	if test.tenant != "" {
		tenant = test.tenant
	}
	before := structureBatch37JSON(t, map[string]any{"questions": s.questions, "imports": s.imports, "readiness": s.readiness, "exam": s.examState, "next": s.next})
	out, err := s.AdoptPaperStructure(context.Background(), tenant, "import", "user", input)
	if !errors.Is(err, test.want) {
		t.Fatalf("guard error=%v, want %v", err, test.want)
	}
	after := structureBatch37JSON(t, map[string]any{"questions": s.questions, "imports": s.imports, "readiness": s.readiness, "exam": s.examState, "next": s.next})
	if string(before) != string(after) {
		t.Fatal("guard or fresh confirmation wrote memory state")
	}
	logStructureBatch37Proof(t, map[string]any{"preview": out, "error": fmt.Sprint(err), "state": json.RawMessage(after)})
}

type paperStructureReceiptCase struct {
	name   string
	column int
	value  driver.Value
}

func assertPaperStructureReceiptFailureOrder(t *testing.T, test paperStructureReceiptCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	steps := structureBatch37ReadSteps(t, job, qs)[:2]
	steps[1].rows[0][test.column] = test.value
	store, script := openStructureBatch37Store(t, steps)
	_, err := store.PreviewPaperStructure(context.Background(), "tenant", "import")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("receipt error=%v", err)
	}
	assertStructureBatch37Script(t, script, "rollback", err.Error())
}

type paperStructureAdoptionGuardCase struct {
	name                          string
	invalid, oldGeneration        bool
	staleHash, blocked, confirmed bool
	want                          error
}

func assertPaperStructureAdoptionWithoutWrite(t *testing.T, test paperStructureAdoptionGuardCase) {
	t.Helper()
	job, qs := structureBatch37Fixture()
	if test.blocked {
		job.Sources[0].DetectedRole = "answer"
	}
	if test.confirmed {
		job.StructureConfirmedGeneration = job.Generation
	}
	input := structureBatch37Input(job, qs)
	steps := structureBatch37ReadSteps(t, job, qs)
	end := "rollback"
	if test.invalid {
		input.ExpectedGeneration = 0
		steps, end = nil, ""
	}
	if test.oldGeneration {
		input.ExpectedGeneration++
	}
	if test.staleHash {
		input.PreviewHash = "stale"
	}
	store, script := openStructureBatch37Store(t, steps)
	out, err := store.AdoptPaperStructure(context.Background(), "tenant", "import", "user", input)
	if !errors.Is(err, test.want) {
		t.Fatalf("adoption guard=%v, want %v", err, test.want)
	}
	if test.invalid && len(script.events) != 0 {
		t.Fatal("invalid input opened a transaction")
	}
	assertStructureBatch37Script(t, script, end, map[string]any{"preview": out, "error": fmt.Sprint(err)})
}

// 采用结构只能填充允许变化的正式题目；教师内容、题库题、锁定题和其他租户资料逐项核对。
func assertPaperAdoptionPreservesProtectedQuestions(t *testing.T, s *MemoryStore, job PaperImportJob, bank, locked, foreign Question) {
	t.Helper()
	teacher := s.questions["question"]
	if teacher.Stem != "teacher stem" || !reflect.DeepEqual(teacher.Options, []string{"teacher option"}) || teacher.AnswerKey.StandardAnswer != "teacher answer" || teacher.Solution.RawText != "teacher solution" {
		t.Fatalf("teacher content replaced: %#v", teacher)
	}
	if !reflect.DeepEqual(s.questions[bank.ID], bank) || !reflect.DeepEqual(s.questions[locked.ID], locked) || !reflect.DeepEqual(s.questions[foreign.ID], foreign) {
		t.Fatal("protected or foreign question changed")
	}
	if _, exists := s.readiness["exam"]; exists || s.imports["import"].StructureConfirmedGeneration != job.Generation {
		t.Fatal("successful adoption did not persist receipt and invalidate readiness")
	}
	var added Question
	for _, q := range s.questions {
		if q.TenantID == "tenant" && q.QuestionNo == "5" {
			added = q
		}
	}
	if added.Status != "draft" || added.SortOrder != 4 || added.Stem != "material 5" {
		t.Fatalf("new draft question changed: %#v", added)
	}
}

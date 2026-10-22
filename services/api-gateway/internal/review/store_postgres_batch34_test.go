package review

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

func TestBatch34SubmitGradeKeepsParentLockAndFailurePriority(t *testing.T) {
	lookupFailure := errors.New("task lookup failed")
	runFailure := errors.New("parent lock failed")
	taskLockFailure := errors.New("reached task lock")
	for _, test := range []reviewParentLockCase{
		{name: "missing task", missing: true, want: ErrNotFound},
		{name: "task lookup failure", lookupErr: lookupFailure, want: lookupFailure},
		{name: "task without parent", want: taskLockFailure},
		{name: "missing parent", run: "parent-run", missing: true, want: ErrInvalidTransition},
		{name: "parent lookup failure", run: "parent-run", runErr: runFailure, want: runFailure},
		{name: "queued parent", run: "parent-run", status: "queued", want: taskLockFailure},
		{name: "processing parent", run: "parent-run", status: "processing", want: taskLockFailure},
		{name: "review parent", run: "parent-run", status: "needs_review", want: taskLockFailure},
		{name: "failed parent", run: "parent-run", status: "failed", want: taskLockFailure},
		{name: "completed parent", run: "parent-run", status: "completed", want: ErrInvalidTransition},
		{name: "cancelled parent", run: "parent-run", status: "cancelled", want: ErrInvalidTransition},
		{name: "unknown parent", run: "parent-run", status: "unknown", want: ErrInvalidTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertReviewParentLockFailurePriority(t, test, taskLockFailure)
		})
	}
}

func TestBatch34SubmitGradeReplaysBeforeReadingDeletedTask(t *testing.T) {
	ctx := commandreceipt.WithInput(commandreceipt.WithID(context.Background(), "saved-command"), map[string]any{"answer": "frozen"})
	sum := sha256.Sum256([]byte(`{"Operation":"review.submit","Target":"task","Input":{"answer":"frozen"}}`))
	saved := SubmitResult{Task: ReviewTask{ID: "task", Status: "completed"}, Grade: HumanGrade{ID: "saved-grade"}, QuestionGradeID: "saved-question-grade"}
	raw, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	store, script := openReviewBatch34Store(t, []reviewBatch34Step{
		{name: "command lock", kind: "exec", fragment: "pg_advisory_xact_lock", args: []any{"tenant:reviewer:saved-command:business-command"}},
		{name: "saved receipt", fragment: "FROM business_command_receipt", columns: 2,
			args: []any{"tenant", "reviewer", "saved-command"}, rows: [][]driver.Value{{hex.EncodeToString(sum[:]), raw}}},
	})
	// 驱动不提供任何任务行；回执命中后继续读任务会使此测试失败。
	result, err := store.SubmitGrade(ctx, "tenant", "task", "reviewer", SubmitGradeInput{ExpectedRevision: -1})
	if err != nil || result.Grade.ID != saved.Grade.ID || result.QuestionGradeID != saved.QuestionGradeID || result.Task.Status != "completed" {
		t.Fatalf("saved submission = %#v, err = %v", result, err)
	}
	assertReviewBatch34Script(t, script)
}

func TestBatch34ArbitrationInsertKeepsSessionIdentityAndPolicy(t *testing.T) {
	writeFailure := errors.New("arbitration insert failed")
	session := reviewBatch34Session()
	for _, test := range []reviewArbitrationInsertCase{
		{name: "unassigned fallback reason"},
		{name: "assigned explicit reason", assignedTo: "arbitrator", reason: "  retained reason  "},
		{name: "original reviewer forbidden", assignedTo: session.FirstReviewerID, forbidden: true},
		{name: "original reviewer allowed", assignedTo: session.SecondReviewerID, allowSame: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertReviewArbitrationSessionBinding(t, test, session, writeFailure)
		})
	}
}

func TestBatch34FinalGradeInsertUsesContextMaxAndExplicitIdentity(t *testing.T) {
	writeFailure := errors.New("final grade insert failed")
	for _, pending := range []FinalGrade{
		{Score: 0, Source: "double_mark_auto", ResolutionStrategy: "average"},
		{Score: 4.25, Source: "arbitration", ArbitrationTaskID: "arbitration", ResolutionStrategy: "arbitration"},
	} {
		t.Run(pending.Source, func(t *testing.T) {
			pending.TenantID, pending.CreatedBy, pending.AnswerSegmentID, pending.MaxScore = "unused-tenant", "unused-actor", "unused-segment", 999
			steps := append(reviewBatch34ContextSteps(), reviewBatch34Step{
				name: "final grade insert", fragment: "INSERT INTO final_grade", err: writeFailure,
				args: []any{"tenant", "exam", "question", "Q1", "segment", "submission", "anonymous",
					pending.Score, float64(10.5), pending.Source, "session", pending.ArbitrationTaskID, pending.ResolutionStrategy, "actor"},
			})
			store, script := openReviewBatch34Store(t, steps)
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.insertFinalGradeTx(context.Background(), tx, "tenant", "actor", reviewBatch34Session(), pending)
			_ = tx.Rollback()
			if !errors.Is(err, writeFailure) {
				t.Fatalf("final grade error = %v, want %v", err, writeFailure)
			}
			assertReviewBatch34Script(t, script)
		})
	}
}

func TestBatch34DoubleMarkWritesCurrentGradeBeforeRunStatistics(t *testing.T) {
	writeFailure := errors.New("current grade insert failed")
	statsFailure := errors.New("statistics update failed")
	for _, test := range []struct {
		name      string
		finalized bool
		insertErr error
		statsErr  error
		wantErr   error
		wantID    string
	}{
		{name: "first mark still synchronizes counts"},
		{name: "final mark writes then synchronizes", finalized: true, wantID: "current-grade"},
		{name: "grade failure stops statistics", finalized: true, insertErr: writeFailure, wantErr: writeFailure},
		{name: "statistics failure stays in caller transaction", finalized: true, statsErr: statsFailure, wantErr: statsFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			steps := []reviewBatch34Step{}
			var final *FinalGrade
			if test.finalized {
				final = &FinalGrade{ID: "final", AnswerSegmentID: "segment", Score: 3, MaxScore: 10.5, DoubleMarkSessionID: "session", ResolutionStrategy: "average"}
				steps = append(steps,
					reviewBatch34Step{name: "prior grade lock", fragment: "FROM question_grade", lock: true, columns: 1, args: []any{"tenant", "segment"}},
					reviewBatch34Step{name: "current grade insert", fragment: "INSERT INTO question_grade", columns: 1,
						args: []any{"tenant", "task", "parent-run", float64(3), float64(10.5), []byte(`{"double_mark_session_id":"session","final_grade_id":"final","resolution_strategy":"average"}`), "", "reviewer"},
						rows: [][]driver.Value{{"current-grade"}}, err: test.insertErr},
				)
			}
			if test.insertErr == nil {
				steps = append(steps,
					reviewBatch34Step{name: "run counts", fragment: "count(DISTINCT pending.answer_segment_id)", columns: 2, args: []any{"tenant", "parent-run"}, rows: [][]driver.Value{{int64(2), int64(3)}}},
					reviewBatch34Step{name: "run statistics", kind: "exec", fragment: "UPDATE scoring_run SET review_count", args: []any{"tenant", "parent-run", int64(2), int64(3)}, err: test.statsErr},
				)
			}
			store, script := openReviewBatch34Store(t, steps)
			tx, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			id, err := store.syncSubmittedDoubleMarkTx(context.Background(), tx, "tenant", "parent-run", "task", "reviewer", final)
			_ = tx.Rollback()
			if !errors.Is(err, test.wantErr) || id != test.wantID {
				t.Fatalf("double mark result = %q, %v, want %q, %v", id, err, test.wantID, test.wantErr)
			}
			assertReviewBatch34Script(t, script)
		})
	}
}

func reviewBatch34Session() DoubleMarkSession {
	return DoubleMarkSession{
		ID: "session", TenantID: "session-tenant", ExamID: "exam", QuestionID: "question", QuestionNo: "Q1",
		AnswerSegmentID: "segment", SubmissionID: "submission", AnonymousCode: "anonymous",
		FirstReviewerID: "first-reviewer", SecondReviewerID: "second-reviewer", Threshold: 1.25,
	}
}

func reviewBatch34ContextSteps() []reviewBatch34Step {
	return []reviewBatch34Step{
		{name: "answer context", fragment: "FROM answer_segment seg", columns: 32, args: []any{"tenant", "segment"}, rows: [][]driver.Value{{
			"exam", "submission", "anonymous", "segment", []byte(`{}`),
			"question", "tenant", "exam", "", "Q1", "short_answer", float64(10.5), "stem",
			[]byte(`[]`), []byte(`null`), int64(1), "active", "", "", "", "", float64(0),
			[]byte(`[]`), []byte(`[]`), []byte(`[]`), "answer", "manual", []byte(`null`), []byte(`null`), []byte(`null`), []byte(`null`), "answer-version",
		}}},
		{name: "automation context", fragment: "FROM (SELECT 1) seed", columns: 10, args: []any{"tenant", "segment"},
			rows: [][]driver.Value{{"", "", nil, "", "", []byte(`null`), "", nil, nil, ""}}},
	}
}

// 脚本驱动只核对 SQL 调用顺序和绑定；不模拟 PostgreSQL 的锁竞争、回滚或触发器行为。
type reviewBatch34Step struct {
	name, kind, fragment string
	lock                 bool
	columns              int
	args                 []any
	rows                 [][]driver.Value
	err                  error
}

type reviewBatch34Script struct {
	t       *testing.T
	steps   []reviewBatch34Step
	next    int
	events  []string
	commits int
	rolls   int
}

func openReviewBatch34Store(t *testing.T, steps []reviewBatch34Step) (*PostgresStore, *reviewBatch34Script) {
	t.Helper()
	script := &reviewBatch34Script{t: t, steps: steps}
	db := sql.OpenDB(reviewBatch34Connector{script: script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func assertReviewBatch34Script(t *testing.T, script *reviewBatch34Script) {
	t.Helper()
	if script.next != len(script.steps) || script.commits != 0 || script.rolls != 1 {
		t.Fatalf("SQL boundary changed: consumed=%d/%d commits=%d rollbacks=%d events=%v", script.next, len(script.steps), script.commits, script.rolls, script.events)
	}
}

func (s *reviewBatch34Script) consume(kind, query string, args []driver.NamedValue) (reviewBatch34Step, error) {
	s.t.Helper()
	if s.next >= len(s.steps) {
		s.t.Errorf("unexpected %s after script end: %s", kind, query)
		return reviewBatch34Step{}, errors.New("unexpected SQL after script end")
	}
	step := s.steps[s.next]
	s.next++
	wantKind := step.kind
	if wantKind == "" {
		wantKind = "query"
	}
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = arg.Value
	}
	if kind != wantKind || !strings.Contains(query, step.fragment) || (step.lock && !strings.Contains(query, "FOR UPDATE")) || !reflect.DeepEqual(values, step.args) {
		s.t.Errorf("step %s: kind=%s query=%s args=%v, want kind=%s fragment=%s lock=%t args=%v", step.name, kind, query, values, wantKind, step.fragment, step.lock, step.args)
		return step, errors.New("SQL script mismatch")
	}
	s.events = append(s.events, step.name)
	return step, step.err
}

type reviewBatch34Connector struct{ script *reviewBatch34Script }

func (c reviewBatch34Connector) Connect(context.Context) (driver.Conn, error) {
	return &reviewBatch34Conn{script: c.script}, nil
}
func (reviewBatch34Connector) Driver() driver.Driver { return reviewBatch34Driver{} }

type reviewBatch34Driver struct{}

func (reviewBatch34Driver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

type reviewBatch34Conn struct{ script *reviewBatch34Script }

func (*reviewBatch34Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is outside this fixture")
}
func (*reviewBatch34Conn) Close() error { return nil }
func (c *reviewBatch34Conn) Begin() (driver.Tx, error) {
	c.script.events = append(c.script.events, "begin")
	return reviewBatch34Tx{script: c.script}, nil
}
func (c *reviewBatch34Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.consume("query", query, args)
	if err != nil {
		return nil, err
	}
	return &reviewBatch34Rows{columns: step.columns, rows: step.rows}, nil
}
func (c *reviewBatch34Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.consume("exec", query, args)
	if err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

type reviewBatch34Tx struct{ script *reviewBatch34Script }

func (tx reviewBatch34Tx) Commit() error {
	tx.script.commits++
	tx.script.events = append(tx.script.events, "commit")
	return nil
}
func (tx reviewBatch34Tx) Rollback() error {
	tx.script.rolls++
	tx.script.events = append(tx.script.events, "rollback")
	return nil
}

type reviewBatch34Rows struct {
	columns int
	rows    [][]driver.Value
	next    int
}

func (r *reviewBatch34Rows) Columns() []string {
	columns := make([]string, r.columns)
	for index := range columns {
		columns[index] = fmt.Sprintf("column_%d", index)
	}
	return columns
}
func (*reviewBatch34Rows) Close() error { return nil }
func (r *reviewBatch34Rows) Next(values []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(values, r.rows[r.next])
	r.next++
	return nil
}

type reviewParentLockCase struct {
	name      string
	run       driver.Value
	status    string
	missing   bool
	lookupErr error
	runErr    error
	want      error
}

func assertReviewParentLockFailurePriority(t *testing.T, test reviewParentLockCase, taskLockFailure error) {
	t.Helper()
	lookup := reviewBatch34Step{
		name: "task parent", fragment: "SELECT scoring_run_id::text FROM review_task", columns: 1,
		args: []any{"tenant", "task"}, rows: [][]driver.Value{{test.run}}, err: test.lookupErr,
	}
	if test.missing && test.run == nil {
		lookup.rows = nil
	}
	steps := []reviewBatch34Step{lookup}
	if test.run != nil {
		parent := reviewBatch34Step{
			name: "parent lock", fragment: "FROM scoring_run", lock: true, columns: 1,
			args: []any{"tenant", "parent-run"}, rows: [][]driver.Value{{test.status}}, err: test.runErr,
		}
		if test.missing {
			parent.rows = nil
		}
		steps = append(steps, parent)
	}
	if errors.Is(test.want, taskLockFailure) {
		steps = append(steps, reviewBatch34Step{
			name: "task lock", fragment: "FROM review_task", lock: true,
			args: []any{"tenant", "task"}, err: taskLockFailure,
		})
	}
	// 在任务行锁处停止，保护父运行拒绝/失败必须优先于阅卷任务读取的顺序。
	store, script := openReviewBatch34Store(t, steps)
	_, err := store.SubmitGrade(context.Background(), "tenant", "task", "reviewer", SubmitGradeInput{})
	if !errors.Is(err, test.want) {
		t.Fatalf("submit error = %v, want %v", err, test.want)
	}
	assertReviewBatch34Script(t, script)
}

type reviewArbitrationInsertCase struct {
	name, assignedTo, reason string
	allowSame, forbidden     bool
}

func assertReviewArbitrationSessionBinding(t *testing.T, test reviewArbitrationInsertCase, session DoubleMarkSession, writeFailure error) {
	t.Helper()
	steps := []reviewBatch34Step{}
	if !test.forbidden {
		steps = reviewBatch34ContextSteps()
		reason := test.reason
		if strings.TrimSpace(reason) == "" {
			reason = "score difference 3.50 exceeds threshold 1.25"
		}
		status := "pending"
		if test.assignedTo != "" {
			status = "assigned"
		}
		steps = append(steps, reviewBatch34Step{
			name: "arbitration insert", fragment: "INSERT INTO arbitration_task", err: writeFailure,
			args: []any{"tenant", "session", "exam", "question", "Q1", "segment", "submission", "anonymous",
				"first-reviewer", "second-reviewer", float64(5), float64(1.5), float64(3.5), reason, status,
				test.assignedTo, test.allowSame, []byte(`{"question":{"options":null,"id":"question","tenant_id":"tenant","exam_id":"exam","question_no":"Q1","question_type":"short_answer","score":10.5,"stem":"stem","knowledge_points":[],"sort_order":1,"status":"active"},"rubric":{"id":"","question_id":"question","version":"","status":"missing","max_score":10.5,"points":[],"deductions":[],"examples":[]},"raw_answer":"answer"}`), "actor"},
		})
	}
	store, script := openReviewBatch34Store(t, steps)
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.createArbitrationTaskTx(context.Background(), tx, "tenant", "actor", session, ArbitrationTask{
		TenantID: "unused-tenant", AnswerSegmentID: "unused-segment", FirstReviewerID: "unused-reviewer",
		FirstScore: 5, SecondScore: 1.5, DifferenceReason: test.reason,
		AssignedTo: test.assignedTo, AllowSameArbitrator: test.allowSame,
	})
	_ = tx.Rollback()
	want := writeFailure
	if test.forbidden {
		want = ErrForbidden
	}
	if !errors.Is(err, want) {
		t.Fatalf("arbitration error = %v, want %v", err, want)
	}
	assertReviewBatch34Script(t, script)
}

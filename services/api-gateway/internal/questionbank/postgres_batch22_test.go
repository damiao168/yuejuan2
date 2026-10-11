package questionbank

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
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/commandreceipt"
)

const (
	batch22BankID    = "00000000-0000-0000-0000-000000000101"
	batch22ItemID    = "00000000-0000-0000-0000-000000000102"
	batch22VersionID = "00000000-0000-0000-0000-000000000103"
	batch22ExamID    = "00000000-0000-0000-0000-000000000104"
)

type batch22Step struct {
	kind, fragment string
	columns        int
	rows           [][]driver.Value
	args           map[int]any
	err            error
}

// 脚本只验证本批路径的 SQL 顺序、参数和回滚，不替代真实 PostgreSQL 的锁与触发器验证。
type batch22Script struct {
	steps          []batch22Step
	next           int
	opened, closed int
	events         []string
	err            error
}

func (s *batch22Script) take(kind, query string, args []driver.NamedValue) (batch22Step, error) {
	if s.next >= len(s.steps) {
		s.err = fmt.Errorf("unexpected %s: %s", kind, query)
		return batch22Step{}, s.err
	}
	step := s.steps[s.next]
	s.next++
	s.events = append(s.events, kind+":"+step.fragment)
	if step.kind != kind || !strings.Contains(query, step.fragment) {
		s.err = fmt.Errorf("step %d: %s %q, want %s containing %q", s.next, kind, query, step.kind, step.fragment)
		return step, s.err
	}
	for index, value := range step.args {
		if index >= len(args) || !reflect.DeepEqual(args[index].Value, value) {
			s.err = fmt.Errorf("step %d argument %d: %#v, want %#v", s.next, index, args, value)
			return step, s.err
		}
	}
	return step, step.err
}

type batch22Connector struct{ script *batch22Script }

func (c batch22Connector) Connect(context.Context) (driver.Conn, error) {
	return &batch22Conn{c.script}, nil
}
func (c batch22Connector) Driver() driver.Driver { return c }
func (batch22Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use test connector")
}

type batch22Conn struct{ script *batch22Script }

func (*batch22Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*batch22Conn) Close() error                             { return nil }
func (*batch22Conn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (c *batch22Conn) Begin() (driver.Tx, error) {
	c.script.events = append(c.script.events, "begin")
	return c, nil
}
func (c *batch22Conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c *batch22Conn) Commit() error {
	c.script.events = append(c.script.events, "commit")
	return nil
}
func (c *batch22Conn) Rollback() error {
	c.script.events = append(c.script.events, "rollback")
	return nil
}
func (c *batch22Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c *batch22Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.script.opened++
	return &batch22Rows{script: c.script, columns: step.columns, values: step.rows}, nil
}

type batch22Rows struct {
	script  *batch22Script
	columns int
	values  [][]driver.Value
	next    int
}

func (r *batch22Rows) Columns() []string { return make([]string, r.columns) }
func (r *batch22Rows) Close() error {
	r.script.closed++
	return nil
}
func (r *batch22Rows) Next(dest []driver.Value) error {
	if r.next == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.next])
	r.next++
	return nil
}

func openBatch22Store(t *testing.T, steps []batch22Step) (*PostgresStore, *batch22Script) {
	t.Helper()
	script := &batch22Script{steps: steps}
	db := sql.OpenDB(batch22Connector{script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func assertBatch22Script(t *testing.T, s *batch22Script, end string) {
	t.Helper()
	if s.err != nil {
		t.Fatal(s.err)
	}
	if s.next != len(s.steps) || s.opened != s.closed || len(s.events) == 0 || s.events[len(s.events)-1] != end {
		t.Fatalf("SQL steps=%d/%d, rows=%d/%d, events=%#v", s.next, len(s.steps), s.closed, s.opened, s.events)
	}
}

func batch22Scope() auth.AccessScope {
	return auth.AccessScope{TenantID: "tenant", ActorID: "actor", TenantWide: true}
}

func batch22BankStep(id, status string) batch22Step {
	now := time.Unix(0, 0)
	return batch22Step{kind: "query", fragment: "question_bank_actor_has_action", columns: 11,
		rows: [][]driver.Value{{id, "tenant", "school", "bank", "", status, int64(4), int64(1), "actor", now, now}},
		args: map[int]any{1: id}}
}

func batch22VersionRow(t *testing.T, content Content, scoring Scoring) []driver.Value {
	t.Helper()
	raw, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	scoreRaw, err := json.Marshal(scoring)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(0, 0)
	return []driver.Value{batch22VersionID, "tenant", batch22ItemID, int64(3), int64(7), int64(1), "published", nil, "actor", contentHash(content), raw, now, now, scoreRaw, "bundle"}
}

func TestBatch22MutationWritesAuditOutboxReceiptBeforeCommit(t *testing.T) {
	failure := errors.New("persistence failed")
	for _, failAt := range []string{"", "audit_log", "event_outbox", "business_command_receipt"} {
		t.Run("failure_at_"+failAt, func(t *testing.T) {
			assertQuestionBankMutationPersistenceOrder(t, failAt, failure)
		})
	}
}

func TestBatch22MaterializeReplayRejectsRevokedSourceBeforeReturningReceipt(t *testing.T) {
	command := pgMutationCommand{op: "question_bank.materialize", target: batch22ExamID, action: "read"}
	inputRaw, err := json.Marshal(struct {
		Operation, Target string
		Input             any
	}{command.op, command.target, nil})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(inputRaw)
	receipt, err := json.Marshal(MaterializeResult{ExamID: batch22ExamID, SourceVersionIDs: []string{batch22VersionID, batch22ItemID}})
	if err != nil {
		t.Fatal(err)
	}
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "exec", fragment: "pg_advisory_xact_lock"},
		{kind: "query", fragment: "FROM business_command_receipt", columns: 2, rows: [][]driver.Value{{hex.EncodeToString(hash[:]), receipt}}},
		{kind: "query", fragment: "SELECT school_id::text", columns: 2, rows: [][]driver.Value{{"school", []byte("[]")}}},
		{kind: "query", fragment: "FROM question_bank_item_version", columns: 15, args: map[int]any{1: batch22VersionID}},
	})
	ctx := commandreceipt.WithID(context.Background(), "batch22-replay")
	got, err := pgMutation(ctx, store, batch22Scope(), command, func(*sql.Tx) (MaterializeResult, error) {
		t.Fatal("replay invoked mutation")
		return MaterializeResult{}, nil
	})
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(got, MaterializeResult{}) {
		t.Fatalf("replay result=%+v error=%v", got, err)
	}
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22ReviewerBindingStopsAfterFirstACLError(t *testing.T) {
	failure := errors.New("review ACL failed")
	bankStep := batch22BankStep(batch22BankID, "active")
	bankStep.fragment = "FOR UPDATE OF b"
	store, script := openBatch22Store(t, []batch22Step{
		bankStep,
		{kind: "query", fragment: "SELECT EXISTS(SELECT 1 FROM app_user", columns: 1, rows: [][]driver.Value{{true}}},
		{kind: "exec", fragment: "INSERT INTO question_bank_acl", args: map[int]any{3: "read"}},
		{kind: "exec", fragment: "DELETE FROM question_bank_acl", args: map[int]any{3: "review"}, err: failure},
	})
	got, err := store.BindReviewers(context.Background(), batch22Scope(), batch22BankID, ReviewerBinding{ExpectedRevision: 4, UserID: batch22ItemID, Read: true})
	if !errors.Is(err, failure) || got.ID != batch22BankID || got.Revision != 4 {
		t.Fatalf("binding result=%+v error=%v", got, err)
	}
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22MaterializeRevisionConflictPrecedesStatusFailure(t *testing.T) {
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "query", fragment: "SELECT school_id::text", columns: 2, rows: [][]driver.Value{{"school", []byte("[]")}}},
		{kind: "query", fragment: "SELECT status,revision,subject", columns: 3, rows: [][]driver.Value{{"published", int64(9), "mathematics"}}},
	})
	got, err := store.Materialize(context.Background(), batch22Scope(), batch22ExamID, MaterializeInput{ExpectedRevision: 8, Selections: []MaterializeSelection{{VersionID: batch22VersionID, QuestionNo: "1", SortOrder: 1}}})
	if !errors.Is(err, ErrConflict) || got.ExamID != batch22ExamID || got.Questions == nil || got.SourceVersionIDs == nil {
		t.Fatalf("materialize result=%+v error=%v", got, err)
	}
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22MaterializeLocksBanksInOrderAndStopsAtArchived(t *testing.T) {
	second := batch22ItemID
	first := batch22BankStep(batch22BankID, "active")
	first.fragment = "FOR SHARE OF b"
	last := batch22BankStep(second, "archived")
	last.fragment = "FOR SHARE OF b"
	store, script := openBatch22Store(t, []batch22Step{first, last})
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = store.lockMaterializeBanks(context.Background(), tx, batch22Scope(), map[string]bool{second: true, batch22BankID: true})
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("bank locking error=%v", err)
	}
	_ = tx.Rollback()
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22InsertVersionPreservesSourceAndPayloadSlots(t *testing.T) {
	content, scoring := fixtureContent(), scoringFixture()
	raw, _ := json.Marshal(content)
	scoreRaw, _ := json.Marshal(scoring)
	source := batch22VersionID
	store, script := openBatch22Store(t, []batch22Step{{kind: "query", fragment: "INSERT INTO question_bank_item_version", columns: 15,
		rows: [][]driver.Value{batch22VersionRow(t, content, scoring)},
		args: map[int]any{0: "tenant", 1: batch22ItemID, 2: 3, 3: 7, 4: &source, 5: "actor", 6: raw, 7: contentHash(content), 8: scoreRaw}}})
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.insertVersion(context.Background(), tx, batch22Scope(), versionInsert{itemID: batch22ItemID, no: 3, schemaVersion: 7, source: &source, content: content, scoring: scoring})
	if err != nil || got.ID != batch22VersionID || got.VersionNo != 3 || got.SchemaVersion != 7 || got.ContentHash != contentHash(content) {
		t.Fatalf("version result=%+v error=%v", got, err)
	}
	_ = tx.Rollback()
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22ScoringTemplateCopiesOnlyRubric(t *testing.T) {
	sourceScoring := scoringFixture()
	itemStep := batch22Step{kind: "query", fragment: "FROM question_bank_item WHERE", columns: 12,
		rows: [][]driver.Value{{batch22ItemID, "tenant", batch22BankID, "T1", "mathematics", "grade_8", nil, "active", int64(1), "actor", time.Unix(0, 0), "rubric_template"}}}
	shared := batch22BankStep(batch22BankID, "active")
	shared.fragment = "FOR SHARE OF b"
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "query", fragment: "FROM question_bank_item_version", columns: 15, rows: [][]driver.Value{batch22VersionRow(t, fixtureContent(), sourceScoring)}},
		itemStep, batch22BankStep(batch22BankID, "active"),
		itemStep, batch22BankStep(batch22BankID, "active"), shared,
	})
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	input := scoringFixture()
	input.Answer.StandardAnswer = "caller answer"
	input.Solution.RawText = "caller solution"
	input.Rubric.Points[0].Score = 1
	template := batch22VersionID
	input.TemplateVersionID = &template
	got, err := store.resolveScoringTemplate(context.Background(), tx, batch22Scope(), Item{Kind: "question"}, input)
	if err != nil || got.Answer != input.Answer || got.Solution != input.Solution || got.TemplateVersionID != input.TemplateVersionID ||
		got.Rubric.Points[0].Score != 2.5 || input.Rubric.Points[0].Score != 1 {
		t.Fatalf("template result=%+v error=%v", got, err)
	}
	_ = tx.Rollback()
	assertBatch22Script(t, script, "rollback")
}

func TestBatch22MaterializeRetainsEarlierResultWhenLaterAssetFails(t *testing.T) {
	// 第一题已写入并返回后，第二题附件失效；返回值保留第一题，但事务不得提交或写收据。
	store, script := openBatch22Store(t, []batch22Step{
		{kind: "query", fragment: "SELECT status FROM exam", columns: 1, rows: [][]driver.Value{{"draft"}}},
		{kind: "query", fragment: "INSERT INTO question(", columns: 12,
			rows: [][]driver.Value{{"question", "tenant", batch22ExamID, "", "9", "single_choice", float64(2.5), "stem", []byte("[]"), nil, int64(2), "active"}},
			args: map[int]any{2: "9", 7: 2}},
		{kind: "exec", fragment: "SELECT assessment_apply_default_question_config"},
		{kind: "exec", fragment: "UPDATE question_assessment_config"},
		{kind: "query", fragment: "SELECT lifecycle_status='active'", columns: 1, rows: [][]driver.Value{{false}}, args: map[int]any{1: "asset", 2: "hash"}},
	})
	sources := map[string]Version{
		batch22VersionID: {ID: batch22VersionID, ItemID: batch22ItemID, Content: fixtureContent()},
		batch22ItemID:    {ID: batch22ItemID, ItemID: batch22ItemID, Content: fixtureContent(), Scoring: Scoring{Assets: []Asset{{FileAssetID: "asset", SHA256: "hash"}}}},
	}
	selections := []MaterializeSelection{{VersionID: batch22VersionID, QuestionNo: "9", SortOrder: 2}, {VersionID: batch22ItemID, QuestionNo: "1", SortOrder: 1}}
	got, err := pgMutation(context.Background(), store, batch22Scope(), pgMutationCommand{op: "question_bank.materialize", target: batch22ExamID, action: "read"}, func(tx *sql.Tx) (MaterializeResult, error) {
		out := MaterializeResult{ExamID: batch22ExamID, SourceVersionIDs: []string{}}
		err := materializeQuestions(context.Background(), tx, batch22Scope(), selections, sources, &out)
		return out, err
	})
	if !errors.Is(err, ErrInvalidInput) || len(got.Questions) != 1 || got.Questions[0].QuestionNo != "9" ||
		!reflect.DeepEqual(got.SourceVersionIDs, []string{batch22VersionID}) || got.Revision != 0 {
		t.Fatalf("partial materialize result=%+v error=%v", got, err)
	}
	assertBatch22Script(t, script, "rollback")
}

func assertQuestionBankMutationPersistenceOrder(t *testing.T, failAt string, failure error) {
	t.Helper()
	steps := []batch22Step{
		{kind: "exec", fragment: "pg_advisory_xact_lock"},
		{kind: "query", fragment: "FROM business_command_receipt", columns: 2},
	}
	for _, table := range []string{"audit_log", "event_outbox", "business_command_receipt"} {
		step := batch22Step{kind: "exec", fragment: "INSERT INTO " + table}
		if table == failAt {
			step.err = failure
		}
		steps = append(steps, step)
		if step.err != nil {
			break
		}
	}
	store, script := openBatch22Store(t, steps)
	ctx := commandreceipt.WithID(context.Background(), "batch22-command")
	bank := Bank{ID: batch22BankID, Revision: 4}
	got, err := pgMutation(ctx, store, batch22Scope(), pgMutationCommand{op: "question_bank.update", target: batch22BankID, action: "manage"}, func(*sql.Tx) (Bank, error) {
		script.events = append(script.events, "mutation")
		return bank, nil
	})
	end := "commit"
	if failAt != "" {
		end = "rollback"
		if !errors.Is(err, failure) {
			t.Fatalf("error=%v, want persistence failure", err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if got != bank || script.events[3] != "mutation" {
		t.Fatalf("result=%+v, events=%#v", got, script.events)
	}
	assertBatch22Script(t, script, end)
}

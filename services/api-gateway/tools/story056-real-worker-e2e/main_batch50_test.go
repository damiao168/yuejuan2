package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

type fixtureBatch50Step struct {
	kind, fragment string
	args           []any
	id             string
	changed        int64
	err            error
}

type fixtureBatch50Script struct {
	steps []fixtureBatch50Step
	next  int
	err   error
}

func (s *fixtureBatch50Script) take(kind, query string, args []driver.NamedValue) (fixtureBatch50Step, error) {
	if s.next >= len(s.steps) {
		s.err = fmt.Errorf("unexpected %s: %s", kind, query)
		return fixtureBatch50Step{}, s.err
	}
	step := s.steps[s.next]
	s.next++
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if kind != step.kind || !strings.Contains(query, step.fragment) || !reflect.DeepEqual(values, step.args) {
		s.err = fmt.Errorf("step %d: %s args=%v, want %s %s args=%v", s.next, kind, values, step.kind, step.fragment, step.args)
		return step, s.err
	}
	return step, step.err
}

type fixtureBatch50Connector struct{ script *fixtureBatch50Script }

func (c fixtureBatch50Connector) Connect(context.Context) (driver.Conn, error) {
	return fixtureBatch50Conn{c.script}, nil
}
func (c fixtureBatch50Connector) Driver() driver.Driver { return c }
func (fixtureBatch50Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type fixtureBatch50Conn struct{ script *fixtureBatch50Script }

func (fixtureBatch50Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (fixtureBatch50Conn) Close() error { return nil }
func (fixtureBatch50Conn) Begin() (driver.Tx, error) {
	return nil, errors.New("fixture writes must retain their independent commit boundary")
}
func (c fixtureBatch50Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	return &fixtureBatch50Rows{id: step.id}, nil
}
func (c fixtureBatch50Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step, err := c.script.take("exec", query, args)
	return driver.RowsAffected(step.changed), err
}

type fixtureBatch50Rows struct {
	id   string
	read bool
}

func (*fixtureBatch50Rows) Columns() []string { return []string{"id"} }
func (*fixtureBatch50Rows) Close() error      { return nil }
func (r *fixtureBatch50Rows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = r.id
	return nil
}

func openFixtureBatch50DB(t *testing.T, steps []fixtureBatch50Step) *sql.DB {
	t.Helper()
	script := &fixtureBatch50Script{steps: steps}
	db := sql.OpenDB(fixtureBatch50Connector{script})
	t.Cleanup(func() {
		_ = db.Close()
		if script.err != nil || script.next != len(script.steps) {
			t.Errorf("SQL script error=%v steps=%d/%d", script.err, script.next, len(script.steps))
		}
	})
	return db
}

// 各 UUID 和哈希故意不同，防止领域参数重组后把同类型字符串传给错误占位。
// API 资产的 JSON 数字解码为 float64；SQL 参数仍应转换为 int64。
func TestBatch50RegistrationSeedPreservesScopeTemplateAndWriteOrder(t *testing.T) {
	db := openFixtureBatch50DB(t, []fixtureBatch50Step{
		{kind: "query", fragment: "INSERT INTO submission (", args: []any{"tenant", "exam", "student", "admin"}, id: "submission"},
		{kind: "query", fragment: "INSERT INTO submission_page", args: []any{"tenant", "submission", "source"}, id: "page"},
		{kind: "query", fragment: "INSERT INTO capture_batch", args: []any{"tenant", "exam", "admin"}, id: "batch"},
		{kind: "query", fragment: "INSERT INTO capture_file", args: []any{"tenant", "batch", "source", "source-hash", int64(123), "admin"}, id: "file"},
		{kind: "query", fragment: "INSERT INTO capture_page", args: []any{"tenant", "batch", "file", "submission", "page", "source"}, id: "capture-page"},
		{kind: "query", fragment: "INSERT INTO page_registration_run", args: []any{"tenant", "capture-page", "page", "source", "source-hash", "template", "template-hash"}, id: "registration"},
	})
	got, err := seedRegistrationFixtureDB(t.Context(), db, registrationSeedScope{tenantID: "tenant", adminID: "admin", examID: "exam", studentID: "student"}, lockedTemplate{id: "template", contentHash: "template-hash"}, map[string]any{"id": "source", "hash_sha256": "source-hash", "size_bytes": float64(123)})
	if err != nil || got != (registrationFixture{submissionID: "submission", submissionPageID: "page", registrationRunID: "registration"}) {
		t.Fatalf("registration=%+v error=%v", got, err)
	}
}

func TestBatch50RegistrationCompletionPreservesAssetsAndGuard(t *testing.T) {
	for _, changed := range []int64{0, 1, 2} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			assertRegistrationCompletionAffectedRows(t, changed)
		})
	}
}

func TestBatch50RegistrationInvalidAssetsStopBeforeSQL(t *testing.T) {
	db := openFixtureBatch50DB(t, nil)
	if _, err := seedRegistrationFixtureDB(t.Context(), db, registrationSeedScope{}, lockedTemplate{}, map[string]any{"id": "source", "hash_sha256": "hash", "size_bytes": float64(0)}); err == nil {
		t.Fatal("missing source bytes accepted")
	}
	if _, err := completeRegistrationAndSeedSegmentDB(t.Context(), db, "tenant", "question", lockedTemplate{}, registrationFixture{}, registrationAssets{registered: map[string]any{"id": "registered"}, crop: map[string]any{"id": "crop"}}); err == nil {
		t.Fatal("missing crop hash accepted")
	}
}

func assertRegistrationCompletionAffectedRows(t *testing.T, changed int64) {
	t.Helper()
	steps := []fixtureBatch50Step{{kind: "exec", fragment: "AND processing_status='processing'", args: []any{"tenant", "registration", "registered"}, changed: changed}}
	if changed == 1 {
		steps = append(steps, fixtureBatch50Step{kind: "query", fragment: "INSERT INTO answer_segment", args: []any{"tenant", "submission", "page", "question", "template", "template-hash", "registration", "crop", "crop-hash"}, id: "segment"})
	}
	got, err := completeRegistrationAndSeedSegmentDB(t.Context(), openFixtureBatch50DB(t, steps), "tenant", "question", lockedTemplate{id: "template", contentHash: "template-hash"}, registrationFixture{submissionID: "submission", submissionPageID: "page", registrationRunID: "registration"}, registrationAssets{registered: map[string]any{"id": "registered"}, crop: map[string]any{"id": "crop", "hash_sha256": "crop-hash"}})
	if changed == 1 {
		if err != nil || got != "segment" {
			t.Fatalf("segment=%q error=%v", got, err)
		}
		return
	}
	if err == nil || got != "" || !strings.Contains(err.Error(), fmt.Sprintf("affected %d rows", changed)) {
		t.Fatalf("unexpected affected-row result=%q error=%v", got, err)
	}
}

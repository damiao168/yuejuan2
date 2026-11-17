package modelgovernance

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

type governanceSQLStep struct {
	kind, fragment string
	args           []any
	check          func([]any) error
	columns        int
	rows           [][]driver.Value
	err, streamErr error
}

// 夹具验证租户参数、关键写入顺序和事务边界。它不执行 PostgreSQL 的行锁或约束。
type governanceSQLScript struct {
	steps                []governanceSQLStep
	next, opened, closed int
	failure              error
}

func openGovernanceSQL(t *testing.T, cipher *CredentialCipher, steps ...governanceSQLStep) (*PostgresStore, *governanceSQLScript) {
	t.Helper()
	script := &governanceSQLScript{steps: steps}
	db := sql.OpenDB(governanceSQLConnector{script})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db, cipher), script
}

func (s *governanceSQLScript) requireDone(t *testing.T) {
	t.Helper()
	if s.failure != nil || s.next != len(s.steps) || s.opened != s.closed {
		t.Fatalf("SQL script: %v, steps %d/%d, rows closed %d/%d", s.failure, s.next, len(s.steps), s.closed, s.opened)
	}
}

func (s *governanceSQLScript) take(kind, query string, named []driver.NamedValue) (governanceSQLStep, error) {
	if s.next >= len(s.steps) {
		s.failure = fmt.Errorf("unexpected %s: %s", kind, query)
		return governanceSQLStep{}, s.failure
	}
	step := s.steps[s.next]
	s.next++
	args := make([]any, 0, len(named))
	for _, arg := range named {
		args = append(args, arg.Value)
	}
	if kind != step.kind || !strings.Contains(query, step.fragment) {
		s.failure = fmt.Errorf("step %d: got %s %q, want %s containing %q", s.next, kind, query, step.kind, step.fragment)
	} else if step.args != nil && !reflect.DeepEqual(args, step.args) {
		s.failure = fmt.Errorf("step %d: args = %v, want %v", s.next, args, step.args)
	} else if step.check != nil {
		if err := step.check(args); err != nil {
			s.failure = fmt.Errorf("step %d: %w", s.next, err)
		}
	}
	if s.failure != nil {
		return governanceSQLStep{}, s.failure
	}
	return step, step.err
}

type governanceSQLConnector struct{ script *governanceSQLScript }

func (c governanceSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return governanceSQLConn(c), nil
}
func (c governanceSQLConnector) Driver() driver.Driver { return c }
func (governanceSQLConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type governanceSQLConn struct{ script *governanceSQLScript }

func (governanceSQLConn) Close() error                             { return nil }
func (governanceSQLConn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (governanceSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (c governanceSQLConn) Begin() (driver.Tx, error) {
	_, err := c.script.take("begin", "", nil)
	return c, err
}
func (c governanceSQLConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c.Begin()
}
func (c governanceSQLConn) Commit() error {
	_, err := c.script.take("commit", "", nil)
	return err
}
func (c governanceSQLConn) Rollback() error {
	_, err := c.script.take("rollback", "", nil)
	return err
}
func (c governanceSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c governanceSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.script.opened++
	return &governanceSQLRows{script: c.script, step: step}, nil
}

type governanceSQLRows struct {
	script *governanceSQLScript
	step   governanceSQLStep
	next   int
}

func (r *governanceSQLRows) Columns() []string { return make([]string, r.step.columns) }
func (r *governanceSQLRows) Close() error {
	r.script.closed++
	return nil
}
func (r *governanceSQLRows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		if r.step.streamErr != nil {
			return r.step.streamErr
		}
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

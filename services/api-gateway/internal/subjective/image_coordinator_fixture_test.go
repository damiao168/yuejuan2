package subjective

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

// 驱动只提供数据库边界输入并记录事务顺序，实际执行正式存储方法和 HTTP 协议。
// PostgreSQL 参数类型、约束及真实回滚仍由 image_coordinator_postgres_test.go 验证。
type imageSQLStep struct {
	kind, contains string
	values         []driver.Value
	err            error
	check          func(string, []driver.NamedValue) error
}

type imageSQLScript struct {
	t     *testing.T
	mu    sync.Mutex
	steps []imageSQLStep
}

func imageQuery(contains string, values ...driver.Value) imageSQLStep {
	return imageSQLStep{kind: "query", contains: contains, values: values}
}

func imageTestDB(t *testing.T, steps ...imageSQLStep) *sql.DB {
	t.Helper()
	script := &imageSQLScript{t: t, steps: steps}
	db := sql.OpenDB(imageSQLConnector{script})
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = db.Close()
		script.mu.Lock()
		defer script.mu.Unlock()
		if len(script.steps) != 0 {
			t.Errorf("database boundary not reached: next=%s %s, remaining=%d", script.steps[0].kind, script.steps[0].contains, len(script.steps))
		}
	})
	return db
}

func (s *imageSQLScript) next(kind, query string, args []driver.NamedValue) (imageSQLStep, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) == 0 {
		err := fmt.Errorf("unexpected %s after expected database operations", kind)
		s.t.Error(err)
		return imageSQLStep{}, err
	}
	step := s.steps[0]
	s.steps = s.steps[1:]
	if step.kind != kind || !strings.Contains(query, step.contains) {
		err := fmt.Errorf("database operation: got %s, want %s containing %q", kind, step.kind, step.contains)
		s.t.Error(err)
		return step, err
	}
	if step.check != nil {
		if err := step.check(query, args); err != nil {
			s.t.Error(err)
			return step, err
		}
	}
	return step, step.err
}

type imageSQLConnector struct{ script *imageSQLScript }

func (c imageSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &imageSQLConn{c.script}, nil
}
func (c imageSQLConnector) Driver() driver.Driver            { return c }
func (c imageSQLConnector) Open(string) (driver.Conn, error) { return c.Connect(context.Background()) }

type imageSQLConn struct{ script *imageSQLScript }

func (*imageSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepared statements are outside this fixture")
}
func (*imageSQLConn) Close() error { return nil }
func (c *imageSQLConn) Begin() (driver.Tx, error) {
	_, err := c.script.next("begin", "", nil)
	return c, err
}
func (c *imageSQLConn) Commit() error   { _, err := c.script.next("commit", "", nil); return err }
func (c *imageSQLConn) Rollback() error { _, err := c.script.next("rollback", "", nil); return err }
func (c *imageSQLConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.next("query", query, args)
	if err != nil {
		return nil, err
	}
	return &imageSQLRows{values: step.values}, nil
}
func (c *imageSQLConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.next("exec", query, args)
	return driver.RowsAffected(1), err
}

type imageSQLRows struct {
	values []driver.Value
	done   bool
}

func (r *imageSQLRows) Columns() []string {
	n := len(r.values)
	if n == 0 {
		n = 1
	}
	columns := make([]string, n)
	for i := range columns {
		columns[i] = fmt.Sprint(i)
	}
	return columns
}
func (*imageSQLRows) Close() error { return nil }
func (r *imageSQLRows) Next(values []driver.Value) error {
	if r.done || len(r.values) == 0 {
		return io.EOF
	}
	r.done = true
	copy(values, r.values)
	return nil
}

func imageJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

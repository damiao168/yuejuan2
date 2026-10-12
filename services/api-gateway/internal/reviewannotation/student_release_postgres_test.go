package reviewannotation

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// 脚本驱动让发布在两次查询之间变化，核对代码确实使用同一只读重复读事务。
// 它验证事务边界，不替代真实 PostgreSQL 的快照与并发集成验收。
func TestStudentAnnotationVersionCheckAndQueryUseSameReadSnapshot(t *testing.T) {
	scenario := &studentReleaseScenario{current: "release-old"}
	db := sql.OpenDB(studentReleaseConnector{scenario})
	defer db.Close()
	store := NewPostgresStore(db)
	items, err := store.ListStudentQuestionAnnotations(context.Background(), "tenant-a", "exam-a", "student-a", "question-a", "release-old")
	if err != nil || len(items) != 1 || items[0].Content != "old release evidence" {
		t.Fatalf("snapshot evidence: %+v %v", items, err)
	}
	if scenario.queries != 2 || scenario.current != "release-new" || !scenario.rolledBack {
		t.Fatalf("scenario: %+v", scenario)
	}
	// 后续请求已看到新指针，旧版期望必须在读取批注前被拒绝。
	scenario.queries = 0
	scenario.rolledBack = false
	if _, err := store.ListStudentQuestionAnnotations(context.Background(), "tenant-a", "exam-a", "student-a", "question-a", "release-old"); !errors.Is(err, ErrStudentReleaseChanged) {
		t.Fatalf("changed release: %v", err)
	}
	if scenario.queries != 1 || !scenario.rolledBack {
		t.Fatalf("changed request queried annotations: %+v", scenario)
	}
}

type studentReleaseScenario struct {
	current, snapshot         string
	queries                   int
	inTransaction, rolledBack bool
}
type studentReleaseConnector struct{ scenario *studentReleaseScenario }

func (c studentReleaseConnector) Connect(context.Context) (driver.Conn, error) {
	return &studentReleaseConn{c.scenario}, nil
}
func (c studentReleaseConnector) Driver() driver.Driver { return studentReleaseDriver(c) }

type studentReleaseDriver struct{ scenario *studentReleaseScenario }

func (d studentReleaseDriver) Open(string) (driver.Conn, error) {
	return &studentReleaseConn{d.scenario}, nil
}

type studentReleaseConn struct{ scenario *studentReleaseScenario }

func (c *studentReleaseConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (c *studentReleaseConn) Close() error { return nil }
func (c *studentReleaseConn) Begin() (driver.Tx, error) {
	return nil, errors.New("must declare transaction isolation")
}
func (c *studentReleaseConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if !options.ReadOnly || options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		return nil, errors.New("student evidence requires read-only repeatable-read snapshot")
	}
	c.scenario.inTransaction = true
	c.scenario.snapshot = c.scenario.current
	return studentReleaseTx{c.scenario}, nil
}
func (c *studentReleaseConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !c.scenario.inTransaction {
		return nil, errors.New("query outside version snapshot")
	}
	c.scenario.queries++
	if strings.Contains(query, "SELECT release_id::text FROM score_release_current") {
		if len(args) != 2 || args[0].Value != "tenant-a" || args[1].Value != "exam-a" {
			return nil, errors.New("invalid release scope")
		}
		c.scenario.current = "release-new"
		return &studentReleaseRows{columns: []string{"release_id"}, values: [][]driver.Value{{c.scenario.snapshot}}}, nil
	}
	if !strings.Contains(query, "ra.visibility = 'student_after_publish'") || len(args) != 4 || args[2].Value != "student-a" || args[3].Value != "question-a" {
		return nil, errors.New("invalid student annotation scope")
	}
	now := time.Now().UTC()
	return &studentReleaseRows{columns: []string{"id", "segment", "page", "type", "space", "x", "y", "width", "height", "content", "created", "updated"}, values: [][]driver.Value{{"annotation-1", "segment-1", "page-1", AnnotationNote, CanonicalImageNormalized, .1, .1, .2, .2, "old release evidence", now, now}}}, nil
}

type studentReleaseTx struct{ scenario *studentReleaseScenario }

func (tx studentReleaseTx) Commit() error { return errors.New("read-only evidence must not write") }
func (tx studentReleaseTx) Rollback() error {
	tx.scenario.inTransaction = false
	tx.scenario.rolledBack = true
	return nil
}

type studentReleaseRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *studentReleaseRows) Columns() []string { return r.columns }
func (r *studentReleaseRows) Close() error      { return nil }
func (r *studentReleaseRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

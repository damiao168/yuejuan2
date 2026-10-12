package processing

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// 脚本驱动执行真实列表扫描路径，核对最少人员摘要的租户/学校与页号的考试边界；不替代真实 PostgreSQL 集成测试。
func TestPostgresExceptionListIncludesScopedReadableSummary(t *testing.T) {
	now := time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)
	row := []driver.Value{"exception", "exam", "page", "quality", "run", string(IssueLowImageQuality), "P1", true, "assigned", "teacher", []byte("{}"), now, now, nil, "", "", "", "张老师", "zhang", "00123", int64(2)}
	connector := exceptionPresentationConnector{t: t, row: row}
	db := sql.OpenDB(connector)
	defer db.Close()
	result, err := NewPostgresStore(db).ListExceptions(t.Context(), "tenant", ExceptionFilter{ExamID: "exam"})
	if err != nil || len(result.Exceptions) != 1 {
		t.Fatalf("list: %+v %v", result, err)
	}
	item := result.Exceptions[0]
	if item.AssignedToName != "张老师" || item.AssignedToUsername != "zhang" || item.CandidateNo != "00123" || item.PageNo != 2 || item.AssignedTo != "teacher" || result.ProjectedAt == nil {
		t.Fatalf("lost presentation or authority fields: %+v", result)
	}
}

type exceptionPresentationConnector struct {
	t   *testing.T
	row []driver.Value
}

func (c exceptionPresentationConnector) Connect(context.Context) (driver.Conn, error) {
	return exceptionPresentationConn{c}, nil
}
func (c exceptionPresentationConnector) Driver() driver.Driver { return exceptionPresentationDriver{c} }

type exceptionPresentationDriver struct {
	config exceptionPresentationConnector
}

func (d exceptionPresentationDriver) Open(string) (driver.Conn, error) {
	return exceptionPresentationConn(d), nil
}

type exceptionPresentationConn struct {
	config exceptionPresentationConnector
}

func (exceptionPresentationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected mutation")
}
func (exceptionPresentationConn) Close() error { return nil }
func (exceptionPresentationConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected mutation")
}
func (c exceptionPresentationConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(args) < 2 || args[0].Value != "tenant" || args[1].Value != "exam" {
		return nil, errors.New("wrong tenant or exam arguments")
	}
	if strings.Contains(query, "MAX(projected_at)") {
		return &exceptionPresentationRows{rows: [][]driver.Value{{c.config.row[12]}}}, nil
	}
	for _, scope := range []string{"assignee.tenant_id=e.tenant_id", "assignee.id=e.assigned_to", "assignee.school_id=x.school_id", "assignee.deleted_at IS NULL", "page.tenant_id=e.tenant_id", "sub.tenant_id=page.tenant_id", "sub.exam_id=e.exam_id", "CASE WHEN sub.id IS NOT NULL THEN page.page_no", "WHERE e.tenant_id=$1::uuid"} {
		if !strings.Contains(query, scope) {
			return nil, fmt.Errorf("missing presentation scope %s", scope)
		}
	}
	return &exceptionPresentationRows{rows: [][]driver.Value{c.config.row}}, nil
}

type exceptionPresentationRows struct {
	rows  [][]driver.Value
	index int
}

func (r *exceptionPresentationRows) Columns() []string {
	cols := make([]string, len(r.rows[0]))
	for i := range cols {
		cols[i] = fmt.Sprint(i)
	}
	return cols
}
func (*exceptionPresentationRows) Close() error { return nil }
func (r *exceptionPresentationRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}

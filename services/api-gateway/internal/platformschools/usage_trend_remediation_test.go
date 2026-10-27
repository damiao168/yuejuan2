package platformschools

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

type adversarialUsageConnector struct{ failAfter bool }

func (c adversarialUsageConnector) Connect(context.Context) (driver.Conn, error) {
	return adversarialUsageConnection{failAfter: c.failAfter}, nil
}
func (adversarialUsageConnector) Driver() driver.Driver { return adversarialUsageDriver{} }

type adversarialUsageDriver struct{}

func (adversarialUsageDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector only")
}

type adversarialUsageConnection struct{ failAfter bool }

func (adversarialUsageConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("no prepare")
}
func (adversarialUsageConnection) Close() error { return nil }
func (adversarialUsageConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("no transactions")
}
func (c adversarialUsageConnection) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "SELECT usage_date::text") {
		return &adversarialUsageRows{columns: 5, values: [][]driver.Value{{"2026-10-01", int64(20), int64(10), int64(10), int64(1)}}, failAfter: c.failAfter}, nil
	}
	if strings.Contains(query, "GROUP BY feature") || strings.Contains(query, "GROUP BY model_name") {
		return &adversarialUsageRows{columns: 4}, nil
	}
	return &adversarialUsageRows{columns: 8, values: [][]driver.Value{{int64(20), int64(20), int64(0), int64(0), int64(40), int64(2), int64(0), int64(0)}}}, nil
}

type adversarialUsageRows struct {
	columns   int
	values    [][]driver.Value
	position  int
	failAfter bool
}

func (r *adversarialUsageRows) Columns() []string { return make([]string, r.columns) }
func (*adversarialUsageRows) Close() error        { return nil }
func (r *adversarialUsageRows) Next(dest []driver.Value) error {
	if r.position < len(r.values) {
		copy(dest, r.values[r.position])
		r.position++
		return nil
	}
	if r.failAfter {
		return io.ErrUnexpectedEOF
	}
	return io.EOF
}

// 真实 database/sql Rows 在读到第一天后返回传输错误；仅替换驱动，不启动数据库。
// 失败必须返回到调用方，不能把部分趋势标为完整成功；不是 PostgreSQL 集成测试。
func TestUsageTrendIterationErrorRejectsPartialSuccess(t *testing.T) {
	db := sql.OpenDB(adversarialUsageConnector{failAfter: true})
	defer db.Close()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	result, err := NewPostgresStore(db).GetUsage(context.Background(), "synthetic-tenant", UsageRange{Start: start, End: start.Add(24 * time.Hour), Days: 2})
	if !errors.Is(err, io.ErrUnexpectedEOF) || len(result.Trend) != 0 {
		t.Fatalf("partial-trend accepted: err=%v result=%+v", err, result)
	}
}

func TestUsageTrendCompleteIteratorStillSucceeds(t *testing.T) {
	db := sql.OpenDB(adversarialUsageConnector{})
	defer db.Close()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	result, err := NewPostgresStore(db).GetUsage(context.Background(), "synthetic-tenant", UsageRange{Start: start, End: start.Add(24 * time.Hour), Days: 2})
	if err != nil || len(result.Trend) != 1 || result.Trend[0].TotalTokens != 20 {
		t.Fatalf("complete trend rejected: %#v %v", result, err)
	}
}

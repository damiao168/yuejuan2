package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"
)

type printLedgerQuery struct {
	fragments []string
	args      []driver.Value
	columns   int
	values    [][]driver.Value
	queryErr  error
	readErr   error
}

type printLedgerConnector struct {
	queries []printLedgerQuery
	next    int
	closed  int
}

func (c *printLedgerConnector) Connect(context.Context) (driver.Conn, error) {
	return &printLedgerConnection{connector: c}, nil
}
func (c *printLedgerConnector) Driver() driver.Driver { return printLedgerDriver{} }

type printLedgerDriver struct{}

func (printLedgerDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use the test connector")
}

type printLedgerConnection struct{ connector *printLedgerConnector }

func (*printLedgerConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared query")
}
func (*printLedgerConnection) Close() error { return nil }
func (*printLedgerConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected write transaction")
}
func (c *printLedgerConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.connector.next >= len(c.connector.queries) {
		return nil, errors.New("unexpected ledger query")
	}
	step := c.connector.queries[c.connector.next]
	c.connector.next++
	for _, fragment := range step.fragments {
		if !strings.Contains(query, fragment) {
			return nil, fmt.Errorf("ledger query is missing %q", fragment)
		}
	}
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if !reflect.DeepEqual(values, step.args) {
		return nil, fmt.Errorf("ledger query args=%v want=%v", values, step.args)
	}
	if step.queryErr != nil {
		return nil, step.queryErr
	}
	return &printLedgerRows{step: step, connector: c.connector}, nil
}

type printLedgerRows struct {
	step      printLedgerQuery
	connector *printLedgerConnector
	next      int
}

func (r *printLedgerRows) Columns() []string {
	names := make([]string, r.step.columns)
	for i := range names {
		names[i] = fmt.Sprintf("column_%d", i)
	}
	return names
}
func (r *printLedgerRows) Close() error { r.connector.closed++; return nil }
func (r *printLedgerRows) Next(out []driver.Value) error {
	if r.next < len(r.step.values) {
		copy(out, r.step.values[r.next])
		r.next++
		return nil
	}
	if r.step.readErr != nil {
		err := r.step.readErr
		r.step.readErr = nil
		return err
	}
	return io.EOF
}

func printLedgerStore(t *testing.T, queries []printLedgerQuery) *PostgresStore {
	t.Helper()
	connector := &printLedgerConnector{queries: queries}
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		_ = db.Close()
		if connector.next != len(queries) {
			t.Errorf("ledger consumed %d of %d expected queries", connector.next, len(queries))
		}
		wantClosed := 0
		for _, step := range queries {
			if step.queryErr == nil {
				wantClosed++
			}
		}
		if connector.closed != wantClosed {
			t.Errorf("ledger row readers closed=%d want=%d", connector.closed, wantClosed)
		}
	})
	return NewPostgresStore(db)
}

func printLedgerPackageQueries() []printLedgerQuery {
	issued := time.Date(2026, 11, 16, 8, 0, 0, 0, time.FixedZone("test-offset", 8*3600))
	layout := []byte(`{"pages":[{"page_no":1,"width":1000,"height":1400},{"page_no":2,"width":1400,"height":1000}]}`)
	return []printLedgerQuery{
		{fragments: []string{"FROM answer_sheet_print_batch", "e.tenant_id=b.tenant_id", "t.tenant_id=b.tenant_id", "b.tenant_id=$1 AND b.id=$2::uuid"},
			args: []driver.Value{"tenant", "batch"}, columns: 11,
			values: [][]driver.Value{{"batch", "exam", "synthetic exam", "template", "frozen-hash", "key", issued, int64(2), int64(2), layout, nil}}},
		{fragments: []string{"FROM answer_sheet_print_sheet", "p.tenant_id=sh.tenant_id", "sh.tenant_id=$1 AND sh.print_batch_id=$2::uuid", "ORDER BY sh.ordinal,p.page_no"},
			args: []driver.Value{"tenant", "batch"}, columns: 6,
			values: [][]driver.Value{
				{"student-a", "sheet-a", int64(1), "issued", int64(1), "barcode-a1"},
				{"student-a", "sheet-a", int64(1), "issued", int64(2), "barcode-a2"},
				{"student-b", "sheet-b", int64(2), "issued", int64(1), "barcode-b1"},
				{"student-b", "sheet-b", int64(2), "issued", int64(2), "barcode-b2"},
			}},
	}
}

func TestPrintLedgerReadKeepsStudentSheetOrderAndFrozenPageGeometry(t *testing.T) {
	queries := printLedgerPackageQueries()
	background := samplePrintBackground(t)
	digest := sha256.Sum256(background.payload)
	sourceHash := hex.EncodeToString(digest[:])
	queries[0].values[0][10] = printLayoutJSON(&StudentPrintLayout{SourceSHA256: sourceHash, Pages: []StudentPrintQRPage{
		{PageNo: 1, X: .75, Y: .05, Width: .2, Height: .18},
		{PageNo: 2, X: .75, Y: .05, Width: .2, Height: .18},
	}})
	queries = append(queries, printLedgerQuery{
		fragments: []string{"FROM answer_sheet_template t", "p.exam_id=t.exam_id", "f.lifecycle_status='active'"},
		args:      []driver.Value{"tenant", "template", "frozen-hash"}, columns: 6,
		values: [][]driver.Value{{"asset", "bucket", "source.pdf", "application/pdf", sourceHash, int64(len(background.payload))}},
	})
	store := printLedgerStore(t, queries)
	objects := files.NewMemoryObjectStorage()
	if err := objects.Put(t.Context(), "bucket", "source.pdf", bytes.NewReader(background.payload), int64(len(background.payload)), "application/pdf"); err != nil {
		t.Fatal(err)
	}
	store.printObjects = objects
	pkg, err := store.GetStudentPrintPackage(t.Context(), "tenant", "batch")
	if err != nil || pkg.PrintBatchID != "batch" || pkg.TemplateContentHash != "frozen-hash" || pkg.IssuedAt.Location() != time.UTC || len(pkg.Sheets) != 2 {
		t.Fatalf("unexpected print package header: %+v err=%v", pkg, err)
	}
	if pkg.QRLayout == nil || pkg.QRLayout.SourceSHA256 != sourceHash || len(pkg.QRLayout.Pages) != 2 || pkg.background == nil || !bytes.Equal(pkg.background.payload, background.payload) {
		t.Fatalf("print package changed the confirmed QR layout or frozen source: %+v", pkg.QRLayout)
	}
	for i, sheet := range pkg.Sheets {
		student, serial, barcode := "student-a", "sheet-a", "barcode-a"
		if i == 1 {
			student, serial, barcode = "student-b", "sheet-b", "barcode-b"
		}
		if sheet.StudentID != student || sheet.SheetSerial != serial || sheet.Ordinal != i+1 || len(sheet.Pages) != 2 ||
			sheet.Pages[0].PageNo != 1 || sheet.Pages[0].BarcodeValue != barcode+"1" || sheet.Pages[0].TemplateWidth != 1000 || sheet.Pages[0].TemplateHeight != 1400 ||
			sheet.Pages[1].PageNo != 2 || sheet.Pages[1].BarcodeValue != barcode+"2" || sheet.Pages[1].TemplateWidth != 1400 || sheet.Pages[1].TemplateHeight != 1000 {
			t.Fatalf("ledger mixed student identity or page geometry: %+v", sheet)
		}
	}
}

func TestPrintLedgerReadRejectsIncompleteOrNonIssuedSheetsWithoutPartialPackage(t *testing.T) {
	readFailure := errors.New("synthetic page stream failed")
	for _, scenario := range []struct {
		name   string
		change func([]printLedgerQuery) []printLedgerQuery
		want   error
	}{
		{"missing batch", func(q []printLedgerQuery) []printLedgerQuery { q[0].values = nil; return q[:1] }, ErrNotFound},
		{"invalid frozen layout", func(q []printLedgerQuery) []printLedgerQuery {
			q[0].values[0][9] = []byte(`{"pages":[]}`)
			return q[:1]
		}, ErrInvalidTransition},
		{"query unavailable", func(q []printLedgerQuery) []printLedgerQuery { q[1].queryErr = readFailure; return q }, readFailure},
		{"observed sheet", func(q []printLedgerQuery) []printLedgerQuery { q[1].values[2][3] = "observed"; return q }, ErrInvalidTransition},
		{"revoked sheet", func(q []printLedgerQuery) []printLedgerQuery { q[1].values[2][3] = "revoked"; return q }, ErrInvalidTransition},
		{"page outside frozen template", func(q []printLedgerQuery) []printLedgerQuery { q[1].values[3][4] = int64(3); return q }, ErrInvalidTransition},
		{"missing last page", func(q []printLedgerQuery) []printLedgerQuery { q[1].values = q[1].values[:3]; return q }, ErrInvalidTransition},
		{"missing second sheet", func(q []printLedgerQuery) []printLedgerQuery { q[1].values = q[1].values[:2]; return q }, ErrInvalidTransition},
		{"page stream interrupted", func(q []printLedgerQuery) []printLedgerQuery { q[1].readErr = readFailure; return q }, readFailure},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// 模拟 SQL 读取边界。后续纸张或游标失败时，已经读取的前一名学生也不能作为半份包返回。
			store := printLedgerStore(t, scenario.change(printLedgerPackageQueries()))
			pkg, err := store.GetStudentPrintPackage(t.Context(), "tenant", "batch")
			if !errors.Is(err, scenario.want) || pkg.PrintBatchID != "" || len(pkg.Sheets) != 0 {
				t.Fatalf("ledger returned partial printable facts: package=%+v err=%v want=%v", pkg, err, scenario.want)
			}
		})
	}
}

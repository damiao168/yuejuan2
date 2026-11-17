package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type formalExportTransactionProbe struct {
	publicationTxProbe
	options   driver.TxOptions
	commitErr error
}

type formalExportConnector struct{ probe *formalExportTransactionProbe }

func (c formalExportConnector) Connect(context.Context) (driver.Conn, error) {
	return &formalExportConnection{
		publicationTxConnection: publicationTxConnection{
			studentBatch50Conn: studentBatch50Conn{script: &c.probe.studentBatch50Script},
			probe:              &c.probe.publicationTxProbe,
		},
		probe: c.probe,
	}, nil
}
func (formalExportConnector) Driver() driver.Driver { return batch48ReleaseConnector{} }

type formalExportConnection struct {
	publicationTxConnection
	probe    *formalExportTransactionProbe
	activeTx bool
}

func (c *formalExportConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.probe.options = options
	c.activeTx = true
	return c, nil
}

// 每条读取都必须来自已开启快照的连接，不能只靠共享查询脚本模拟事务内读取。
func (c *formalExportConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if !c.activeTx {
		err := errors.New("formal export queried outside its snapshot transaction")
		c.probe.err = err
		return nil, err
	}
	return c.publicationTxConnection.QueryContext(ctx, query, args)
}

func (c *formalExportConnection) Commit() error {
	c.activeTx = false
	c.probe.committed = true
	return c.probe.commitErr
}

func (c *formalExportConnection) Rollback() error {
	c.activeTx = false
	return c.publicationTxConnection.Rollback()
}

func formalExportSQLSteps() []studentBatch50Step {
	const examID = "11111111-1111-4111-8111-111111111111"
	const englishExamID = "22222222-2222-4222-8222-222222222222"
	const ids = examID + "," + englishExamID
	return []studentBatch50Step{
		{fragment: "FROM exam e JOIN school", args: []any{"tenant", examID}, columns: 4, rows: [][]driver.Value{{"session", "school", "frozen exam name", "school name"}}},
		{fragment: "LIMIT 21", args: []any{"tenant", examID, "session"}, columns: 7, rows: [][]driver.Value{
			{examID, "mathematics", "school", "grading", "math-release-v2", int64(2), []byte(`{}`)},
			{englishExamID, "english", "school", "published", "english-release-v3", int64(3), []byte(`{}`)},
		}},
		{fragment: "NOT EXISTS(SELECT 1 FROM exam_candidate_snapshot", args: []any{"tenant", ids}, columns: 1, rows: [][]driver.Value{{false}}},
		{fragment: "ORDER BY c.exam_id,c.student_id LIMIT 200001", args: []any{"tenant", ids}, columns: 10, rows: [][]driver.Value{
			{examID, "student", "00001", "frozen student", "grade", "frozen grade", "class", "frozen class", float64(7), float64(10)},
			{englishExamID, "student", "00001", "frozen student", "grade", "frozen grade", "class", "frozen class", float64(8), float64(10)},
		}},
	}
}

func openFormalExportProbe(t *testing.T, steps []studentBatch50Step, commitErr error) (*PostgresStore, *formalExportTransactionProbe) {
	t.Helper()
	probe := &formalExportTransactionProbe{
		publicationTxProbe: publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: steps}},
		commitErr:          commitErr,
	}
	db := sql.OpenDB(formalExportConnector{probe: probe})
	t.Cleanup(func() {
		_ = db.Close()
		if probe.err != nil || probe.next != len(steps) {
			t.Errorf("formal export SQL error=%v consumed=%d/%d", probe.err, probe.next, len(steps))
		}
	})
	return NewPostgresStore(db, nil), probe
}

func TestFormalExportSQLPinsEverySubjectInReadOnlySnapshot(t *testing.T) {
	const examID = "11111111-1111-4111-8111-111111111111"
	store, probe := openFormalExportProbe(t, formalExportSQLSteps(), nil)
	scope := auth.AccessScope{TenantID: "tenant", ActorID: "manager", TenantWide: true}
	result, err := store.ExportCurrentPublishedCSV(t.Context(), scope, examID, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	if err != nil || result.RowCount != 1 || result.Data == nil {
		t.Fatalf("formal export did not return complete frozen facts: result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(result.Pins, []formalExportPin{
		{ExamID: examID, Subject: "mathematics", ReleaseID: "math-release-v2", Version: 2},
		{ExamID: "22222222-2222-4222-8222-222222222222", Subject: "english", ReleaseID: "english-release-v3", Version: 3},
	}) {
		t.Fatalf("formal export did not preserve published subject versions: %+v", result.Pins)
	}
	rows := formalTestRows(t, result.Content)
	if len(rows) != 1 || rows[0]["学号"] != "00001" || rows[0]["姓名"] != "frozen student" || rows[0]["考试班级"] != "frozen class" ||
		rows[0]["数学·成绩"] != "7.00" || rows[0]["英语·成绩"] != "8.00" || rows[0]["数学·发布版本ID"] != "math-release-v2" || rows[0]["英语·发布版本ID"] != "english-release-v3" {
		t.Fatalf("export mixed current identity or working scores into frozen facts: %+v", rows)
	}
	// 同一次事务固定各科发布指针，SQL 替身只验证适配器的事务选项和查询边界。
	if probe.options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) || !probe.options.ReadOnly || !probe.committed || probe.aborted {
		t.Fatalf("formal export lost read snapshot boundary: options=%+v commit=%t rollback=%t", probe.options, probe.committed, probe.aborted)
	}
}

func TestFormalExportSQLRejectsIncompleteFactsWithoutPartialFile(t *testing.T) {
	const examID = "11111111-1111-4111-8111-111111111111"
	failure := errors.New("synthetic formal export read failure")
	for _, scenario := range []struct {
		name      string
		change    func([]studentBatch50Step) []studentBatch50Step
		scope     auth.AccessScope
		commitErr error
		want      error
	}{
		{"missing exam", func(q []studentBatch50Step) []studentBatch50Step { q[0].rows = nil; return q[:1] }, auth.AccessScope{}, nil, ErrNotFound},
		{"no school management", func(q []studentBatch50Step) []studentBatch50Step { return q[:1] }, auth.AccessScope{TenantID: "tenant", ActorID: "manager", ExamIDs: []string{examID}}, nil, ErrForbidden},
		{"unpublished subject", func(q []studentBatch50Step) []studentBatch50Step { q[1].rows[1][4] = ""; return q[:2] }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"draft subject", func(q []studentBatch50Step) []studentBatch50Step { q[1].rows[1][3] = "draft"; return q[:2] }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"subject in another school", func(q []studentBatch50Step) []studentBatch50Step { q[1].rows[1][2] = "other-school"; return q[:2] }, auth.AccessScope{}, nil, ErrForbidden},
		{"invalid published visibility", func(q []studentBatch50Step) []studentBatch50Step { q[1].rows[1][6] = []byte(`{"broken"`); return q[:2] }, auth.AccessScope{}, nil, nil},
		{"no published subjects", func(q []studentBatch50Step) []studentBatch50Step { q[1].rows = nil; return q[:2] }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"published student outside roster", func(q []studentBatch50Step) []studentBatch50Step { q[2].rows[0][0] = true; return q[:3] }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"roster query failed", func(q []studentBatch50Step) []studentBatch50Step { q[3].err = failure; return q }, auth.AccessScope{}, nil, failure},
		{"unknown subject in roster", func(q []studentBatch50Step) []studentBatch50Step { q[3].rows[1][0] = "another-exam"; return q }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"missing subject roster", func(q []studentBatch50Step) []studentBatch50Step { q[3].rows = q[3].rows[:1]; return q }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"historical identity differs", func(q []studentBatch50Step) []studentBatch50Step { q[3].rows[1][6] = "current-class"; return q }, auth.AccessScope{}, nil, errFormalExportIncomplete},
		{"snapshot commit failure", func(q []studentBatch50Step) []studentBatch50Step { return q }, auth.AccessScope{}, failure, failure},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			scope := scenario.scope
			if scope.TenantID == "" {
				scope = auth.AccessScope{TenantID: "tenant", ActorID: "manager", TenantWide: true}
			}
			store, probe := openFormalExportProbe(t, scenario.change(formalExportSQLSteps()), scenario.commitErr)
			result, err := store.ExportCurrentPublishedCSV(t.Context(), scope, examID, time.Now().UTC())
			if err == nil || scenario.want != nil && !errors.Is(err, scenario.want) || len(result.Content) != 0 || len(result.Pins) != 0 || result.Data != nil {
				t.Fatalf("incomplete export exposed a partial file: result=%+v err=%v want=%v", result, err, scenario.want)
			}
			if probe.committed != (scenario.commitErr != nil) || probe.aborted != (scenario.commitErr == nil) {
				t.Fatalf("failed export transaction mismatch: commit=%t rollback=%t err=%v", probe.committed, probe.aborted, err)
			}
		})
	}
}

func TestFormalExportSQLRejectsInvalidCallerBeforeOpeningSnapshot(t *testing.T) {
	for _, scenario := range []struct {
		scope auth.AccessScope
		exam  string
		want  error
	}{
		{auth.AccessScope{TenantID: "tenant", ActorID: "manager", TenantWide: true}, "invalid-exam", ErrInvalidInput},
		{auth.AccessScope{ActorID: "manager", TenantWide: true}, "11111111-1111-4111-8111-111111111111", ErrForbidden},
	} {
		store, probe := openFormalExportProbe(t, nil, nil)
		result, err := store.ExportCurrentPublishedCSV(t.Context(), scenario.scope, scenario.exam, time.Now().UTC())
		if !errors.Is(err, scenario.want) || len(result.Content) != 0 || probe.committed || probe.aborted {
			t.Fatalf("invalid caller reached snapshot: result=%+v err=%v", result, err)
		}
	}
}

package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type batch48ReleaseSQL struct {
	query string
	args  []driver.Value
	row   []driver.Value
	err   error
}

type batch48ReleaseConnector struct{ state *batch48ReleaseSQL }

func (c batch48ReleaseConnector) Connect(context.Context) (driver.Conn, error) {
	return &batch48ReleaseConn{state: c.state}, nil
}
func (c batch48ReleaseConnector) Driver() driver.Driver { return c }
func (batch48ReleaseConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type batch48ReleaseConn struct{ state *batch48ReleaseSQL }

func (*batch48ReleaseConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*batch48ReleaseConn) Close() error                { return nil }
func (c *batch48ReleaseConn) Begin() (driver.Tx, error) { return c, nil }
func (*batch48ReleaseConn) Commit() error               { return nil }
func (*batch48ReleaseConn) Rollback() error             { return nil }
func (c *batch48ReleaseConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.query = query
	for _, arg := range args {
		c.state.args = append(c.state.args, arg.Value)
	}
	if c.state.err != nil {
		return nil, c.state.err
	}
	return &batch48ReleaseRows{row: c.state.row}, nil
}

type batch48ReleaseRows struct {
	row  []driver.Value
	read bool
}

func (r *batch48ReleaseRows) Columns() []string { return make([]string, len(r.row)) }
func (*batch48ReleaseRows) Close() error        { return nil }
func (r *batch48ReleaseRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.row)
	return nil
}

func batch48ReleaseTx(t *testing.T, state *batch48ReleaseSQL) *sql.Tx {
	t.Helper()
	db := sql.OpenDB(batch48ReleaseConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestBatch48ReleaseDraftKeepsInsertArgumentsAndDatabaseResult(t *testing.T) {
	for _, source := range []string{SourceInitial, SourceRollback, SourceRegrade} {
		t.Run(source, func(t *testing.T) {
			assertReleaseDraftDatabaseOwnership(t, source)
		})
	}
}

func TestBatch48ReleaseDraftReturnsInsertFailure(t *testing.T) {
	dbErr := errors.New("release insert failed")
	state := &batch48ReleaseSQL{err: dbErr}
	got, err := NewPostgresStore(nil, nil).insertReleaseTx(t.Context(), batch48ReleaseTx(t, state), Release{})
	if !errors.Is(err, dbErr) || !reflect.DeepEqual(got, Release{}) {
		t.Fatalf("insert failure changed: release=%#v err=%v", got, err)
	}
}

func TestBatch48ReleaseDraftWindowMarshalFailureStopsBeforeSQL(t *testing.T) {
	invalid := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	state := &batch48ReleaseSQL{}
	got, err := NewPostgresStore(nil, nil).insertReleaseTx(t.Context(), batch48ReleaseTx(t, state), Release{AppealWindow: AppealWindow{OpensAt: &invalid}})
	if err == nil || state.query != "" || !reflect.DeepEqual(got, Release{}) {
		t.Fatalf("invalid appeal window reached SQL: release=%#v query=%q err=%v", got, state.query, err)
	}
}

func assertReleaseDraftDatabaseOwnership(t *testing.T, source string) {
	t.Helper()
	opens := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	closes := opens.Add(24 * time.Hour)
	created := opens.Add(-time.Hour)
	draft := Release{
		TenantID: "tenant", ExamID: "exam", Version: 8, Source: source, Reason: "reason", IdempotencyKey: "idempotency",
		VisibilityPolicy: VisibilityPolicy{ShowQuestionScores: true, ShowFeedback: true},
		AppealWindow:     AppealWindow{Enabled: true, OpensAt: &opens, ClosesAt: &closes, AllowedReasonCodes: []string{"score"}},
		CreatedBy:        "actor", ID: "untrusted-id", Status: StatusPublished, CreatedAt: closes,
	}
	if source != SourceInitial {
		draft.SourceReleaseID = "frozen-source"
	}
	visibility, err := json.Marshal(draft.VisibilityPolicy)
	if err != nil {
		t.Fatal(err)
	}
	window, err := json.Marshal(draft.AppealWindow)
	if err != nil {
		t.Fatal(err)
	}
	state := &batch48ReleaseSQL{row: []driver.Value{
		"persisted-release", "tenant", "exam", int64(8), source, "reason", StatusDraft, "idempotency",
		visibility, window, []byte(`{}`), draft.SourceReleaseID, "", "actor", created, "", nil,
	}}
	got, err := NewPostgresStore(nil, nil).insertReleaseTx(t.Context(), batch48ReleaseTx(t, state), draft)
	wantArgs := []driver.Value{"tenant", "exam", int64(8), source, "reason", "idempotency", string(visibility), string(window), draft.SourceReleaseID, "actor"}
	if err != nil || !reflect.DeepEqual(state.args, wantArgs) {
		t.Fatalf("release insert changed: args=%#v err=%v", state.args, err)
	}
	// 聚合对象可含数据库拥有的字段，但草稿写入不能采纳它们。
	if got.ID != "persisted-release" || got.Status != StatusDraft || !got.CreatedAt.Equal(created) || got.PublishedAt != nil || got.PublishedBy != "" {
		t.Fatalf("draft fields replaced database facts: %#v", got)
	}
	if got.SourceReleaseID != draft.SourceReleaseID || !reflect.DeepEqual(got.VisibilityPolicy, draft.VisibilityPolicy) || !reflect.DeepEqual(got.AppealWindow, draft.AppealWindow) {
		t.Fatalf("release policy or provenance changed: %#v", got)
	}
	if !strings.Contains(state.query, "NULLIF($9, '')::uuid") || !strings.Contains(state.query, "RETURNING ") {
		t.Fatalf("unexpected draft SQL: %s", state.query)
	}
}

package goldpaper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

type batch48GoldSQL struct {
	query string
	args  []driver.Value
	err   error
}

type batch48GoldConnector struct{ state *batch48GoldSQL }

func (c batch48GoldConnector) Connect(context.Context) (driver.Conn, error) {
	return &batch48GoldConn{state: c.state}, nil
}
func (c batch48GoldConnector) Driver() driver.Driver { return c }
func (batch48GoldConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type batch48GoldConn struct{ state *batch48GoldSQL }

func (*batch48GoldConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*batch48GoldConn) Close() error                { return nil }
func (c *batch48GoldConn) Begin() (driver.Tx, error) { return c, nil }
func (*batch48GoldConn) Commit() error               { return nil }
func (*batch48GoldConn) Rollback() error             { return nil }
func (c *batch48GoldConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.query = query
	for _, arg := range args {
		c.state.args = append(c.state.args, arg.Value)
	}
	if c.state.err != nil {
		return nil, c.state.err
	}
	return &batch48GoldRows{}, nil
}

type batch48GoldRows struct{ read bool }

func (*batch48GoldRows) Columns() []string { return []string{"id"} }
func (*batch48GoldRows) Close() error      { return nil }
func (r *batch48GoldRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = "persisted-version"
	return nil
}

func batch48GoldTx(t *testing.T, state *batch48GoldSQL) *sql.Tx {
	t.Helper()
	db := sql.OpenDB(batch48GoldConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return tx
}

func TestBatch48GoldVersionKeepsSourceSnapshotAndSQLArgumentOrder(t *testing.T) {
	source := Source{SnapshotID: "frozen-snapshot", MaxScore: 10, RubricSnapshot: map[string]any{"criterion": "reasoning"}}
	input := CreateVersionInput{
		ReferenceScore: 8.5, Explanation: "nomination explanation", TraitScores: map[string]any{"reasoning": 4},
		ErrorTags: []string{"omission"}, SourceGradeIDs: []string{"human-grade", "ai-grade"},
	}
	state := &batch48GoldSQL{}
	version := versionForInsert(source, "nominator", 4, input)
	got, err := insertVersion(t.Context(), batch48GoldTx(t, state), "tenant", "gold", version)
	want := []driver.Value{
		"tenant", "gold", int64(4), "frozen-snapshot", 8.5, float64(10), []byte(`{"criterion":"reasoning"}`),
		"nomination explanation", []byte(`{"reasoning":4}`), []byte(`["omission"]`), []byte(`["human-grade","ai-grade"]`), "nominator",
	}
	if err != nil || got != "persisted-version" || !reflect.DeepEqual(state.args, want) {
		t.Fatalf("version persistence changed: id=%q args=%#v err=%v", got, state.args, err)
	}
	if !strings.Contains(state.query, "INSERT INTO grading_gold_paper_version") || !strings.Contains(state.query, "RETURNING id::text") {
		t.Fatalf("unexpected version SQL: %s", state.query)
	}
}

func TestBatch48GoldVersionMarshalFailureStopsBeforeSQL(t *testing.T) {
	// 快照或维度分包含无法编码的值时，不能写入半成品版本；错误仍为领域输入错误。
	for _, field := range []string{"rubric", "traits"} {
		t.Run(field, func(t *testing.T) {
			version := Version{RubricSnapshot: map[string]any{}, TraitScores: map[string]any{}}
			switch field {
			case "rubric":
				version.RubricSnapshot["unsupported"] = func() {}
			case "traits":
				version.TraitScores["unsupported"] = func() {}
			}
			state := &batch48GoldSQL{}
			id, err := insertVersion(t.Context(), batch48GoldTx(t, state), "tenant", "gold", version)
			if !errors.Is(err, ErrInvalidInput) || id != "" || state.query != "" {
				t.Fatalf("invalid version reached SQL: id=%q query=%q err=%v", id, state.query, err)
			}
		})
	}
}

func TestBatch48GoldVersionKeepsNilJSONValues(t *testing.T) {
	state := &batch48GoldSQL{}
	if _, err := insertVersion(t.Context(), batch48GoldTx(t, state), "tenant", "gold", Version{}); err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{6, 8, 9, 10} {
		if string(state.args[index].([]byte)) != "null" {
			t.Fatalf("nil JSON value changed at argument %d: %#v", index, state.args)
		}
	}
}

func TestBatch48GoldVersionReturnsDatabaseFailure(t *testing.T) {
	dbErr := errors.New("version insert failed")
	state := &batch48GoldSQL{err: dbErr}
	id, err := insertVersion(t.Context(), batch48GoldTx(t, state), "tenant", "gold", Version{})
	if !errors.Is(err, dbErr) || id != "" {
		t.Fatalf("database failure changed: id=%q err=%v", id, err)
	}
}

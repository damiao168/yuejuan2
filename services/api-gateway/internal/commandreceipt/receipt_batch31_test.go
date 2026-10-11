package commandreceipt

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestBatch31ReceiptIdentityPreservesOriginalInputAndStoredProtocol(t *testing.T) {
	state := &batch31ReceiptDriverState{}
	db := sql.OpenDB(batch31ReceiptConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	identity := Identity{TenantID: "tenant", ActorID: "actor", Operation: "review.submit", TargetID: "task"}
	ctx := WithInput(WithID(t.Context(), "batch31-command"), map[string]any{"answer": "frozen"})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var replay map[string]any
	if found, err := Load(ctx, tx, identity, map[string]any{"answer": "enriched"}, &replay); err != nil || found {
		t.Fatalf("new command was already present: found=%t err=%v", found, err)
	}
	if err := Save(ctx, tx, identity, map[string]any{"answer": "enriched"}, map[string]any{"score": 0}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// 固定协议夹具代表旧回执的指纹形状；聚合身份后不能把结构字段或补全后的输入混入指纹。
	wire := []byte(`{"Operation":"review.submit","Target":"task","Input":{"answer":"frozen"}}`)
	sum := sha256.Sum256(wire)
	if state.hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("stored fingerprint changed: %s", state.hash)
	}
	if !reflect.DeepEqual(state.insert[:5], []any{"tenant", "actor", "batch31-command", "review.submit", "task"}) {
		t.Fatalf("receipt identity bindings changed: %v", state.insert)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if found, err := Load(ctx, tx, identity, map[string]any{"answer": "different enrichment"}, &replay); err != nil || !found || replay["score"] != float64(0) {
		t.Fatalf("original input no longer replays zero score: found=%t result=%v err=%v", found, replay, err)
	}
	if !reflect.DeepEqual(state.query, []any{"tenant", "actor", "batch31-command"}) || state.lock != "tenant:actor:batch31-command:business-command" {
		t.Fatalf("receipt scope or lock changed: query=%v lock=%s", state.query, state.lock)
	}
	changed := WithInput(WithID(t.Context(), "batch31-command"), map[string]any{"answer": "changed"})
	if found, err := Load(changed, tx, identity, nil, &replay); found || !errors.Is(err, ErrConflict) {
		t.Fatalf("changed raw input was accepted: found=%t err=%v", found, err)
	}
}

// 此驱动只提供固定回执行并记录 SQL 绑定，不模拟 PostgreSQL 的事务原子性或 advisory lock 竞争。
type batch31ReceiptDriverState struct {
	hash   string
	raw    []byte
	insert []any
	query  []any
	lock   string
}

type batch31ReceiptConnector struct{ state *batch31ReceiptDriverState }

func (c batch31ReceiptConnector) Connect(context.Context) (driver.Conn, error) {
	return &batch31ReceiptConn{state: c.state}, nil
}
func (batch31ReceiptConnector) Driver() driver.Driver { return batch31ReceiptDriver{} }

type batch31ReceiptDriver struct{}

func (batch31ReceiptDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("connector required")
}

type batch31ReceiptConn struct{ state *batch31ReceiptDriverState }

func (*batch31ReceiptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is outside this fixture")
}
func (*batch31ReceiptConn) Close() error              { return nil }
func (*batch31ReceiptConn) Begin() (driver.Tx, error) { return batch31ReceiptTx{}, nil }

func (c *batch31ReceiptConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if strings.HasPrefix(query, "SELECT pg_advisory_xact_lock") {
		c.state.lock = args[0].Value.(string)
	} else if strings.HasPrefix(query, "INSERT INTO business_command_receipt") {
		c.state.insert = batch31ReceiptValues(args)
		c.state.hash = args[5].Value.(string)
		c.state.raw = append([]byte(nil), args[6].Value.([]byte)...)
	} else {
		return nil, errors.New("unexpected fixture statement")
	}
	return driver.RowsAffected(1), nil
}

func (c *batch31ReceiptConn) QueryContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Rows, error) {
	c.state.query = batch31ReceiptValues(args)
	return &batch31ReceiptRows{hash: c.state.hash, raw: c.state.raw}, nil
}

func batch31ReceiptValues(args []driver.NamedValue) []any {
	values := make([]any, len(args))
	for index, arg := range args {
		values[index] = arg.Value
	}
	return values
}

type batch31ReceiptTx struct{}

func (batch31ReceiptTx) Commit() error   { return nil }
func (batch31ReceiptTx) Rollback() error { return nil }

type batch31ReceiptRows struct {
	hash string
	raw  []byte
	done bool
}

func (*batch31ReceiptRows) Columns() []string { return []string{"request_hash", "result"} }
func (*batch31ReceiptRows) Close() error      { return nil }
func (r *batch31ReceiptRows) Next(values []driver.Value) error {
	if r.done || r.hash == "" {
		return io.EOF
	}
	r.done = true
	values[0], values[1] = r.hash, r.raw
	return nil
}

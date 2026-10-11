package regrade

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
	"time"
)

type batch48RegradeStep struct {
	kind, fragment string
	args           []driver.Value
	row            []driver.Value
	err            error
}

// 脚本验证 SQL 调用顺序和事务退出路径，不模拟 PostgreSQL 的行锁竞争。
type batch48RegradeSQL struct {
	steps  []batch48RegradeStep
	next   int
	events []string
}

func (s *batch48RegradeSQL) take(kind, query string, args []driver.NamedValue) (batch48RegradeStep, error) {
	s.events = append(s.events, kind)
	if s.next >= len(s.steps) {
		return batch48RegradeStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
	}
	step := s.steps[s.next]
	s.next++
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if step.kind != kind || !strings.Contains(query, step.fragment) || !reflect.DeepEqual(values, step.args) {
		return step, fmt.Errorf("step %d: got %s %q %#v, want %s %q %#v", s.next, kind, query, values, step.kind, step.fragment, step.args)
	}
	return step, step.err
}

type batch48RegradeConnector struct{ state *batch48RegradeSQL }

func (c batch48RegradeConnector) Connect(context.Context) (driver.Conn, error) {
	return &batch48RegradeConn{state: c.state}, nil
}
func (c batch48RegradeConnector) Driver() driver.Driver { return c }
func (batch48RegradeConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type batch48RegradeConn struct{ state *batch48RegradeSQL }

func (*batch48RegradeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*batch48RegradeConn) Close() error { return nil }
func (c *batch48RegradeConn) Begin() (driver.Tx, error) {
	c.state.events = append(c.state.events, "begin")
	return c, nil
}
func (c *batch48RegradeConn) Commit() error {
	c.state.events = append(c.state.events, "commit")
	return nil
}
func (c *batch48RegradeConn) Rollback() error {
	c.state.events = append(c.state.events, "rollback")
	return nil
}
func (c *batch48RegradeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.state.take("query", query, args)
	if err != nil {
		return nil, err
	}
	return &batch48RegradeRows{row: step.row}, nil
}
func (c *batch48RegradeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.state.take("exec", query, args)
	return driver.RowsAffected(1), err
}

type batch48RegradeRows struct {
	row  []driver.Value
	read bool
}

func (r *batch48RegradeRows) Columns() []string { return make([]string, len(r.row)) }
func (*batch48RegradeRows) Close() error        { return nil }
func (r *batch48RegradeRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	copy(dest, r.row)
	return nil
}

func batch48RegradeDB(t *testing.T, state *batch48RegradeSQL) *sql.DB {
	t.Helper()
	db := sql.OpenDB(batch48RegradeConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func batch48RegradeJobRow(status string) []driver.Value {
	now := time.Date(2026, 10, 10, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	return []driver.Value{
		"job", "tenant", "exam", "question", "source-release", "reason-code", "reason-text", "human_regrade", []byte(`{}`),
		"rubric", "policy", float64(0), "idempotency", status, int64(2), "creator", "", nil, "", nil, now, now,
	}
}

func TestBatch48RegradeTransitionsKeepRulesAndAtomicEvent(t *testing.T) {
	for _, operation := range []struct {
		name, expected, target, eventType, updateFragment string
		updateArgs                                        []driver.Value
		run                                               func(*PostgresStore, context.Context) (Job, error)
	}{
		{"approve", StatusAwaitingApproval, StatusApproved, "approved", "status='approved'", []driver.Value{"tenant", "job", "actor"}, func(s *PostgresStore, ctx context.Context) (Job, error) {
			return s.Approve(ctx, "tenant", "job", "actor")
		}},
		{"start", StatusApproved, StatusRunning, "started", "status='running'", []driver.Value{"tenant", "job"}, func(s *PostgresStore, ctx context.Context) (Job, error) {
			return s.Start(ctx, "tenant", "job", "actor")
		}},
		{"resume", StatusPaused, StatusRunning, "resumed", "status='running'", []driver.Value{"tenant", "job"}, func(s *PostgresStore, ctx context.Context) (Job, error) {
			return s.Resume(ctx, "tenant", "job", "actor")
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			state := &batch48RegradeSQL{steps: []batch48RegradeStep{
				{kind: "query", fragment: "FOR UPDATE", args: []driver.Value{"tenant", "job"}, row: batch48RegradeJobRow(operation.expected)},
				{kind: "query", fragment: operation.updateFragment, args: operation.updateArgs, row: batch48RegradeJobRow(operation.target)},
				{kind: "exec", fragment: "INSERT INTO regrade_event", args: []driver.Value{"tenant", "job", "", operation.eventType, "actor", `{}`}},
			}}
			job, err := operation.run(NewPostgresStore(batch48RegradeDB(t, state)), t.Context())
			wantEvents := []string{"begin", "query", "query", "exec", "commit"}
			if err != nil || job.Status != operation.target || job.CreatedAt.Location() != time.UTC || state.next != len(state.steps) || !reflect.DeepEqual(state.events, wantEvents) {
				t.Fatalf("transition changed: job=%#v events=%#v step=%d err=%v", job, state.events, state.next, err)
			}
		})
	}
}

func TestBatch48RegradeTransitionRetryAndFailuresDoNotCommit(t *testing.T) {
	dbErr := errors.New("database write failed")
	for _, scenario := range []regradeTransitionFailureCase{
		{name: "target retry", current: StatusApproved, wantEvents: []string{"begin", "query", "rollback"}},
		{name: "wrong state", current: StatusPaused, wantErr: ErrStateConflict, wantEvents: []string{"begin", "query", "rollback"}},
		{name: "update fails", current: StatusAwaitingApproval, updateErr: dbErr, wantErr: dbErr, wantEvents: []string{"begin", "query", "query", "rollback"}},
		{name: "event fails", current: StatusAwaitingApproval, eventErr: dbErr, wantErr: dbErr, wantEvents: []string{"begin", "query", "query", "exec", "rollback"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assertRegradeTransitionRetryAndFailure(t, scenario)
		})
	}
}

func TestBatch48RegradeItemEventKeepsIdentityPayloadAndMarshalFailure(t *testing.T) {
	for _, invalidPayload := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid=%t", invalidPayload), func(t *testing.T) {
			state := &batch48RegradeSQL{}
			event := Event{JobID: "job", ItemID: "item", Type: "item_reviewed", ActorID: "reviewer", Payload: map[string]any{"decision": "accept"}}
			if invalidPayload {
				event.Payload["unsupported"] = func() {}
			} else {
				state.steps = []batch48RegradeStep{{kind: "exec", fragment: "NULLIF($3,'')::uuid", args: []driver.Value{"tenant", "job", "item", "item_reviewed", "reviewer", `{"decision":"accept"}`}}}
			}
			tx, err := batch48RegradeDB(t, state).BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			err = addEventTx(t.Context(), tx, "tenant", event)
			_ = tx.Rollback()
			if (err != nil) != invalidPayload || state.next != len(state.steps) {
				t.Fatalf("event persistence changed: steps=%d err=%v", state.next, err)
			}
			wantEvents := []string{"begin", "exec", "rollback"}
			if invalidPayload {
				wantEvents = []string{"begin", "rollback"}
			}
			if !reflect.DeepEqual(state.events, wantEvents) {
				t.Fatalf("event failure reached SQL: %#v", state.events)
			}
		})
	}
}

type regradeTransitionFailureCase struct {
	name, current string
	updateErr     error
	eventErr      error
	wantErr       error
	wantEvents    []string
}

func assertRegradeTransitionRetryAndFailure(t *testing.T, scenario regradeTransitionFailureCase) {
	t.Helper()
	state := &batch48RegradeSQL{steps: []batch48RegradeStep{{kind: "query", fragment: "FOR UPDATE", args: []driver.Value{"tenant", "job"}, row: batch48RegradeJobRow(scenario.current)}}}
	if scenario.current == StatusAwaitingApproval {
		state.steps = append(state.steps, batch48RegradeStep{kind: "query", fragment: "status='approved'", args: []driver.Value{"tenant", "job", "actor"}, row: batch48RegradeJobRow(StatusApproved), err: scenario.updateErr})
		if scenario.updateErr == nil {
			state.steps = append(state.steps, batch48RegradeStep{kind: "exec", fragment: "INSERT INTO regrade_event", args: []driver.Value{"tenant", "job", "", "approved", "actor", `{}`}, err: scenario.eventErr})
		}
	}
	job, err := NewPostgresStore(batch48RegradeDB(t, state)).Approve(t.Context(), "tenant", "job", "actor")
	if !errors.Is(err, scenario.wantErr) || state.next != len(state.steps) || !reflect.DeepEqual(state.events, scenario.wantEvents) {
		t.Fatalf("failure path changed: job=%#v events=%#v step=%d err=%v", job, state.events, state.next, err)
	}
	if scenario.wantErr != nil && !reflect.DeepEqual(job, Job{}) {
		t.Fatalf("failed transition returned a partial job: %#v", job)
	}
	if scenario.wantErr == nil && job.Status != StatusApproved {
		t.Fatalf("retry did not return current job: %#v", job)
	}
}

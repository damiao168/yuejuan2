package workerruntime

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

type batch48RuntimeStep struct {
	kind, fragment string
	args           []driver.Value
	columns        int
	rows           [][]driver.Value
	err            error
}

// 脚本只检查数据库时间的传递、SQL 参数与事务退出，不模拟真实 worker 并发。
type batch48RuntimeSQL struct {
	steps  []batch48RuntimeStep
	next   int
	events []string
}

func (s *batch48RuntimeSQL) take(kind, query string, args []driver.NamedValue) (batch48RuntimeStep, error) {
	s.events = append(s.events, kind)
	if s.next >= len(s.steps) {
		return batch48RuntimeStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
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

type batch48RuntimeConnector struct{ state *batch48RuntimeSQL }

func (c batch48RuntimeConnector) Connect(context.Context) (driver.Conn, error) {
	return &batch48RuntimeConn{state: c.state}, nil
}
func (c batch48RuntimeConnector) Driver() driver.Driver { return c }
func (batch48RuntimeConnector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type batch48RuntimeConn struct{ state *batch48RuntimeSQL }

func (*batch48RuntimeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*batch48RuntimeConn) Close() error { return nil }
func (c *batch48RuntimeConn) Begin() (driver.Tx, error) {
	c.state.events = append(c.state.events, "begin")
	return c, nil
}
func (c *batch48RuntimeConn) Commit() error {
	c.state.events = append(c.state.events, "commit")
	return nil
}
func (c *batch48RuntimeConn) Rollback() error {
	c.state.events = append(c.state.events, "rollback")
	return nil
}
func (c *batch48RuntimeConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.state.take("query", query, args)
	if err != nil {
		return nil, err
	}
	return &batch48RuntimeRows{columns: step.columns, rows: step.rows}, nil
}
func (c *batch48RuntimeConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.state.take("exec", query, args)
	return driver.RowsAffected(1), err
}

type batch48RuntimeRows struct {
	columns int
	rows    [][]driver.Value
	next    int
}

func (r *batch48RuntimeRows) Columns() []string { return make([]string, r.columns) }
func (*batch48RuntimeRows) Close() error        { return nil }
func (r *batch48RuntimeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}

func batch48RuntimeDB(t *testing.T, state *batch48RuntimeSQL) *sql.DB {
	t.Helper()
	db := sql.OpenDB(batch48RuntimeConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestBatch48RuntimeClaimKeepsPollingIdentityAndDatabaseClock(t *testing.T) {
	clock := time.Date(2026, 10, 10, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	dbNow := clock.UTC()
	for _, scenario := range []workerPollingIdentityCase{
		{name: "school polling", tenant: "tenant"},
		{name: "platform polling", tenant: "platform", across: true},
		{name: "process executor", across: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			assertWorkerPollingDatabaseClock(t, scenario, clock, dbNow)
		})
	}
}

func batch48RuntimeTaskRow(status string, now, expires time.Time) []driver.Value {
	return []driver.Value{
		"task", "tenant", "task-type", "persisted-queue", "source-type", "source", status, int64(1), []byte(`{}`), "payload-v1", []byte(`{}`), []byte(`{}`),
		"", "", "idempotency", "", int64(3), int64(1), int64(10), nil, "lease", expires, "worker:instance", "worker", "instance",
		nil, nil, nil, int64(0), "", []byte(`{}`), "creator", now, now, int64(2),
	}
}

func TestBatch48RuntimeHeartbeatKeepsTaskQueueProgressAndAtomicFailure(t *testing.T) {
	dbNow := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	for _, heartbeatErr := range []error{nil, errors.New("worker heartbeat failed")} {
		t.Run(fmt.Sprintf("failure=%t", heartbeatErr != nil), func(t *testing.T) {
			assertWorkerHeartbeatAtomicFailure(t, heartbeatErr, dbNow)
		})
	}
}

type workerPollingIdentityCase struct {
	name, tenant string
	across       bool
}

func assertWorkerPollingDatabaseClock(t *testing.T, scenario workerPollingIdentityCase, clock time.Time, dbNow time.Time) {
	t.Helper()
	state := &batch48RuntimeSQL{steps: []batch48RuntimeStep{{kind: "query", fragment: "SELECT transaction_timestamp()", args: []driver.Value{}, columns: 1, rows: [][]driver.Value{{clock}}}}}
	var tenantScope driver.Value = scenario.tenant
	if scenario.tenant != "" {
		state.steps = append(state.steps, batch48RuntimeStep{kind: "exec", fragment: "INSERT INTO agent_worker_heartbeat", args: []driver.Value{scenario.tenant, "worker", "instance", "queue", dbNow, []byte(`{"state":"polling"}`)}})
	} else {
		tenantScope = nil
	}
	queryArgs := []driver.Value{tenantScope, "queue", int64(2), dbNow, scenario.across}
	state.steps = append(state.steps,
		batch48RuntimeStep{kind: "query", fragment: "attempt_count >= max_attempts", args: queryArgs, columns: 3},
		batch48RuntimeStep{kind: "query", fragment: "attempt_count < max_attempts", args: queryArgs, columns: 3},
	)
	input := ClaimInput{QueueName: "queue", WorkerService: "worker", WorkerInstanceID: "instance", Limit: 2, LeaseSeconds: 60}
	store := NewPostgresStore(batch48RuntimeDB(t, state))
	var tasks []Task
	var err error
	if scenario.across {
		tasks, err = store.ClaimAcrossTenants(t.Context(), scenario.tenant, input)
	} else {
		tasks, err = store.Claim(t.Context(), scenario.tenant, input)
	}
	wantEvents := []string{"begin", "query", "exec", "query", "query", "commit"}
	if scenario.tenant == "" {
		wantEvents = []string{"begin", "query", "query", "query", "commit"}
	}
	if err != nil || tasks == nil || len(tasks) != 0 || state.next != len(state.steps) || !reflect.DeepEqual(state.events, wantEvents) {
		t.Fatalf("idle claim changed: tasks=%#v step=%d events=%#v err=%v", tasks, state.next, state.events, err)
	}
}

func assertWorkerHeartbeatAtomicFailure(t *testing.T, heartbeatErr error, dbNow time.Time) {
	t.Helper()
	expires := dbNow.Add(time.Minute)
	state := &batch48RuntimeSQL{steps: []batch48RuntimeStep{
		{kind: "query", fragment: "SELECT transaction_timestamp()", args: []driver.Value{}, columns: 1, rows: [][]driver.Value{{dbNow}}},
		{kind: "query", fragment: "FOR UPDATE", args: []driver.Value{"tenant", "task"}, columns: 35, rows: [][]driver.Value{batch48RuntimeTaskRow(StatusLeased, dbNow, dbNow.Add(time.Second))}},
		{kind: "query", fragment: "started_at = COALESCE(started_at, $3)", args: []driver.Value{"tenant", "task", dbNow, expires, []byte(`{"processed":3}`)}, columns: 35, rows: [][]driver.Value{batch48RuntimeTaskRow(StatusRunning, dbNow, expires)}},
		{kind: "exec", fragment: "UPDATE agent_worker_task_attempt", args: []driver.Value{"tenant", "task", int64(1), dbNow}},
		{kind: "exec", fragment: "INSERT INTO agent_worker_heartbeat", args: []driver.Value{"tenant", "worker", "instance", "persisted-queue", dbNow, []byte(`{"processed":3,"state":"running"}`)}, err: heartbeatErr},
	}}
	progress := map[string]any{"processed": 3}
	got, err := NewPostgresStore(batch48RuntimeDB(t, state)).Heartbeat(t.Context(), "tenant", "task", HeartbeatInput{
		LeaseToken: "lease", WorkerService: "worker", WorkerInstanceID: "instance", Progress: progress, LeaseSeconds: 60,
	})
	wantEvents := []string{"begin", "query", "query", "query", "exec", "exec", "commit"}
	if heartbeatErr != nil {
		wantEvents[len(wantEvents)-1] = "rollback"
	}
	if !errors.Is(err, heartbeatErr) || state.next != len(state.steps) || !reflect.DeepEqual(state.events, wantEvents) {
		t.Fatalf("heartbeat transaction changed: task=%#v step=%d events=%#v err=%v", got, state.next, state.events, err)
	}
	if heartbeatErr == nil && (got.Status != StatusRunning || got.LeaseExpiresAt == nil || !got.LeaseExpiresAt.Equal(expires)) {
		t.Fatalf("heartbeat result changed: %#v", got)
	}
	if heartbeatErr != nil && !reflect.DeepEqual(got, Task{}) {
		t.Fatalf("failed heartbeat returned partial task: %#v", got)
	}
	if !reflect.DeepEqual(progress, map[string]any{"processed": 3}) {
		t.Fatalf("heartbeat metadata mutated input progress: %#v", progress)
	}
}

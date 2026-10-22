package files

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type reconciliationBatch21Step struct {
	kind, fragment string
	rows           [][]driver.Value
	columns        int
	affected       int64
	err            error
	args           []any
	check          func(context.Context)
}

// 同一脚本记录 SQL 和对象操作，只验证调用顺序，不模拟真实数据库或对象存储并发。
type reconciliationBatch21Script struct {
	t     *testing.T
	steps []reconciliationBatch21Step
	next  int
}

func (s *reconciliationBatch21Script) take(ctx context.Context, kind, query string, args []driver.NamedValue) (reconciliationBatch21Step, error) {
	if s.next == len(s.steps) {
		s.t.Errorf("unexpected %s: %s", kind, query)
		return reconciliationBatch21Step{}, errors.New("unexpected operation")
	}
	step := s.steps[s.next]
	s.next++
	if step.kind != kind || !strings.Contains(query, step.fragment) {
		s.t.Errorf("step %d: %s %q, want %s containing %q", s.next, kind, query, step.kind, step.fragment)
		return step, errors.New("operation order mismatch")
	}
	if step.args != nil {
		values := make([]any, len(args))
		for i, arg := range args {
			values[i] = arg.Value
		}
		if !reflect.DeepEqual(values, step.args) {
			s.t.Errorf("step %d arguments: %#v, want %#v", s.next, values, step.args)
		}
	}
	if step.check != nil {
		step.check(ctx)
	}
	return step, step.err
}

type reconciliationBatch21Connector struct{ script *reconciliationBatch21Script }

func (c reconciliationBatch21Connector) Connect(context.Context) (driver.Conn, error) {
	return &reconciliationBatch21Conn{c.script}, nil
}
func (c reconciliationBatch21Connector) Driver() driver.Driver { return c }
func (reconciliationBatch21Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use test connector")
}

type reconciliationBatch21Conn struct{ script *reconciliationBatch21Script }

func (*reconciliationBatch21Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*reconciliationBatch21Conn) Close() error { return nil }
func (*reconciliationBatch21Conn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (c *reconciliationBatch21Conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take(ctx, "query", query, args)
	if err != nil {
		return nil, err
	}
	if step.fragment == "INSERT INTO file_reconciliation_finding" {
		step.columns = 10
		step.rows = [][]driver.Value{{"finding", args[0].Value, args[1].Value, args[2].Value, args[3].Value,
			args[4].Value, args[5].Value, args[6].Value, args[7].Value, time.Unix(1, 0)}}
	}
	return &reconciliationBatch21Rows{step: step}, nil
}
func (c *reconciliationBatch21Conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step, err := c.script.take(ctx, "exec", query, args)
	return driver.RowsAffected(step.affected), err
}

type reconciliationBatch21Rows struct {
	step reconciliationBatch21Step
	next int
}

func (r *reconciliationBatch21Rows) Columns() []string { return make([]string, r.step.columns) }
func (*reconciliationBatch21Rows) Close() error        { return nil }
func (r *reconciliationBatch21Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

type reconciliationBatch21Objects struct{ script *reconciliationBatch21Script }

func (o reconciliationBatch21Objects) Put(context.Context, string, string, io.Reader, int64, string) error {
	return errors.New("unexpected put")
}
func (o reconciliationBatch21Objects) Stat(ctx context.Context, _, _ string) (ObjectInfo, error) {
	_, err := o.script.take(ctx, "stat", "", nil)
	return ObjectInfo{SizeBytes: 1}, err
}
func (o reconciliationBatch21Objects) Get(ctx context.Context, _, _ string) (io.ReadCloser, error) {
	_, err := o.script.take(ctx, "get", "", nil)
	return &reconciliationBatch21Body{Reader: strings.NewReader("x"), script: o.script}, err
}
func (o reconciliationBatch21Objects) Remove(ctx context.Context, _, _ string) error {
	_, err := o.script.take(ctx, "remove", "", nil)
	return err
}
func (o reconciliationBatch21Objects) List(ctx context.Context, _, _ string, _ int) ([]ObjectInfo, error) {
	_, err := o.script.take(ctx, "list", "", nil)
	return []ObjectInfo{{Bucket: "bucket", Key: "tenant/orphan", SizeBytes: 1}}, err
}

type reconciliationBatch21Body struct {
	*strings.Reader
	script *reconciliationBatch21Script
}

func (b *reconciliationBatch21Body) Close() error {
	_, err := b.script.take(context.Background(), "close", "", nil)
	return err
}

func newReconciliationBatch21(t *testing.T, steps []reconciliationBatch21Step) *Reconciler {
	t.Helper()
	script := &reconciliationBatch21Script{t: t, steps: steps}
	database := sql.OpenDB(reconciliationBatch21Connector{script})
	t.Cleanup(func() {
		_ = database.Close()
		if script.next != len(steps) {
			t.Errorf("only consumed %d/%d operations", script.next, len(steps))
		}
	})
	return NewReconciler(database, reconciliationBatch21Objects{script})
}

func TestReconciliationInspectBranchOrder(t *testing.T) {
	finding := reconciliationBatch21Step{kind: "query", fragment: "INSERT INTO file_reconciliation_finding"}
	setState := reconciliationBatch21Step{kind: "exec", fragment: "SET lifecycle_status=$3", affected: 1}
	storageErr, updateErr := errors.New("remove failed"), errors.New("update failed")
	matched := func(tail ...reconciliationBatch21Step) []reconciliationBatch21Step {
		return append([]reconciliationBatch21Step{{kind: "stat"}, {kind: "get"}, {kind: "close"}}, tail...)
	}
	remove := reconciliationBatch21Step{kind: "remove"}
	unpinned := reconciliationBatch21Step{kind: "query", fragment: "FROM question_reference_manifest_item", columns: 1, rows: [][]driver.Value{{false}}}
	pinned := reconciliationBatch21Step{kind: "query", fragment: "FROM question_reference_manifest_item", columns: 1, rows: [][]driver.Value{{true}}}
	deleteConflict := reconciliationBatch21Step{kind: "exec", fragment: "SET lifecycle_status='deleted'"}
	removeFailed := reconciliationBatch21Step{kind: "remove", err: storageErr}
	updateFailed := reconciliationBatch21Step{kind: "exec", fragment: "SET lifecycle_status=$3", err: updateErr}
	cases := []fileReconciliationBranchCase{
		{"missing callback", LifecycleActive, true, []reconciliationBatch21Step{finding, {kind: "stat", err: ErrObjectNotFound}, setState, finding}, []string{"tenant_prefix_mismatch", "object_missing"}, true, nil},
		{"verified callback", LifecyclePendingUpload, true, append([]reconciliationBatch21Step{finding}, matched(setState, finding)...), []string{"tenant_prefix_mismatch", "recoverable_pending_upload"}, true, nil},
		{"delete CAS conflict", LifecyclePendingDelete, false, matched(unpinned, remove, deleteConflict, finding), []string{"delete_cleanup_pending"}, false, nil},
		{"remove failure compensated", LifecycleDeleteFailed, false, matched(unpinned, removeFailed, setState, finding), []string{"delete_cleanup_pending"}, false, nil},
		{"remove compensation fails", LifecycleDeleteFailed, false, matched(unpinned, removeFailed, updateFailed), nil, false, updateErr},
		{"reference pin prevents physical removal", LifecyclePendingDelete, false, matched(pinned, finding), []string{"reference_material_pinned"}, false, nil},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertFileReconciliationBranchOrder(t, test)
		})
	}
}

func TestReconciliationRunPagesBeforeOrphans(t *testing.T) {
	now := time.Unix(1, 0)
	assetRow := []driver.Value{"asset", "tenant", "", "", "", "owner", "", "file", "text/plain", int64(1), "", "bucket", "tenant/tenant/file", "private", "", LifecycleActive, int64(7), "", int64(0), nil, nil, false, nil, now}
	r := newReconciliationBatch21(t, []reconciliationBatch21Step{
		{kind: "query", fragment: "INSERT INTO file_reconciliation_run", columns: 4, rows: [][]driver.Value{{"run", "report", "running", now}}},
		{kind: "query", fragment: "ORDER BY id LIMIT $2", columns: 24, rows: [][]driver.Value{assetRow}, args: []any{"", int64(1)}},
		{kind: "stat", err: ErrObjectNotFound},
		{kind: "query", fragment: "INSERT INTO file_reconciliation_finding"},
		{kind: "query", fragment: "ORDER BY id LIMIT $2", columns: 24, args: []any{"asset", int64(1)}},
		{kind: "list"},
		{kind: "query", fragment: "SELECT EXISTS", columns: 1, rows: [][]driver.Value{{false}}, args: []any{"bucket", "tenant/orphan"}},
		{kind: "query", fragment: "INSERT INTO file_reconciliation_finding", args: []any{"run", "", "", "bucket", "tenant/orphan", "orphan_object", []byte(`{"size_bytes":1}`), false}},
		{kind: "query", fragment: "UPDATE file_reconciliation_run", columns: 1, rows: [][]driver.Value{{now}}, args: []any{"run", "completed", int64(1), int64(1), int64(2), int64(0), ""}},
	})
	run, err := r.Run(context.Background(), ReconciliationOptions{Bucket: "bucket", BatchSize: 1})
	if err != nil || run.ScannedAssets != 1 || run.ScannedObjects != 1 || run.FindingCount != 2 || run.RepairedCount != 0 {
		t.Fatalf("run=%#v, error=%v", run, err)
	}
}

func TestReconciliationFinishKeepsScopeAndFirstError(t *testing.T) {
	type scopeKey struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), scopeKey{}, "maintenance"))
	defer cancel()
	firstErr, finishErr := errors.New("scan failed"), errors.New("finish failed")
	r := newReconciliationBatch21(t, []reconciliationBatch21Step{
		{kind: "query", fragment: "INSERT INTO file_reconciliation_run", columns: 4, rows: [][]driver.Value{{"run", "report", "running", time.Unix(1, 0)}}},
		{kind: "query", fragment: "FROM file_asset", err: firstErr, check: func(context.Context) { cancel() }},
		{kind: "query", fragment: "UPDATE file_reconciliation_run", err: finishErr, check: func(ctx context.Context) {
			if ctx.Err() != nil || ctx.Value(scopeKey{}) != "maintenance" {
				t.Errorf("finish lost context scope: %v", ctx.Err())
			}
		}},
	})
	run, err := r.Run(ctx, ReconciliationOptions{Bucket: "bucket"})
	if !errors.Is(err, firstErr) || run.Status != "failed" || run.ErrorDetail != firstErr.Error() {
		t.Fatalf("run=%#v, error=%v", run, err)
	}
	if run.CompletedAt == nil || !run.CompletedAt.IsZero() {
		t.Fatalf("unexpected completed_at: %v", run.CompletedAt)
	}
}

type fileReconciliationBranchCase struct {
	name, lifecycle string
	prefixMismatch  bool
	steps           []reconciliationBatch21Step
	codes           []string
	repaired        bool
	err             error
}

func assertFileReconciliationBranchOrder(t *testing.T, test fileReconciliationBranchCase) {
	t.Helper()
	asset := FileAsset{ID: "asset", TenantID: "tenant", StorageBucket: "bucket", StorageKey: "tenant/tenant/file", SizeBytes: 1,
		HashSHA256: "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881", Lifecycle: test.lifecycle, Revision: 7}
	if test.prefixMismatch {
		asset.StorageKey = "tenant/other/file"
	}
	findings, err := newReconciliationBatch21(t, test.steps).inspectAsset(context.Background(), "run", asset, ReconciliationOptions{Repair: true, StaleAfter: time.Hour})
	if !errors.Is(err, test.err) || len(findings) != len(test.codes) {
		t.Fatalf("findings=%#v, error=%v; want codes=%v, error=%v", findings, err, test.codes, test.err)
	}
	for i, finding := range findings {
		identity := []string{finding.RunID, finding.TenantID, finding.FileAssetID, finding.Bucket, finding.Key}
		wantIdentity := []string{"run", asset.TenantID, asset.ID, asset.StorageBucket, asset.StorageKey}
		if !reflect.DeepEqual(identity, wantIdentity) || finding.Code != test.codes[i] || finding.Repaired != (test.repaired && i == len(findings)-1) {
			t.Errorf("unexpected finding: %#v", finding)
		}
	}
}

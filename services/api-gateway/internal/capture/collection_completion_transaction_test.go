package capture

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type collectionSQLStep struct {
	kind string
	printLedgerQuery
}

type collectionSQLProbe struct {
	steps       []collectionSQLStep
	next        int
	opened      int
	readers     printLedgerConnector
	transaction driver.TxOptions
	protocolErr error
	connections []*collectionSQLConnection
}

func (p *collectionSQLProbe) Connect(context.Context) (driver.Conn, error) {
	c := &collectionSQLConnection{probe: p}
	p.connections = append(p.connections, c)
	return c, nil
}
func (*collectionSQLProbe) Driver() driver.Driver { return printLedgerDriver{} }

func (p *collectionSQLProbe) reject(format string, args ...any) error {
	if p.protocolErr == nil {
		p.protocolErr = fmt.Errorf(format, args...)
	}
	return p.protocolErr
}

func (p *collectionSQLProbe) take(kind, query string, args []driver.NamedValue) (printLedgerQuery, error) {
	if p.protocolErr != nil {
		return printLedgerQuery{}, p.protocolErr
	}
	if p.next >= len(p.steps) {
		return printLedgerQuery{}, p.reject("unexpected collection operation %s", kind)
	}
	step := p.steps[p.next]
	if kind != step.kind {
		return printLedgerQuery{}, p.reject("collection operation %s, want %s", kind, step.kind)
	}
	for _, fragment := range step.fragments {
		if !strings.Contains(query, fragment) {
			return printLedgerQuery{}, p.reject("collection query missing %q", fragment)
		}
	}
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if len(values) != len(step.args) || len(values) > 0 && !reflect.DeepEqual(values, step.args) {
		return printLedgerQuery{}, p.reject("collection args=%v want=%v", values, step.args)
	}
	p.next++
	return step.printLedgerQuery, step.queryErr
}

type collectionSQLConnection struct {
	probe    *collectionSQLProbe
	activeTx *collectionSQLTransaction
}

func (c *collectionSQLConnection) Prepare(string) (driver.Stmt, error) {
	return nil, c.probe.reject("unexpected collection prepared statement")
}
func (c *collectionSQLConnection) Close() error {
	if c.activeTx != nil {
		return c.probe.reject("collection connection closed with an active transaction")
	}
	return nil
}
func (c *collectionSQLConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *collectionSQLConnection) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if c.activeTx != nil {
		return nil, c.probe.reject("collection connection already has an active transaction")
	}
	if _, err := c.probe.take("begin", "", nil); err != nil {
		return nil, err
	}
	c.probe.transaction = options
	tx := &collectionSQLTransaction{connection: c}
	c.activeTx = tx
	return tx, nil
}
func (c *collectionSQLConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.activeTx == nil {
		return nil, c.probe.reject("collection query outside its connection transaction")
	}
	step, err := c.probe.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.probe.opened++
	return &printLedgerRows{step: step, connector: &c.probe.readers}, nil
}
func (c *collectionSQLConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.activeTx == nil {
		return nil, c.probe.reject("collection exec outside its connection transaction")
	}
	_, err := c.probe.take("exec", query, args)
	return driver.RowsAffected(1), err
}

type collectionSQLTransaction struct{ connection *collectionSQLConnection }

func (tx *collectionSQLTransaction) finish(kind string) error {
	if tx.connection.activeTx != tx {
		return tx.connection.probe.reject("collection transaction already ended")
	}
	_, err := tx.connection.probe.take(kind, "", nil)
	tx.connection.activeTx = nil
	return err
}
func (tx *collectionSQLTransaction) Commit() error   { return tx.finish("commit") }
func (tx *collectionSQLTransaction) Rollback() error { return tx.finish("rollback") }

func collectionProbeStore(t *testing.T, steps []collectionSQLStep) (*PostgresStore, *collectionSQLProbe) {
	t.Helper()
	probe := &collectionSQLProbe{steps: steps}
	db := sql.OpenDB(probe)
	t.Cleanup(func() {
		_ = db.Close()
		// defer Rollback 的返回值可能被业务代码忽略，协议违例必须独立留存到清理阶段。
		if probe.protocolErr != nil {
			t.Errorf("collection SQL protocol: %v", probe.protocolErr)
		}
		for _, c := range probe.connections {
			if c.activeTx != nil {
				t.Error("collection connection still has an active transaction")
			}
		}
		if probe.next != len(steps) || probe.readers.closed != probe.opened {
			t.Errorf("collection operations=%d/%d, row readers closed=%d/%d", probe.next, len(steps), probe.readers.closed, probe.opened)
		}
	})
	return NewPostgresStore(db), probe
}

func TestCollectionSQLProbeRejectsIgnoredProtocolErrorsAndTransactionsOnOtherConnections(t *testing.T) {
	t.Run("commit cannot satisfy expected rollback", func(t *testing.T) {
		probe := &collectionSQLProbe{steps: []collectionSQLStep{{kind: "begin"}, {kind: "rollback"}}}
		db := sql.OpenDB(probe)
		defer db.Close()
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		err = tx.Commit()
		// 即使调用方忽略事务终止错误，清理检查仍能发现最初的错误提交。
		if err == nil || probe.protocolErr != err || probe.next != 1 {
			t.Fatalf("wrong commit consumed rollback or lost its error: next=%d err=%v recorded=%v", probe.next, err, probe.protocolErr)
		}
	})
	t.Run("database query cannot borrow another connection transaction", func(t *testing.T) {
		probe := &collectionSQLProbe{steps: []collectionSQLStep{{kind: "begin"}, {kind: "query"}, {kind: "rollback"}}}
		db := sql.OpenDB(probe)
		defer db.Close()
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(t.Context(), "SELECT 1")
		if rows != nil {
			_ = rows.Close()
		}
		if err == nil || probe.protocolErr != err || probe.next != 1 || len(probe.connections) != 2 {
			t.Fatalf("query escaped its connection transaction: next=%d connections=%d err=%v recorded=%v", probe.next, len(probe.connections), err, probe.protocolErr)
		}
		if rollbackErr := tx.Rollback(); rollbackErr != err || probe.connections[0].activeTx != nil {
			t.Fatalf("rollback lost first protocol error or retained transaction: %v", rollbackErr)
		}
	})
}

func collectionSummaryReadSteps(closed bool) []collectionSQLStep {
	status, revision, actor := "collecting", int64(7), ""
	var completed driver.Value
	if closed {
		status, revision, actor = "grading", 8, "manager"
		completed = time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	}
	return []collectionSQLStep{
		{kind: "query", printLedgerQuery: printLedgerQuery{
			fragments: []string{"FROM exam e LEFT JOIN capture_collection c ON c.tenant_id=e.tenant_id", "e.tenant_id=$1 AND e.id=$2::uuid AND e.deleted_at IS NULL"},
			args:      []driver.Value{"tenant", "exam"}, columns: 5, values: [][]driver.Value{{status, revision, completed, actor, int64(2)}},
		}},
		{kind: "query", printLedgerQuery: printLedgerQuery{
			fragments: []string{"FROM exam_candidate_snapshot c", "cp.status<>'deleted'", "WHERE c.tenant_id=$1 AND c.exam_id=$2::uuid"},
			args:      []driver.Value{"tenant", "exam"}, columns: 16, values: [][]driver.Value{
				{"ready-student", "001", "Ready student", "Class", int64(1), "submission", "matched", "passed", "batch", int64(2), int64(2), int64(0), int64(2), int64(1), int64(2), false},
				{"absent-student", "002", "Absent student", "Class", int64(0), "", "", "", "", int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), false},
			},
		}},
		{kind: "query", printLedgerQuery: printLedgerQuery{
			fragments: []string{"FROM capture_file f JOIN capture_batch b ON b.tenant_id=f.tenant_id", "t.tenant_id=$1", "UNION ALL SELECT s.id::text", "ORDER BY 2,1"},
			args:      []driver.Value{"tenant", "exam"}, columns: 7,
		}},
	}
}

func collectionCompletionPrefix(t *testing.T, input CompleteCollectionInput, priorRevision int64) ([]collectionSQLStep, string) {
	t.Helper()
	request, err := json.Marshal(struct {
		Actor string
		Input CompleteCollectionInput
	}{"manager", input})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(request)
	requestHash := hex.EncodeToString(digest[:])
	return []collectionSQLStep{
		{kind: "begin"},
		{kind: "query", printLedgerQuery: printLedgerQuery{fragments: []string{"FROM exam WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE NOWAIT"}, args: []driver.Value{"tenant", "exam"}, columns: 1, values: [][]driver.Value{{"exam"}}}},
		{kind: "exec", printLedgerQuery: printLedgerQuery{fragments: []string{"INSERT INTO capture_collection(tenant_id,exam_id)", "ON CONFLICT DO NOTHING"}, args: []driver.Value{"tenant", "exam"}}},
		{kind: "query", printLedgerQuery: printLedgerQuery{fragments: []string{"SELECT revision FROM capture_collection WHERE tenant_id=$1 AND exam_id=$2::uuid FOR UPDATE"}, args: []driver.Value{"tenant", "exam"}, columns: 1, values: [][]driver.Value{{priorRevision}}}},
		{kind: "query", printLedgerQuery: printLedgerQuery{fragments: []string{"FROM capture_collection_completion WHERE tenant_id=$1 AND exam_id=$2::uuid AND command_id=$3"}, args: []driver.Value{"tenant", "exam", input.IdempotencyKey}, columns: 2}},
	}, requestHash
}

func TestCollectionSummaryReadUsesOneRepeatableSnapshotAndKeepsAbsenceSeparate(t *testing.T) {
	steps := append([]collectionSQLStep{{kind: "begin"}}, collectionSummaryReadSteps(false)...)
	steps = append(steps, collectionSQLStep{kind: "commit"})
	store, probe := collectionProbeStore(t, steps)
	summary, err := store.GetCollectionSummary(t.Context(), "tenant", "exam")
	if err != nil || !summary.CanComplete || summary.RosterCount != 2 || summary.RequiredCount != 1 || summary.ReadyCount != 1 || summary.AbsentCount != 1 || summary.PendingCount != 0 {
		t.Fatalf("frozen roster summary is inaccurate: %+v err=%v", summary, err)
	}
	if !probe.transaction.ReadOnly || probe.transaction.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		t.Fatalf("roster, pages and issues were not read in one stable snapshot: %+v", probe.transaction)
	}
}

func TestCollectionCompletionReplaysStoredCommandBeforeCheckingNewRevision(t *testing.T) {
	input := CompleteCollectionInput{Revision: 7, IdempotencyKey: "complete-once", Reason: "checked sheets"}
	for _, scenario := range []string{"exact replay", "same key with different payload", "stale new command"} {
		t.Run(scenario, func(t *testing.T) {
			steps, requestHash := collectionCompletionPrefix(t, input, 99)
			result, err := json.Marshal(CollectionSummary{ExamID: "exam", Revision: 8, Closed: true, ReadyCount: 1})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "same key with different payload" {
				requestHash = "another-request-digest"
			}
			if scenario != "stale new command" {
				steps[4].values = [][]driver.Value{{requestHash, result}}
			}
			// 重放已成功的命令只读原回执，并回滚本次锁事务。既有修订变化不应重做封存。
			steps = append(steps, collectionSQLStep{kind: "rollback"})
			store, _ := collectionProbeStore(t, steps)
			out, err := store.CompleteCollection(t.Context(), "tenant", "exam", "manager", input)
			if scenario == "exact replay" {
				if err != nil || !out.Closed || out.Revision != 8 || out.ReadyCount != 1 {
					t.Fatalf("stored completion not replayed: %+v %v", out, err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatalf("conflicting command accepted: %+v %v", out, err)
			}
		})
	}
}

func TestCollectionCompletionCommitsSealAndReceiptTogether(t *testing.T) {
	input := CompleteCollectionInput{Revision: 7, IdempotencyKey: "seal", Reason: "checked sheets"}
	failure := errors.New("synthetic completion receipt unavailable")
	for _, scenario := range []string{"success", "missing page blocks sealing", "receipt write fails"} {
		t.Run(scenario, func(t *testing.T) {
			steps, requestHash := collectionCompletionPrefix(t, input, 7)
			steps = append(steps, collectionSQLStep{kind: "query", printLedgerQuery: printLedgerQuery{
				fragments: []string{"FROM capture_batch WHERE tenant_id=$1 AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE NOWAIT"}, args: []driver.Value{"tenant", "exam"}, columns: 1, values: [][]driver.Value{{"batch"}},
			}})
			summarySteps := collectionSummaryReadSteps(false)
			if scenario == "missing page blocks sealing" {
				summarySteps[1].values[0][9] = int64(1)
			}
			steps = append(steps, summarySteps...)
			if scenario == "missing page blocks sealing" {
				steps = append(steps, collectionSQLStep{kind: "rollback"})
			} else {
				steps = append(steps,
					collectionSQLStep{kind: "exec", printLedgerQuery: printLedgerQuery{fragments: []string{"UPDATE capture_batch b SET status=CASE WHEN EXISTS", "THEN 'completed' ELSE 'cancelled' END", "WHERE b.tenant_id=$1 AND b.exam_id=$2::uuid"}, args: []driver.Value{"tenant", "exam"}}},
					collectionSQLStep{kind: "exec", printLedgerQuery: printLedgerQuery{fragments: []string{"UPDATE capture_collection SET completed_at=now(),completed_by=$3::uuid,revision=revision+1 WHERE tenant_id=$1 AND exam_id=$2::uuid"}, args: []driver.Value{"tenant", "exam", "manager"}}},
					collectionSQLStep{kind: "exec", printLedgerQuery: printLedgerQuery{fragments: []string{"UPDATE exam SET status='grading'", "WHERE tenant_id=$1 AND id=$2::uuid AND status='collecting'"}, args: []driver.Value{"tenant", "exam"}}},
				)
				steps = append(steps, collectionSummaryReadSteps(true)...)
				closedAt := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
				expected := CollectionSummary{
					ExamID: "exam", ExamStatus: "grading", Revision: 8, Closed: true, CompletedAt: &closedAt, CompletedBy: "manager", PagesPerStudent: 2,
					RosterCount: 2, RequiredCount: 1, ReadyCount: 1, AbsentCount: 1, Blockers: []string{}, Issues: []CollectionIssue{},
					Entries: []CollectionEntry{
						{StudentID: "ready-student", StudentNo: "001", StudentName: "Ready student", ClassName: "Class", Status: "ready", Code: "import_complete", BatchID: "batch", SubmissionID: "submission", ExpectedPages: 2, ActualPages: 2},
						{StudentID: "absent-student", StudentNo: "002", StudentName: "Absent student", ClassName: "Class", Status: "absent", Code: "absent", ExpectedPages: 2},
					},
				}
				result, err := json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
				step := collectionSQLStep{kind: "exec", printLedgerQuery: printLedgerQuery{
					fragments: []string{"INSERT INTO capture_collection_completion(tenant_id,exam_id,command_id,request_hash,actor_id,result)"}, args: []driver.Value{"tenant", "exam", input.IdempotencyKey, requestHash, "manager", result},
				}}
				if scenario == "receipt write fails" {
					step.queryErr = failure
					steps = append(steps, step, collectionSQLStep{kind: "rollback"})
				} else {
					steps = append(steps, step, collectionSQLStep{kind: "commit"})
				}
			}
			store, _ := collectionProbeStore(t, steps)
			out, err := store.CompleteCollection(t.Context(), "tenant", "exam", "manager", input)
			switch scenario {
			case "success":
				if err != nil || !out.Closed || out.Revision != 8 || out.CanComplete || out.CompletedBy != "manager" {
					t.Fatalf("seal did not return its committed receipt: %+v %v", out, err)
				}
			case "missing page blocks sealing":
				if !errors.Is(err, ErrInvalidTransition) || out.Closed || out.PendingCount != 1 || out.CanComplete {
					t.Fatalf("missing-page roster was sealed: %+v %v", out, err)
				}
			case "receipt write fails":
				if !errors.Is(err, failure) {
					t.Fatalf("late receipt failure was not returned for rollback: %+v %v", out, err)
				}
			}
		})
	}
}

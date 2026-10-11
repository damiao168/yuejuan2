package main

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type repairBatch50Step struct {
	kind, fragment string
	args           []any
	columns        int
	rows           [][]driver.Value
	changed        int64
	err, rowsErr   error
}

type repairBatch50Script struct {
	steps []repairBatch50Step
	next  int
	end   string
	err   error
}

func (s *repairBatch50Script) take(kind, query string, args []driver.NamedValue) (repairBatch50Step, error) {
	if s.next == len(s.steps) {
		s.err = fmt.Errorf("unexpected %s: %s", kind, query)
		return repairBatch50Step{}, s.err
	}
	step := s.steps[s.next]
	s.next++
	values := make([]any, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if kind != step.kind || !strings.Contains(query, step.fragment) || !reflect.DeepEqual(values, step.args) {
		s.err = fmt.Errorf("step %d: %s args=%v, want %s %s args=%v", s.next, kind, values, step.kind, step.fragment, step.args)
		return step, s.err
	}
	return step, step.err
}

type repairBatch50Connector struct{ script *repairBatch50Script }

func (c repairBatch50Connector) Connect(context.Context) (driver.Conn, error) {
	return &repairBatch50Conn{c.script}, nil
}
func (c repairBatch50Connector) Driver() driver.Driver { return c }
func (repairBatch50Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type repairBatch50Conn struct{ script *repairBatch50Script }

func (*repairBatch50Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*repairBatch50Conn) Close() error                { return nil }
func (c *repairBatch50Conn) Begin() (driver.Tx, error) { c.script.end = "began"; return c, nil }
func (c *repairBatch50Conn) Commit() error             { c.script.end = "commit"; return nil }
func (c *repairBatch50Conn) Rollback() error           { c.script.end = "rollback"; return nil }
func (c *repairBatch50Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	return &repairBatch50Rows{step: step}, nil
}
func (c *repairBatch50Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step, err := c.script.take("exec", query, args)
	return driver.RowsAffected(step.changed), err
}

type repairBatch50Rows struct {
	step repairBatch50Step
	next int
}

func (r *repairBatch50Rows) Columns() []string { return make([]string, r.step.columns) }
func (*repairBatch50Rows) Close() error        { return nil }
func (r *repairBatch50Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		if r.step.rowsErr != nil {
			return r.step.rowsErr
		}
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

func repairBatch50DB(t *testing.T, steps []repairBatch50Step) (*sql.DB, *repairBatch50Script) {
	t.Helper()
	script := &repairBatch50Script{steps: steps}
	db := sql.OpenDB(repairBatch50Connector{script})
	t.Cleanup(func() {
		_ = db.Close()
		if script.err != nil || script.next != len(script.steps) {
			t.Errorf("SQL script error=%v steps=%d/%d", script.err, script.next, len(script.steps))
		}
	})
	return db, script
}

func TestBatch50RepairActionPreservesPrecedence(t *testing.T) {
	for _, tc := range []struct {
		finding finding
		want    string
	}{
		{finding{RunStatus: "missing", JobStatus: "processing", DispatchStatus: "pending"}, "mark_failed_missing_run"},
		{finding{RunStatus: "processing", JobStatus: "processing", TerminalTaskCount: 1, DispatchStatus: "pending"}, "mark_failed_no_sources"},
		{finding{RunStatus: "processing", JobStatus: "processing", SourceCount: 1, TerminalTaskCount: 1, DispatchStatus: "pending"}, "create_recovery_generation"},
		{finding{RunStatus: "processing", JobStatus: "processing", SourceCount: 1, DispatchStatus: "failed"}, "await_dispatch_reconciler"},
		{finding{RunStatus: "processing", JobStatus: "processing", SourceCount: 1, DispatchStatus: "dispatched"}, "reset_durable_dispatch_intent"},
		{finding{RunStatus: "review_required", DispatchStatus: "not_required"}, "mark_candidate_provenance_needs_review"},
		{finding{RunStatus: "processing", JobStatus: "processing", SourceCount: 1, RecoverableTaskCount: 1}, "none"},
	} {
		if got := paperImportRepairAction(tc.finding); got != tc.want {
			t.Fatalf("action=%s want=%s finding=%+v", got, tc.want, tc.finding)
		}
	}
}

func TestBatch50RepairDryRunReadsBothScopesWithoutStartingTransaction(t *testing.T) {
	db, script := repairBatch50DB(t, []repairBatch50Step{
		{kind: "query", fragment: "ORDER BY job.created_at,job.id", args: []any{"tenant", "", "exam"}, columns: 9, rows: [][]driver.Value{{"tenant", "import", int64(2), "processing", "missing", "missing", int64(0), int64(0), int64(0)}}},
		{kind: "query", fragment: "AND $2=''", args: []any{"tenant", "", "exam"}, columns: 6, rows: [][]driver.Value{{"tenant", "actor", "missing-command", "business-hash", "session", "missing"}, {"tenant", "actor", "processing-command", "business-hash", "session", "processing"}}},
	})
	var output bytes.Buffer
	if err := runRepair(t.Context(), db, "tenant", "", "exam", false, &output); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Mode     string           `json:"mode"`
		Count    int              `json:"count"`
		Commands []commandFinding `json:"exam_command_findings"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "dry-run" || report.Count != 3 || report.Commands[0].Action != "use_business_command_recovery" || report.Commands[0].RecoveryPath != "/api/v1/exam-sessions/commands/missing-command" || report.Commands[1].Action != "complete_replay_from_business_fact" || script.end != "" {
		t.Fatalf("dry-run report=%+v transaction=%s", report, script.end)
	}
}

func TestBatch50RepairTransactionKeepsLockRefreshEvidenceAndRollbackOrder(t *testing.T) {
	failure := errors.New("repair failed")
	for _, phase := range []string{"success", "stale", "run write", "evidence"} {
		t.Run(phase, func(t *testing.T) {
			assertRepairTransactionLockAndRollbackPhase(t, phase, failure)
		})
	}
}

type paperBatch50Recovery struct {
	job, recovered paper.PaperImportJob
	input          paper.ReplacePaperImportSourcesInput
	args           []string
	replaced       bool
}

func (s *paperBatch50Recovery) GetPaperImport(_ context.Context, tenantID, importID string) (paper.PaperImportJob, error) {
	s.args = []string{tenantID, importID}
	return s.job, nil
}
func (s *paperBatch50Recovery) ReplacePaperImportSources(_ context.Context, tenantID, importID, actorID string, input paper.ReplacePaperImportSourcesInput) (paper.PaperImportJob, error) {
	s.args = append(s.args, tenantID, importID, actorID)
	s.input, s.replaced = input, true
	return s.recovered, nil
}

func TestBatch50RecoveryGenerationKeepsCommandAndSourceVersion(t *testing.T) {
	f := finding{TenantID: "tenant", ImportID: "import", Generation: 2, Action: "create_recovery_generation"}
	for _, stale := range []bool{false, true} {
		store := &paperBatch50Recovery{job: paper.PaperImportJob{ID: "import", CreatedBy: "creator", Generation: 2, Sources: []paper.PaperImportSource{{ID: "source-b", DocumentIndex: 7, RoleHint: "answer"}, {ID: "source-a", DocumentIndex: 1, RoleHint: "paper"}}}, recovered: paper.PaperImportJob{Generation: 3}}
		if stale {
			store.job.Generation = 3
		}
		count, err := createRecoveryGenerations(t.Context(), store, []finding{{Action: "none"}, f})
		if stale {
			if err == nil || store.replaced || count != 0 {
				t.Fatalf("stale recovery count=%d replaced=%t error=%v", count, store.replaced, err)
			}
			continue
		}
		wantSources := []paper.ReplacePaperImportSourceInput{{ID: "source-b", DocumentIndex: 7, RoleHint: "answer"}, {ID: "source-a", DocumentIndex: 1, RoleHint: "paper"}}
		if err != nil || count != 1 || !reflect.DeepEqual(store.args, []string{"tenant", "import", "tenant", "import", "creator"}) || store.input.CommandID != "repair:import:g:2" || store.input.ExpectedGeneration != 2 || !reflect.DeepEqual(store.input.Sources, wantSources) {
			t.Fatalf("recovery count=%d args=%v input=%+v error=%v", count, store.args, store.input, err)
		}
	}
}

type examBatch50Recovery struct {
	result exam.ExamSessionCommandResult
	err    error
	scope  auth.AccessScope
	args   []string
}

func (s *examBatch50Recovery) RecoverExamSessionCommand(_ context.Context, scope auth.AccessScope, actorID, commandID string) (exam.ExamSessionCommandResult, error) {
	s.scope, s.args = scope, []string{actorID, commandID}
	return s.result, s.err
}

func TestBatch50ExamReplayRequiresMatchingBusinessFactAndGuardedReceipt(t *testing.T) {
	f := commandFinding{TenantID: "tenant", ActorID: "actor", CommandID: "command", SessionID: "session", RequestHash: "business-hash", Action: "complete_replay_from_business_fact"}
	for _, state := range []string{"success", "wrong session", "processing", "concurrent receipt", "missing receipt"} {
		t.Run(state, func(t *testing.T) {
			assertExamReplayBusinessFactAndReceipt(t, state, f)
		})
	}
}

func TestBatch50RepairInspectionDoesNotIgnoreLaterRowFailure(t *testing.T) {
	failure := errors.New("later row unavailable")
	db, _ := repairBatch50DB(t, []repairBatch50Step{{kind: "query", fragment: "ORDER BY job.created_at,job.id", args: []any{"tenant", "import", ""}, columns: 9, rowsErr: failure}})
	if findings, err := inspectPaperImportFindings(t.Context(), db, "tenant", "import", ""); !errors.Is(err, failure) || findings != nil {
		t.Fatalf("partial findings=%+v error=%v", findings, err)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertRepairTransactionLockAndRollbackPhase(t *testing.T, phase string, failure error) {
	t.Helper()
	steps := []repairBatch50Step{
		{kind: "query", fragment: "FOR UPDATE", args: []any{"tenant", "import"}, columns: 1, rows: [][]driver.Value{{int64(2)}}},
		{kind: "exec", fragment: "UPDATE paper_import_job", args: []any{"tenant", "import"}, changed: 1},
		{kind: "exec", fragment: "UPDATE paper_import_run", args: []any{"tenant", "import", int64(2)}, changed: 1},
		{kind: "exec", fragment: "request_processing_projection_refresh", args: []any{"tenant", "exam"}},
		{kind: "query", fragment: "SELECT jsonb_build_object(", args: []any{"tenant", "import"}, columns: 1, rows: [][]driver.Value{{[]byte(`{"generation":2}`)}}},
	}
	switch phase {
	case "stale":
		steps[0].rows = [][]driver.Value{{int64(3)}}
		steps = steps[:1]
	case "run write":
		steps[2].err = failure
		steps = steps[:3]
	case "evidence":
		steps[4].err = failure
	}
	db, script := repairBatch50DB(t, steps)
	after, err := applyRepairTransaction(t.Context(), db, "tenant", "exam", []finding{{TenantID: "tenant", ImportID: "import", Generation: 2, Action: "mark_failed_no_sources"}}, nil)
	if phase == "success" {
		if err != nil || after.mutations != 1 || len(after.imports) != 1 || after.commands == nil || script.end != "commit" {
			t.Fatalf("transaction after=%+v error=%v end=%s", after, err, script.end)
		}
		return
	}
	if err == nil || script.end != "rollback" || after.mutations != 0 {
		t.Fatalf("failed transaction after=%+v error=%v end=%s", after, err, script.end)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertExamReplayBusinessFactAndReceipt(t *testing.T, state string, f commandFinding) {
	t.Helper()
	recoverer := &examBatch50Recovery{result: exam.ExamSessionCommandResult{Status: "succeeded", Session: &exam.ExamSession{ID: "session"}}}
	finding := f
	var steps []repairBatch50Step
	switch state {
	case "wrong session":
		recoverer.result.Session.ID = "other"
	case "processing":
		recoverer.result.Status = "processing"
	case "missing receipt":
		finding.Action = "use_business_command_recovery"
	default:
		body, err := json.Marshal(map[string]any{"exam_session": recoverer.result.Session})
		if err != nil {
			t.Fatal(err)
		}
		changed := int64(1)
		if state == "concurrent receipt" {
			changed = 0
		}
		steps = []repairBatch50Step{{kind: "exec", fragment: "session.command_request_hash=$5", args: []any{"tenant", "actor", "command", "session", "business-hash", body}, changed: changed}}
	}
	db, _ := repairBatch50DB(t, steps)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	count, err := applyExamCommandFindings(t.Context(), tx, recoverer, []commandFinding{finding})
	if state == "missing receipt" {
		if err != nil || count != 0 || recoverer.args != nil {
			t.Fatalf("missing receipt was rebuilt: count=%d args=%v error=%v", count, recoverer.args, err)
		}
		return
	}
	if recoverer.scope.TenantID != "tenant" || !recoverer.scope.TenantWide || !reflect.DeepEqual(recoverer.args, []string{"actor", "command"}) {
		t.Fatalf("recovery scope=%+v args=%v", recoverer.scope, recoverer.args)
	}
	if (err == nil) != (state == "success") || (count == 1) != (state == "success") {
		t.Fatalf("state=%s count=%d error=%v", state, count, err)
	}
}

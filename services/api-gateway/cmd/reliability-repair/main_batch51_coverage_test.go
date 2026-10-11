package main

import (
	"bytes"
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

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// 复用批50的 SQL 顺序与参数校验，只增加故障和事务次数；原用例不改。
type repairBatch51State struct {
	script                       *repairBatch50Script
	beginErr, commitErr, rowsErr error
	begins, commits, rollbacks   int
	options                      []driver.TxOptions
}

type repairBatch51Connector struct {
	repairBatch50Connector
	state *repairBatch51State
}

func (c repairBatch51Connector) Connect(context.Context) (driver.Conn, error) {
	return &repairBatch51Conn{repairBatch50Conn: &repairBatch50Conn{script: c.state.script}, state: c.state}, nil
}

type repairBatch51Conn struct {
	*repairBatch50Conn
	state *repairBatch51State
}

func (c *repairBatch51Conn) Begin() (driver.Tx, error) {
	c.state.begins++
	if c.state.beginErr != nil {
		return nil, c.state.beginErr
	}
	c.state.script.end = "began"
	return c, nil
}

func (c *repairBatch51Conn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.state.options = append(c.state.options, options)
	return c.Begin()
}

func (c *repairBatch51Conn) Commit() error {
	c.state.commits++
	c.state.script.end = "commit"
	return c.state.commitErr
}

func (c *repairBatch51Conn) Rollback() error {
	c.state.rollbacks++
	c.state.script.end = "rollback"
	return nil
}

type repairBatch51Result struct {
	driver.Result
	err error
}

func (r repairBatch51Result) RowsAffected() (int64, error) {
	if r.err != nil {
		return 0, r.err
	}
	return r.Result.RowsAffected()
}

func (c *repairBatch51Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	step, err := c.state.script.take("exec", query, args)
	return repairBatch51Result{Result: driver.RowsAffected(step.changed), err: c.state.rowsErr}, err
}

func repairBatch51DB(t *testing.T, steps []repairBatch50Step) (*sql.DB, *repairBatch51State) {
	t.Helper()
	state := &repairBatch51State{script: &repairBatch50Script{steps: steps}}
	db := sql.OpenDB(repairBatch51Connector{repairBatch50Connector: repairBatch50Connector{script: state.script}, state: state})
	t.Cleanup(func() {
		_ = db.Close()
		if state.script.err != nil || state.script.next != len(state.script.steps) {
			t.Errorf("SQL script error=%v steps=%d/%d", state.script.err, state.script.next, len(state.script.steps))
		}
	})
	return db, state
}

func repairBatch51InspectionSteps() []repairBatch50Step {
	return []repairBatch50Step{
		{kind: "query", fragment: "ORDER BY job.created_at,job.id", args: []any{"tenant", "", ""}, columns: 9},
		{kind: "query", fragment: "ORDER BY session.command_completed_at,session.id", args: []any{"tenant", "", ""}, columns: 6},
	}
}

func TestBatch51RepairInspectionRejectsIncompleteEvidenceBeforeMutation(t *testing.T) {
	failure := errors.New("inspection unavailable")
	for _, phase := range []string{"paper query", "paper scan", "command query", "command scan", "command rows"} {
		t.Run(phase, func(t *testing.T) {
			assertIncompleteRepairInspectionStopsMutation(t, phase, failure)
		})
	}
}

func TestBatch51RepairApplyReportsOnlyCommittedFactsWithoutInventingMutations(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "await dispatch"}[pending], func(t *testing.T) {
			assertRepairApplyReportsCommittedFacts(t, pending)
		})
	}
}

func TestBatch51RepairTransactionFailuresReturnNoAppliedState(t *testing.T) {
	failure := errors.New("repair operation unavailable")
	for _, phase := range []string{"begin", "lock", "affected rows", "projection", "command recovery", "command evidence", "commit"} {
		t.Run(phase, func(t *testing.T) {
			assertRepairTransactionFailureReturnsNoState(t, phase, failure)
		})
	}
}

func TestBatch51RepairGenerationReadFailureStopsBeforeRepairTransaction(t *testing.T) {
	failure := errors.New("recovery source read unavailable")
	steps := repairBatch51InspectionSteps()
	steps[0].rows = [][]driver.Value{{"tenant", "import", int64(2), "processing", "processing", "dispatched", int64(1), int64(0), int64(1)}}
	steps = append(steps, repairBatch50Step{kind: "query", fragment: "FROM paper_import_job WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL", args: []any{"tenant", "import"}, err: failure})
	db, state := repairBatch51DB(t, steps)
	var output bytes.Buffer
	if err := runRepair(t.Context(), db, "tenant", "", "", true, &output); !errors.Is(err, failure) {
		t.Fatalf("recovery failure lost: %v", err)
	}
	var before struct {
		Findings []finding `json:"paper_import_findings"`
	}
	if err := json.Unmarshal(output.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if len(before.Findings) != 1 || before.Findings[0].Action != "create_recovery_generation" || state.begins != 0 || state.commits != 0 || state.rollbacks != 0 {
		t.Fatalf("failed recovery began later repair: report=%+v tx=%+v", before, state)
	}
}

func TestBatch51RepairTransactionBeginFailureKeepsOnlyDiagnosticReport(t *testing.T) {
	failure := errors.New("repair transaction unavailable")
	db, state := repairBatch51DB(t, repairBatch51InspectionSteps())
	state.beginErr = failure
	var output bytes.Buffer
	if err := runRepair(t.Context(), db, "tenant", "", "", true, &output); !errors.Is(err, failure) {
		t.Fatalf("transaction begin failure lost: %v", err)
	}
	var report struct {
		Mode    string `json:"mode"`
		Count   int    `json:"count"`
		Applied *bool  `json:"applied"`
	}
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "apply" || report.Count != 0 || report.Applied != nil || state.begins != 1 || state.commits != 0 || state.rollbacks != 0 {
		t.Fatalf("failed begin reported applied state: report=%+v tx=%+v", report, state)
	}
}

type paperBatch51Recovery struct {
	jobs                                map[string]paper.PaperImportJob
	getErrID, replaceErrID, unchangedID string
	failure                             error
	gets, replaces                      []string
	inputs                              map[string]paper.ReplacePaperImportSourcesInput
}

func (s *paperBatch51Recovery) GetPaperImport(_ context.Context, tenantID, importID string) (paper.PaperImportJob, error) {
	s.gets = append(s.gets, tenantID+"/"+importID)
	if importID == s.getErrID {
		return paper.PaperImportJob{}, s.failure
	}
	return s.jobs[importID], nil
}

func (s *paperBatch51Recovery) ReplacePaperImportSources(_ context.Context, tenantID, importID, actorID string, input paper.ReplacePaperImportSourcesInput) (paper.PaperImportJob, error) {
	s.replaces = append(s.replaces, tenantID+"/"+importID+"/"+actorID)
	s.inputs[importID] = input
	if importID == s.replaceErrID {
		return paper.PaperImportJob{}, s.failure
	}
	job := s.jobs[importID]
	if importID != s.unchangedID {
		job.Generation++
		s.jobs[importID] = job
	}
	return job, nil
}

func TestBatch51RecoveryFailureKeepsEarlierCompletedGenerationAndStopsLaterItems(t *testing.T) {
	failure := errors.New("generation recovery unavailable")
	for _, phase := range []string{"read", "replace", "no new generation"} {
		t.Run(phase, func(t *testing.T) {
			assertRecoveryFailureStopsLaterGeneration(t, phase, failure)
		})
	}
}

func TestBatch51ExamReplayEncodingOrWriteFailureCannotCompleteReceipt(t *testing.T) {
	failure := errors.New("receipt write unavailable")
	for _, phase := range []string{"encoding", "write"} {
		t.Run(phase, func(t *testing.T) {
			assertExamReplayFailureCannotCompleteReceipt(t, phase, failure)
		})
	}
}

type repairBatch51Writer struct {
	bytes.Buffer
	writes int
	err    error
}

func (w *repairBatch51Writer) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		return 0, w.err
	}
	return w.Buffer.Write(data)
}

func TestBatch51CommittedRepairIsNotRolledBackWhenFinalReportCannotBeWritten(t *testing.T) {
	failure := errors.New("final report unavailable")
	db, state := repairBatch51DB(t, repairBatch51InspectionSteps())
	output := &repairBatch51Writer{err: failure}
	if err := runRepair(t.Context(), db, "tenant", "", "", true, output); !errors.Is(err, failure) {
		t.Fatalf("final output failure lost: %v", err)
	}
	var diagnostic struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(output.Bytes(), &diagnostic); err != nil {
		t.Fatal(err)
	}
	if diagnostic.Mode != "apply" || output.writes != 2 || state.begins != 1 || state.commits != 1 || state.rollbacks != 0 {
		t.Fatalf("committed outcome misreported as rollback: diagnostic=%+v writes=%d tx=%+v", diagnostic, output.writes, state)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertIncompleteRepairInspectionStopsMutation(t *testing.T, phase string, failure error) {
	t.Helper()
	steps := repairBatch51InspectionSteps()
	switch phase {
	case "paper query":
		steps[0].err = failure
		steps = steps[:1]
	case "paper scan":
		steps[0].rows = [][]driver.Value{{"tenant", "import", "invalid-generation", "processing", "processing", "pending", int64(1), int64(0), int64(0)}}
		steps = steps[:1]
	case "command query":
		steps[1].err = failure
	case "command scan":
		steps[1].rows = [][]driver.Value{{"tenant", "actor", "command-id", "hash", nil, "processing"}}
	case "command rows":
		steps[1].rowsErr = failure
	}
	db, state := repairBatch51DB(t, steps)
	var output bytes.Buffer
	err := runRepair(t.Context(), db, "tenant", "", "", true, &output)
	if err == nil || output.Len() != 0 || state.begins != 0 || state.commits != 0 || state.rollbacks != 0 {
		t.Fatalf("incomplete inspection error=%v output=%s tx=%+v", err, output.String(), state)
	}
	if phase == "paper scan" || phase == "command scan" {
		if !strings.Contains(err.Error(), "Scan error") {
			t.Fatalf("scan failure was hidden: %v", err)
		}
	} else if !errors.Is(err, failure) {
		t.Fatalf("inspection error lost identity: %v", err)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertRepairApplyReportsCommittedFacts(t *testing.T, pending bool) {
	t.Helper()
	steps := repairBatch51InspectionSteps()
	count := 0
	if pending {
		count = 1
		steps[0].rows = [][]driver.Value{{"tenant", "import", int64(2), "processing", "processing", "pending", int64(1), int64(0), int64(0)}}
		steps = append(steps,
			repairBatch50Step{kind: "query", fragment: "FOR UPDATE", args: []any{"tenant", "import"}, columns: 1, rows: [][]driver.Value{{int64(2)}}},
			repairBatch50Step{kind: "query", fragment: "SELECT jsonb_build_object(", args: []any{"tenant", "import"}, columns: 1, rows: [][]driver.Value{{[]byte(`{"generation":2,"dispatch_status":"pending"}`)}}},
		)
	}
	db, state := repairBatch51DB(t, steps)
	var output bytes.Buffer
	if err := runRepair(t.Context(), db, "tenant", "", "", true, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var before struct {
		Mode     string    `json:"mode"`
		Count    int       `json:"count"`
		Findings []finding `json:"paper_import_findings"`
	}
	var after struct {
		Applied   bool              `json:"applied"`
		Mutations int               `json:"mutations"`
		Imports   []json.RawMessage `json:"paper_import_after"`
		Commands  []json.RawMessage `json:"exam_command_after"`
	}
	if err := decoder.Decode(&before); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&after); err != nil {
		t.Fatal(err)
	}
	if before.Mode != "apply" || before.Count != count || !after.Applied || after.Mutations != 0 || len(after.Imports) != count || after.Commands == nil || state.begins != 1 || state.commits != 1 || state.rollbacks != 0 {
		t.Fatalf("before=%+v after=%+v tx=%+v", before, after, state)
	}
	if pending && (before.Findings[0].Action != "await_dispatch_reconciler" || string(after.Imports[0]) != `{"generation":2,"dispatch_status":"pending"}`) {
		t.Fatalf("pending dispatch was mutated or evidence changed: before=%+v after=%+v", before, after)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		t.Fatalf("unexpected extra report: %v", err)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertRepairTransactionFailureReturnsNoState(t *testing.T, phase string, failure error) {
	t.Helper()
	db, state, examID, findings, commands := repairTransactionFailureFixture(t, phase, failure)
	after, err := applyRepairTransaction(t.Context(), db, "tenant", examID, findings, commands)
	if err == nil || after.mutations != 0 || after.imports != nil || after.commands != nil {
		t.Fatalf("failure returned applied evidence: after=%+v error=%v", after, err)
	}
	if phase == "command recovery" {
		if !strings.Contains(err.Error(), failure.Error()) || state.begins != 2 || len(state.options) != 2 || !state.options[1].ReadOnly || state.commits != 0 || state.rollbacks != 2 {
			t.Fatalf("business recovery failure error=%v tx=%+v", err, state)
		}
		return
	}
	if !errors.Is(err, failure) || state.begins != 1 {
		t.Fatalf("failure identity or begin count changed: error=%v tx=%+v", err, state)
	}
	wantCommit, wantRollback := 0, 1
	if phase == "begin" {
		wantRollback = 0
	}
	if phase == "commit" {
		// Commit 调用后 sql.Tx 已结束；不能把失败说成已由 defer 真正回滚。
		wantCommit, wantRollback = 1, 0
	}
	if state.commits != wantCommit || state.rollbacks != wantRollback {
		t.Fatalf("phase=%s commit=%d rollback=%d", phase, state.commits, state.rollbacks)
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertRecoveryFailureStopsLaterGeneration(t *testing.T, phase string, failure error) {
	t.Helper()
	store := &paperBatch51Recovery{jobs: make(map[string]paper.PaperImportJob), inputs: make(map[string]paper.ReplacePaperImportSourcesInput), failure: failure}
	var findings []finding
	for _, id := range []string{"first", "failed", "later"} {
		store.jobs[id] = paper.PaperImportJob{ID: id, Generation: 2, CreatedBy: "creator", Sources: []paper.PaperImportSource{{ID: "source", DocumentIndex: 7, RoleHint: "answer"}}}
		findings = append(findings, finding{TenantID: "tenant", ImportID: id, Generation: 2, Action: "create_recovery_generation"})
	}
	switch phase {
	case "read":
		store.getErrID = "failed"
	case "replace":
		store.replaceErrID = "failed"
	case "no new generation":
		store.unchangedID = "failed"
	}
	count, err := createRecoveryGenerations(t.Context(), store, findings)
	if err == nil || count != 1 || store.jobs["first"].Generation != 3 || store.jobs["failed"].Generation != 2 || store.jobs["later"].Generation != 2 || !reflect.DeepEqual(store.gets, []string{"tenant/first", "tenant/failed"}) {
		t.Fatalf("partial recovery count=%d error=%v jobs=%+v gets=%v", count, err, store.jobs, store.gets)
	}
	if phase == "no new generation" {
		if err.Error() != "repair did not establish a new run" {
			t.Fatalf("non-advancing recovery accepted: %v", err)
		}
	} else if !errors.Is(err, failure) {
		t.Fatalf("recovery error identity changed: %v", err)
	}
	wantReplaces := []string{"tenant/first/creator"}
	if phase != "read" {
		wantReplaces = append(wantReplaces, "tenant/failed/creator")
	}
	if !reflect.DeepEqual(store.replaces, wantReplaces) {
		t.Fatalf("unexpected replacement scope/order: %v", store.replaces)
	}
	wantSources := []paper.ReplacePaperImportSourceInput{{ID: "source", DocumentIndex: 7, RoleHint: "answer"}}
	for id, input := range store.inputs {
		if input.CommandID != "repair:"+id+":g:2" || input.ExpectedGeneration != 2 || !reflect.DeepEqual(input.Sources, wantSources) {
			t.Fatalf("recovery input lost command/version/source: id=%s input=%+v", id, input)
		}
	}
}

// 每个故障阶段单独执行原顺序脚本，断言保留事务及回执的失败边界。
func assertExamReplayFailureCannotCompleteReceipt(t *testing.T, phase string, failure error) {
	t.Helper()
	f := commandFinding{TenantID: "tenant", ActorID: "actor", CommandID: "command-id", SessionID: "session", RequestHash: "business-hash", Action: "complete_replay_from_business_fact"}
	recoverer := &examBatch50Recovery{result: exam.ExamSessionCommandResult{Status: "succeeded", Session: &exam.ExamSession{ID: "session"}}}
	var steps []repairBatch50Step
	if phase == "encoding" {
		// PostgreSQL 可存超出 JSON RFC3339 范围的日期；编码失败不得写完成回执。
		recoverer.result.Session.CreatedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	} else {
		body, err := json.Marshal(map[string]any{"exam_session": recoverer.result.Session})
		if err != nil {
			t.Fatal(err)
		}
		steps = []repairBatch50Step{{kind: "exec", fragment: "session.command_request_hash=$5", args: []any{"tenant", "actor", "command-id", "session", "business-hash", body}, err: failure}}
	}
	db, state := repairBatch51DB(t, steps)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	count, err := applyExamCommandFindings(t.Context(), tx, recoverer, []commandFinding{f})
	if err == nil || count != 0 || recoverer.scope.TenantID != "tenant" || !recoverer.scope.TenantWide || !reflect.DeepEqual(recoverer.args, []string{"actor", "command-id"}) {
		t.Fatalf("failed replay count=%d error=%v scope=%+v args=%v", count, err, recoverer.scope, recoverer.args)
	}
	if phase == "encoding" {
		var encodingErr *json.MarshalerError
		if !errors.As(err, &encodingErr) {
			t.Fatalf("encoding failure lost type: %v", err)
		}
	} else if !errors.Is(err, failure) {
		t.Fatalf("receipt write failure lost: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if state.commits != 0 || state.rollbacks != 1 {
		t.Fatalf("failed receipt transaction completed: %+v", state)
	}
}

// 先设置故障脚本，再执行修复；提交失败仍由原断言区分 sql.Tx 已结束的情形。
func repairTransactionFailureFixture(t *testing.T, phase string, failure error) (*sql.DB, *repairBatch51State, string, []finding, []commandFinding) {
	t.Helper()
	var steps []repairBatch50Step
	var findings []finding
	var commands []commandFinding
	examID := ""
	if phase == "lock" || phase == "affected rows" {
		findings = []finding{{TenantID: "tenant", ImportID: "import", Generation: 2, Action: "reset_durable_dispatch_intent"}}
		steps = []repairBatch50Step{{kind: "query", fragment: "FOR UPDATE", args: []any{"tenant", "import"}, columns: 1, rows: [][]driver.Value{{int64(2)}}}}
	}
	switch phase {
	case "lock":
		steps[0].err = failure
	case "affected rows":
		steps = append(steps, repairBatch50Step{kind: "exec", fragment: "AND NOT EXISTS(SELECT 1 FROM agent_worker_task", args: []any{"tenant", "import", int64(2)}, changed: 1})
	case "projection":
		examID = "exam"
		steps = []repairBatch50Step{{kind: "exec", fragment: "request_processing_projection_refresh", args: []any{"tenant", "exam"}, err: failure}}
	case "command recovery":
		commands = []commandFinding{{TenantID: "tenant", ActorID: "actor", CommandID: "command-id", SessionID: "session", Action: "complete_replay_from_business_fact"}}
		steps = []repairBatch50Step{{kind: "query", fragment: "WHERE tenant_id=$1 AND created_by=$2::uuid AND command_id=$3", args: []any{"tenant", "actor", "command-id"}, err: failure}}
	case "command evidence":
		commands = []commandFinding{{TenantID: "tenant", SessionID: "session", Action: "use_business_command_recovery"}}
		steps = []repairBatch50Step{{kind: "query", fragment: "jsonb_build_object('command_id',session.command_id", args: []any{"tenant", "session"}, err: failure}}
	}
	db, state := repairBatch51DB(t, steps)
	if phase == "begin" {
		state.beginErr = failure
	}
	if phase == "commit" {
		state.commitErr = failure
	}
	if phase == "affected rows" {
		state.rowsErr = failure
	}
	return db, state, examID, findings, commands
}

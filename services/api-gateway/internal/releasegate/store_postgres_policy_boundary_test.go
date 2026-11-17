package releasegate

import (
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
	"time"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type gateSQLStep struct {
	kind, query string
	args        []driver.Value
	rows        [][]driver.Value
	columns     int
	err         error
}

type gateSQLScript struct {
	steps   []gateSQLStep
	next    int
	active  *gateSQLConnection
	failure error
}

func (s *gateSQLScript) Connect(context.Context) (driver.Conn, error) {
	return &gateSQLConnection{script: s}, nil
}
func (s *gateSQLScript) Driver() driver.Driver { return s }
func (s *gateSQLScript) Open(string) (driver.Conn, error) {
	return s.Connect(context.Background())
}

func (s *gateSQLScript) reject(format string, args ...any) error {
	if s.failure == nil {
		s.failure = fmt.Errorf(format, args...)
	}
	return s.failure
}

func (s *gateSQLScript) take(kind, query string, args []driver.NamedValue) (gateSQLStep, error) {
	if s.next >= len(s.steps) {
		return gateSQLStep{}, s.reject("unexpected %s: %s", kind, query)
	}
	step := s.steps[s.next]
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if step.kind != kind || !strings.Contains(strings.Join(strings.Fields(query), " "), step.query) ||
		len(values) != len(step.args) || len(values) > 0 && !reflect.DeepEqual(values, step.args) {
		return gateSQLStep{}, s.reject("step %d: got %s %q args=%v, want %s %q args=%v", s.next+1, kind, query, values, step.kind, step.query, step.args)
	}
	s.next++
	return step, step.err
}

type gateSQLConnection struct{ script *gateSQLScript }

func (c *gateSQLConnection) Prepare(string) (driver.Stmt, error) {
	return nil, c.script.reject("unexpected prepare")
}
func (c *gateSQLConnection) Close() error {
	if c.script.active == c {
		return c.script.reject("connection closed before its transaction ended")
	}
	return nil
}
func (c *gateSQLConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *gateSQLConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	if c.script.active != nil {
		return nil, c.script.reject("another transaction is already active")
	}
	if _, err := c.script.take("begin", "", nil); err != nil {
		return nil, err
	}
	c.script.active = c
	return &gateSQLTransaction{connection: c}, nil
}
func (c *gateSQLConnection) checkTransaction() error {
	// 已开启事务时，数据库池分配的第二条连接不能执行本应随发布回滚的读取或写入。
	if c.script.active != nil && c.script.active != c {
		return c.script.reject("query escaped the active publication transaction")
	}
	return nil
}
func (c *gateSQLConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := c.checkTransaction(); err != nil {
		return nil, err
	}
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c *gateSQLConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.checkTransaction(); err != nil {
		return nil, err
	}
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	columns := step.columns
	if len(step.rows) > 0 {
		columns = len(step.rows[0])
	}
	return &gateSQLRows{rows: step.rows, columns: make([]string, columns)}, nil
}

type gateSQLTransaction struct{ connection *gateSQLConnection }

func (tx *gateSQLTransaction) finish(kind string) error {
	script := tx.connection.script
	if script.active != tx.connection {
		return script.reject("transaction ended without owning its connection")
	}
	_, err := script.take(kind, "", nil)
	script.active = nil
	return err
}
func (tx *gateSQLTransaction) Commit() error   { return tx.finish("commit") }
func (tx *gateSQLTransaction) Rollback() error { return tx.finish("rollback") }

type gateSQLRows struct {
	rows    [][]driver.Value
	columns []string
	next    int
}

func (r *gateSQLRows) Columns() []string { return r.columns }
func (r *gateSQLRows) Close() error      { return nil }
func (r *gateSQLRows) Next(dest []driver.Value) error {
	if r.next == len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}

func openGateSQLScript(t *testing.T, steps []gateSQLStep) *sql.DB {
	t.Helper()
	script := &gateSQLScript{steps: steps}
	db := sql.OpenDB(script)
	t.Cleanup(func() {
		_ = db.Close()
		// 协议错误独立留存，避免调用方忽略 defer Rollback 错误时测试仍通过。
		if script.failure != nil || script.active != nil || script.next != len(script.steps) {
			t.Errorf("publication SQL: error=%v active=%t steps=%d/%d", script.failure, script.active != nil, script.next, len(script.steps))
		}
	})
	return db
}

func TestGateSQLScriptRejectsQueriesOutsideThePublicationTransaction(t *testing.T) {
	for _, kind := range []string{"query", "exec"} {
		t.Run(kind, func(t *testing.T) {
			step := gateSQLStep{kind: kind, query: "publication statement", args: []driver.Value{"tenant"}, columns: 1, rows: [][]driver.Value{{int64(1)}}}
			script := &gateSQLScript{steps: []gateSQLStep{{kind: "begin"}, step, {kind: "rollback"}}}
			db := sql.OpenDB(script)
			t.Cleanup(func() { _ = db.Close() })
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			// 池查询必须另取连接。拒绝后脚本位置不变，原事务仍能执行同一条语句。
			if kind == "query" {
				var value int
				err = db.QueryRowContext(t.Context(), "publication statement", "tenant").Scan(&value)
			} else {
				_, err = db.ExecContext(t.Context(), "publication statement", "tenant")
			}
			if err == nil || script.failure != err || script.next != 1 || script.active == nil {
				t.Fatalf("transaction escape was accepted: err=%v failure=%v next=%d", err, script.failure, script.next)
			}
			if kind == "query" {
				var value int
				if err := tx.QueryRowContext(t.Context(), "publication statement", "tenant").Scan(&value); err != nil || value != 1 {
					t.Fatalf("original transaction read=%d, err=%v", value, err)
				}
			} else if _, err := tx.ExecContext(t.Context(), "publication statement", "tenant"); err != nil {
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil || script.active != nil || script.next != len(script.steps) {
				t.Fatalf("original transaction did not finish: err=%v next=%d active=%t", err, script.next, script.active != nil)
			}
		})
	}
}

func TestGateSQLScriptRecordsAnIgnoredWrongTransactionEnd(t *testing.T) {
	script := &gateSQLScript{steps: []gateSQLStep{{kind: "begin"}, {kind: "rollback"}}}
	db := sql.OpenDB(script)
	t.Cleanup(func() { _ = db.Close() })
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = tx.Commit()
	if err == nil || script.failure != err || script.next != 1 || script.active != nil {
		t.Fatalf("unexpected commit consumed rollback: err=%v next=%d active=%t", err, script.next, script.active != nil)
	}
}

func TestPostgresPolicyReplacementKeepsExamLockAndRetirementAtomic(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	input := CreatePolicyInput{Version: "school-v2", RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning"}}
	config := `{"require_warning_acknowledgement":true,"waivable_warning_codes":["quality_warning"]}`
	for _, stage := range []string{"success", "insert failure", "commit failure"} {
		t.Run(stage, func(t *testing.T) {
			failure := errors.New("synthetic policy persistence failure")
			steps := []gateSQLStep{
				{kind: "begin"},
				{kind: "exec", query: "pg_advisory_xact_lock(hashtext($1), hashtext($2))", args: []driver.Value{"tenant", "exam"}},
				{kind: "exec", query: "pg_advisory_xact_lock(hashtextextended($1, 0))", args: []driver.Value{"tenant:exam:release-gate-policy"}},
				{kind: "exec", query: "UPDATE release_gate_policy SET status = 'retired'", args: []driver.Value{"tenant", "exam"}},
				{kind: "query", query: "INSERT INTO release_gate_policy", args: []driver.Value{"tenant", "exam", input.Version, config, "manager"}, rows: [][]driver.Value{{"policy-v2", "tenant", "exam", input.Version, PolicyActive, []byte(config), "manager", now}}},
				{kind: "commit"},
			}
			if stage == "insert failure" {
				steps[4].err = failure
				steps[5].kind = "rollback"
			}
			if stage == "commit failure" {
				steps[5].err = failure
			}
			// 顺序断言保护发布共用的考试锁；写入失败须回滚，提交报错时不能返回看似生效的新策略。
			policy, err := NewPostgresStore(openGateSQLScript(t, steps)).CreatePolicy(t.Context(), "tenant", "exam", "manager", input)
			if stage != "success" {
				if !errors.Is(err, failure) || policy.ID != "" {
					t.Fatalf("failed replacement exposed an active policy: %+v %v", policy, err)
				}
				return
			}
			if err != nil || policy.ID != "policy-v2" || policy.TenantID != "tenant" || policy.Status != PolicyActive ||
				!policy.RequireWarningAcknowledgement || !reflect.DeepEqual(policy.WaivableWarningCodes, input.WaivableWarningCodes) || !policy.CreatedAt.Equal(now) {
				t.Fatalf("replacement lost policy scope or acknowledgement rules: %+v %v", policy, err)
			}
		})
	}
}

func TestPostgresWaiverDecisionPreservesTenantAndImmutableDecision(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, outcome := range []string{WaiverApproved, WaiverRejected, "already decided", "missing request"} {
		t.Run(outcome, func(t *testing.T) {
			steps := []gateSQLStep{
				{kind: "begin"},
				{kind: "query", query: "FROM release_gate_waiver_request WHERE tenant_id = $1 AND id = $2::uuid", args: []driver.Value{"tenant", "waiver"}, rows: [][]driver.Value{{true}}},
				{kind: "exec", query: "INSERT INTO release_gate_waiver_decision", args: []driver.Value{"tenant", "waiver", WaiverApproved, "independent review", "principal"}},
				{kind: "query", query: "WHERE request.tenant_id = $1 AND request.id = $2::uuid", args: []driver.Value{"tenant", "waiver"}, rows: [][]driver.Value{{"waiver", "tenant", "exam", "policy", "evidence", "quality_warning", "reviewed", "teacher", now, WaiverApproved, "principal", now, "independent review"}}},
				{kind: "commit"},
			}
			if outcome == WaiverRejected {
				steps[2].args[2], steps[3].rows[0][9] = WaiverRejected, WaiverRejected
			}
			if outcome == "already decided" {
				steps[2].err = errors.New("duplicate key violates release_gate_waiver_decision_tenant_id_waiver_id_key")
				steps = append(steps[:3], gateSQLStep{kind: "rollback"})
			}
			if outcome == "missing request" {
				steps[1].rows = [][]driver.Value{{false}}
				steps = append(steps[:2], gateSQLStep{kind: "rollback"})
			}
			waiver, err := NewPostgresStore(openGateSQLScript(t, steps)).DecideWaiver(t.Context(), "tenant", "waiver", "principal", DecideWaiverInput{Approve: outcome != WaiverRejected, Reason: "independent review"})
			if outcome == "already decided" || outcome == "missing request" {
				want := ErrEvidenceImmutable
				if outcome == "missing request" {
					want = ErrNotFound
				}
				if !errors.Is(err, want) || waiver.ID != "" {
					t.Fatalf("invalid decision did not stop before reading or committing: %+v %v", waiver, err)
				}
				return
			}
			if err != nil || waiver.Status != outcome || waiver.TenantID != "tenant" || waiver.PolicyID != "policy" ||
				waiver.EvidenceID != "evidence" || waiver.DecidedBy != "principal" || waiver.DecidedAt == nil || !waiver.DecidedAt.Equal(now) {
				t.Fatalf("decision lost immutable source or reviewer: %+v %v", waiver, err)
			}
		})
	}
}

func TestPostgresGateEvidenceReadsUseStoredPolicyAndApprovalScope(t *testing.T) {
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	evidence := Evidence{TenantID: "tenant", ExamID: "exam", ReleaseID: "release", Phase: EvidencePublish, CreatedBy: "manager", CreatedAt: now,
		Evaluation: Evaluation{Policy: Policy{ID: "policy", Version: "school-v2"}, BaseGate: scorerelease.Gate{Version: "A18", Passed: false, Blocking: []scorerelease.GateIssue{{Code: "question_coverage_incomplete", Blocking: true, Count: 2}}}}}
	evidence.Hash = evidenceHash(evidence)
	payload, err := json.Marshal(evidence.Evaluation)
	if err != nil {
		t.Fatal(err)
	}
	row := []driver.Value{"evidence", "tenant", "exam", "release", EvidencePublish, payload, evidence.Hash, "manager", now}
	policyConfig := []byte(`{"require_warning_acknowledgement":true,"waivable_warning_codes":["quality_warning"]}`)
	db := openGateSQLScript(t, []gateSQLStep{
		{kind: "query", query: "INSERT INTO release_gate_evidence", args: []driver.Value{"tenant", "exam", "release", "policy", EvidencePublish, string(payload), evidence.Hash, "manager", now}, rows: [][]driver.Value{row}},
		{kind: "query", query: "FROM release_gate_evidence WHERE tenant_id = $1 AND id = $2::uuid", args: []driver.Value{"tenant", "evidence"}, rows: [][]driver.Value{row}},
		{kind: "query", query: "FROM release_gate_policy WHERE tenant_id = $1 AND exam_id = $2::uuid AND status = 'active'", args: []driver.Value{"tenant", "exam"}, rows: [][]driver.Value{{"policy", "tenant", "exam", "school-v2", PolicyActive, policyConfig, "manager", now}}},
		{kind: "query", query: "AND request.policy_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid AND decision.status = 'approved'", args: []driver.Value{"tenant", "exam", "policy"}, rows: [][]driver.Value{{"waiver", "tenant", "exam", "policy", "evidence", "quality_warning", "reviewed", "teacher", now, WaiverApproved, "principal", now, "approved"}}},
	})
	store := NewPostgresStore(db)
	saved, err := store.AppendEvidence(t.Context(), evidence)
	if err != nil || saved.ID != "evidence" || saved.Hash != evidence.Hash || saved.Evaluation.Passed || len(saved.Evaluation.BaseGate.Blocking) != 1 || saved.Evaluation.BaseGate.Blocking[0].Count != 2 {
		t.Fatalf("persisted evidence lost its blocked payload: %+v %v", saved, err)
	}
	reloaded, err := store.GetEvidence(t.Context(), "tenant", saved.ID)
	if err != nil || !reflect.DeepEqual(saved, reloaded) {
		t.Fatalf("reloaded evidence differs from saved snapshot: %+v %v", reloaded, err)
	}
	policy, err := store.ActivePolicy(t.Context(), "tenant", "exam")
	if err != nil || !policy.RequireWarningAcknowledgement || policy.ID != "policy" || !reflect.DeepEqual(policy.WaivableWarningCodes, []string{"quality_warning"}) {
		t.Fatalf("active policy lost its warning rules: %+v %v", policy, err)
	}
	approved, err := store.ApprovedWaivers(t.Context(), "tenant", "exam", policy.ID)
	if err != nil || len(approved) != 1 || approved[0].PolicyID != policy.ID || approved[0].Status != WaiverApproved {
		t.Fatalf("approved waiver query lost the active policy scope: %+v %v", approved, err)
	}
}

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
	steps []gateSQLStep
	next  int
}

func (s *gateSQLScript) Connect(context.Context) (driver.Conn, error) {
	return gateSQLConnection{s}, nil
}
func (s *gateSQLScript) Driver() driver.Driver            { return s }
func (s *gateSQLScript) Open(string) (driver.Conn, error) { return gateSQLConnection{s}, nil }

func (s *gateSQLScript) take(kind, query string, args []driver.NamedValue) (gateSQLStep, error) {
	if s.next >= len(s.steps) {
		return gateSQLStep{}, fmt.Errorf("unexpected %s: %s", kind, query)
	}
	step := s.steps[s.next]
	s.next++
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		values[i] = arg.Value
	}
	if step.kind != kind || !strings.Contains(strings.Join(strings.Fields(query), " "), step.query) ||
		len(values) != len(step.args) || len(values) > 0 && !reflect.DeepEqual(values, step.args) {
		return gateSQLStep{}, fmt.Errorf("step %d: got %s %q args=%v, want %s %q args=%v", s.next, kind, query, values, step.kind, step.query, step.args)
	}
	return step, step.err
}

type gateSQLConnection struct{ script *gateSQLScript }

func (gateSQLConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (gateSQLConnection) Close() error { return nil }
func (c gateSQLConnection) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c gateSQLConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	_, err := c.script.take("begin", "", nil)
	return gateSQLTransaction(c), err
}
func (c gateSQLConnection) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c gateSQLConnection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
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

type gateSQLTransaction struct{ script *gateSQLScript }

func (tx gateSQLTransaction) Commit() error { _, err := tx.script.take("commit", "", nil); return err }
func (tx gateSQLTransaction) Rollback() error {
	_, err := tx.script.take("rollback", "", nil)
	return err
}

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
		if script.next != len(script.steps) {
			t.Errorf("executed %d SQL steps, want %d", script.next, len(script.steps))
		}
	})
	return db
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

package releasegate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

type publicationEvidenceRegrades struct {
	RegradeBlockerReader
	t   *testing.T
	tx  *sql.Tx
	err error
}

func (r publicationEvidenceRegrades) BlockingRegradeCountTx(_ context.Context, tx *sql.Tx, tenantID, examID string) (int, error) {
	r.t.Helper()
	if tx != r.tx || tenantID != "tenant" || examID != "exam" {
		r.t.Errorf("unexpected regrade lookup: same transaction=%v tenant=%q exam=%q", tx == r.tx, tenantID, examID)
	}
	return 2, r.err
}

func TestFinalPublishCheckKeepsRegradeEvidenceInPublicationTransaction(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	regradeFailure := errors.New("cannot read in-flight regrades")
	policyFailure := errors.New("cannot read active policy")
	waiverFailure := errors.New("cannot read approved waivers")
	writeFailure := errors.New("cannot persist publication evidence")
	const waiverQuery = "AND request.policy_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid AND decision.status = 'approved'"
	policy := Policy{
		ID: "policy", TenantID: "tenant", ExamID: "exam", Version: "school-v2", Status: PolicyActive,
		RequireWarningAcknowledgement: true, WaivableWarningCodes: []string{"quality_warning"}, CreatedBy: "principal", CreatedAt: now,
	}
	approved := []driver.Value{"waiver", "tenant", "exam", "policy", "old-evidence", "quality_warning", "reviewed", "teacher", now, WaiverApproved, "principal", now, "approved"}
	base := scorerelease.Gate{Version: "A18", Passed: true, Warnings: []scorerelease.GateIssue{{Code: "quality_warning"}}}
	want := Evidence{
		ID: "new-evidence", TenantID: "tenant", ExamID: "exam", ReleaseID: "release", Phase: EvidencePublish, CreatedBy: "manager", CreatedAt: now,
		Evaluation: Evaluation{
			Policy: policy, GeneratedAt: now, PendingWarningCodes: []string{}, AppliedWaiverIDs: []string{"waiver"},
			BaseGate: scorerelease.Gate{Version: "A18", Warnings: base.Warnings, Counts: map[string]int{"regrade_release_pending": 2},
				Blocking: []scorerelease.GateIssue{{Code: "regrade_release_pending", Message: "a regrade job has not been materialised into a successor release", Blocking: true, Count: 2, ActionRoute: "regrade"}}},
		},
	}
	want.Hash = evidenceHash(want)
	payload, err := json.Marshal(want.Evaluation)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                                      string
		regradeErr, policyErr, waiverErr, saveErr error
		wantErr                                   error
	}{
		{name: "批准的警告豁免不能消除重评阻塞", wantErr: scorerelease.ErrGateBlocked},
		{name: "重评查询失败", regradeErr: regradeFailure, wantErr: regradeFailure},
		{name: "策略查询失败", policyErr: policyFailure, wantErr: policyFailure},
		{name: "豁免查询失败", waiverErr: waiverFailure, wantErr: waiverFailure},
		{name: "证据写入失败", saveErr: writeFailure, wantErr: writeFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []gateSQLStep{{kind: "begin"}}
			if tc.regradeErr == nil {
				policyConfig := []byte(`{"require_warning_acknowledgement":true,"waivable_warning_codes":["quality_warning"]}`)
				steps = append(steps, gateSQLStep{kind: "query", query: "AND status = 'active'", args: []driver.Value{"tenant", "exam"},
					rows: [][]driver.Value{{"policy", "tenant", "exam", "school-v2", PolicyActive, policyConfig, "principal", now}}, err: tc.policyErr})
			}
			if tc.regradeErr == nil && tc.policyErr == nil {
				steps = append(steps, gateSQLStep{kind: "query", query: waiverQuery, args: []driver.Value{"tenant", "exam", "policy"},
					rows: [][]driver.Value{approved}, err: tc.waiverErr})
			}
			if tc.regradeErr == nil && tc.policyErr == nil && tc.waiverErr == nil {
				steps = append(steps, gateSQLStep{kind: "query", query: "INSERT INTO release_gate_evidence",
					args: []driver.Value{"tenant", "exam", "release", "policy", EvidencePublish, string(payload), want.Hash, "manager", now},
					rows: [][]driver.Value{{want.ID, "tenant", "exam", "release", EvidencePublish, payload, want.Hash, "manager", now}}, err: tc.saveErr})
			}
			// 求值和证据写入由调用方控制事务；在这里显式回滚，验证求值器没有提前结束事务。
			db := openGateSQLScript(t, append(steps, gateSQLStep{kind: "rollback"}))
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			// 非事务访问走独立内存存储。如果求值误用它，预期的 SQL 读取或写入就会缺失。
			store := struct {
				Store
				transactionalGateStore
			}{Store: NewMemoryStore(), transactionalGateStore: NewPostgresStore(db)}
			service := NewService(store, nil).WithRegradeBlockerReader(publicationEvidenceRegrades{t: t, tx: tx, err: tc.regradeErr})
			service.now = func() time.Time { return now }
			got, err := service.finalPublishCheck(t.Context(), tx, scorerelease.Release{ID: "release", TenantID: "tenant", ExamID: "exam"}, "manager", base)
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				t.Fatalf("publication rollback: %v", rollbackErr)
			}
			wantEvidence := Evidence{}
			if tc.wantErr == scorerelease.ErrGateBlocked {
				wantEvidence = want
			}
			if !errors.Is(err, tc.wantErr) || !reflect.DeepEqual(got, wantEvidence) {
				t.Fatalf("publication evidence = %+v, %v; want %+v, %v", got, err, wantEvidence, tc.wantErr)
			}
			if !base.Passed || base.Counts != nil || len(base.Blocking) != 0 {
				t.Fatalf("base gate was mutated: %+v", base)
			}
		})
	}
}

func TestPostgresWaiverRequestAndLookupPreserveTenantScope(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	request := Waiver{TenantID: "tenant", ExamID: "exam", EvidenceID: "evidence", IssueCode: "warning", Reason: "checked", RequestedBy: "teacher", RequestedAt: now}
	row := []driver.Value{"waiver", "tenant", "exam", "", "evidence", "warning", "checked", "teacher", now, WaiverRequested, "", nil, ""}
	insert := gateSQLStep{kind: "query", query: "INSERT INTO release_gate_waiver_request",
		args: []driver.Value{"tenant", "exam", "", "evidence", "warning", "checked", "teacher", now}, rows: [][]driver.Value{row}}
	missingInsert := insert
	missingInsert.rows, missingInsert.columns = nil, len(row)
	steps := []gateSQLStep{
		insert,
		{kind: "query", query: "WHERE request.tenant_id = $1 AND request.id = $2::uuid", args: []driver.Value{"tenant", "waiver"}, rows: [][]driver.Value{row}},
		{kind: "query", query: "WHERE request.tenant_id = $1 AND request.id = $2::uuid", args: []driver.Value{"other-tenant", "waiver"}, columns: len(row)},
		missingInsert,
	}
	store := NewPostgresStore(openGateSQLScript(t, steps))
	want := request
	want.ID, want.Status = "waiver", WaiverRequested
	created, err := store.RequestWaiver(t.Context(), request)
	if err != nil || !reflect.DeepEqual(created, want) {
		t.Fatalf("new waiver = %+v, %v; want %+v", created, err, want)
	}
	read, err := store.GetWaiver(t.Context(), "tenant", created.ID)
	if err != nil || !reflect.DeepEqual(read, created) {
		t.Fatalf("reloaded waiver = %+v, %v; want %+v", read, err, created)
	}
	foreign, err := store.GetWaiver(t.Context(), "other-tenant", created.ID)
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(foreign, Waiver{}) {
		t.Fatalf("foreign waiver = %+v, %v; want no waiver and ErrNotFound", foreign, err)
	}
	missing, err := store.RequestWaiver(t.Context(), request)
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(missing, Waiver{}) {
		t.Fatalf("insert without returned row = %+v, %v; want ErrNotFound", missing, err)
	}
}

func TestPostgresGateRejectsCorruptedPolicyAndEvidence(t *testing.T) {
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC)
	store := NewPostgresStore(openGateSQLScript(t, []gateSQLStep{
		{kind: "query", query: "FROM release_gate_policy WHERE tenant_id = $1 AND exam_id = $2::uuid",
			args: []driver.Value{"tenant", "exam"}, rows: [][]driver.Value{{"policy", "tenant", "exam", "v2", PolicyActive, []byte(`{"waivable_warning_codes":false}`), "manager", now}}},
		{kind: "query", query: "FROM release_gate_evidence WHERE tenant_id = $1 AND id = $2::uuid",
			args: []driver.Value{"tenant", "evidence"}, rows: [][]driver.Value{{"evidence", "tenant", "exam", "release", EvidencePublish, []byte(`{"passed":"yes"}`), "hash", "manager", now}}},
		{kind: "query", query: "FROM release_gate_evidence WHERE tenant_id = $1 AND id = $2::uuid",
			args: []driver.Value{"other-tenant", "evidence"}, columns: 9},
	}))
	// JSON 类型不合法时交回解码错误，避免把损坏的快照当成有效门禁数据。
	assertDecodeFailure := func(err error) {
		t.Helper()
		var invalid *json.UnmarshalTypeError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v; want a JSON type error", err)
		}
	}
	policy, err := store.ActivePolicy(t.Context(), "tenant", "exam")
	assertDecodeFailure(err)
	if !reflect.DeepEqual(policy, Policy{}) {
		t.Fatalf("corrupt policy leaked partial data: %+v", policy)
	}
	evidence, err := store.GetEvidence(t.Context(), "tenant", "evidence")
	assertDecodeFailure(err)
	if !reflect.DeepEqual(evidence, Evidence{}) {
		t.Fatalf("corrupt evidence leaked partial data: %+v", evidence)
	}
	foreign, err := store.GetEvidence(t.Context(), "other-tenant", "evidence")
	if !errors.Is(err, ErrNotFound) || !reflect.DeepEqual(foreign, Evidence{}) {
		t.Fatalf("foreign evidence = %+v, %v; want ErrNotFound", foreign, err)
	}
}

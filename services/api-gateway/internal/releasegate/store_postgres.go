package releasegate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) CreatePolicy(ctx context.Context, tenantID, examID, actorID string, input CreatePolicyInput) (Policy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Policy{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// 同一考试串行切换策略，旧版停用与新版创建必须一起提交，避免出现没有生效策略的空档。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, tenantID+":"+examID+":release-gate-policy"); err != nil {
		return Policy{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE release_gate_policy SET status = 'retired' WHERE tenant_id = $1 AND exam_id = $2::uuid AND status = 'active'`, tenantID, examID); err != nil {
		return Policy{}, err
	}
	payload, err := json.Marshal(struct {
		RequireWarningAcknowledgement bool     `json:"require_warning_acknowledgement"`
		WaivableWarningCodes          []string `json:"waivable_warning_codes"`
	}{input.RequireWarningAcknowledgement, input.WaivableWarningCodes})
	if err != nil {
		return Policy{}, err
	}
	policy, err := scanPolicy(tx.QueryRowContext(ctx, `
INSERT INTO release_gate_policy (tenant_id, exam_id, version, status, policy, created_by)
VALUES ($1, $2::uuid, $3, 'active', $4::jsonb, $5::uuid)
RETURNING id::text, tenant_id::text, exam_id::text, version, status, policy, created_by::text, created_at
`, tenantID, examID, input.Version, string(payload), actorID))
	if err != nil {
		return Policy{}, err
	}
	if err := tx.Commit(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (s *PostgresStore) ActivePolicy(ctx context.Context, tenantID, examID string) (Policy, error) {
	policy, err := scanPolicy(s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, version, status, policy, created_by::text, created_at
FROM release_gate_policy WHERE tenant_id = $1 AND exam_id = $2::uuid AND status = 'active'
`, tenantID, examID))
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, ErrNotFound
	}
	return policy, err
}

func (s *PostgresStore) GetEvidence(ctx context.Context, tenantID, evidenceID string) (Evidence, error) {
	evidence, err := scanEvidence(s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, COALESCE(release_id::text, ''), phase, evaluation, evidence_hash, created_by::text, created_at
FROM release_gate_evidence WHERE tenant_id = $1 AND id = $2::uuid
`, tenantID, evidenceID))
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, ErrNotFound
	}
	return evidence, err
}

func (s *PostgresStore) AppendEvidence(ctx context.Context, evidence Evidence) (Evidence, error) {
	payload, err := json.Marshal(evidence.Evaluation)
	if err != nil {
		return Evidence{}, err
	}
	policyID := nullUUID(evidence.Evaluation.Policy.ID)
	releaseID := nullUUID(evidence.ReleaseID)
	result, err := scanEvidence(s.db.QueryRowContext(ctx, `
INSERT INTO release_gate_evidence (tenant_id, exam_id, release_id, policy_id, phase, evaluation, evidence_hash, created_by, created_at)
VALUES ($1, $2::uuid, NULLIF($3, '')::uuid, NULLIF($4, '')::uuid, $5, $6::jsonb, $7, $8::uuid, $9)
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(release_id::text, ''), phase, evaluation, evidence_hash, created_by::text, created_at
`, evidence.TenantID, evidence.ExamID, releaseID, policyID, evidence.Phase, string(payload), evidence.Hash, evidence.CreatedBy, evidence.CreatedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return Evidence{}, ErrNotFound
	}
	return result, err
}

func (s *PostgresStore) RequestWaiver(ctx context.Context, waiver Waiver) (Waiver, error) {
	result, err := scanWaiver(s.db.QueryRowContext(ctx, `
INSERT INTO release_gate_waiver_request (tenant_id, exam_id, policy_id, evidence_id, issue_code, reason, requested_by, requested_at)
VALUES ($1, $2::uuid, NULLIF($3, '')::uuid, $4::uuid, $5, $6, $7::uuid, $8)
RETURNING id::text, tenant_id::text, exam_id::text, COALESCE(policy_id::text, ''), evidence_id::text, issue_code, reason, requested_by::text, requested_at,
          'requested'::text, ''::text, NULL::timestamptz, ''::text
`, waiver.TenantID, waiver.ExamID, nullUUID(waiver.PolicyID), waiver.EvidenceID, waiver.IssueCode, waiver.Reason, waiver.RequestedBy, waiver.RequestedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return Waiver{}, ErrNotFound
	}
	return result, err
}

func (s *PostgresStore) GetWaiver(ctx context.Context, tenantID, waiverID string) (Waiver, error) {
	waiver, err := scanWaiver(s.db.QueryRowContext(ctx, waiverSelect+` WHERE request.tenant_id = $1 AND request.id = $2::uuid`, tenantID, waiverID))
	if errors.Is(err, sql.ErrNoRows) {
		return Waiver{}, ErrNotFound
	}
	return waiver, err
}

func (s *PostgresStore) DecideWaiver(ctx context.Context, tenantID, waiverID, actorID string, input DecideWaiverInput) (Waiver, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Waiver{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// A decision can only be inserted once. The unique key is the immutable
	// state transition, not a mutable status column on the request.
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM release_gate_waiver_request WHERE tenant_id = $1 AND id = $2::uuid)`, tenantID, waiverID).Scan(&exists); err != nil {
		return Waiver{}, err
	}
	if !exists {
		return Waiver{}, ErrNotFound
	}
	status := WaiverRejected
	if input.Approve {
		status = WaiverApproved
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO release_gate_waiver_decision (tenant_id, waiver_id, status, reason, decided_by)
VALUES ($1, $2::uuid, $3, $4, $5::uuid)
`, tenantID, waiverID, status, input.Reason, actorID); err != nil {
		return Waiver{}, mapUniqueDecision(err)
	}
	waiver, err := scanWaiver(tx.QueryRowContext(ctx, waiverSelect+` WHERE request.tenant_id = $1 AND request.id = $2::uuid`, tenantID, waiverID))
	if err != nil {
		return Waiver{}, err
	}
	if err := tx.Commit(); err != nil {
		return Waiver{}, err
	}
	return waiver, nil
}

func (s *PostgresStore) ApprovedWaivers(ctx context.Context, tenantID, examID, policyID string) ([]Waiver, error) {
	rows, err := s.db.QueryContext(ctx, waiverSelect+`
 WHERE request.tenant_id = $1 AND request.exam_id = $2::uuid
   AND request.policy_id IS NOT DISTINCT FROM NULLIF($3, '')::uuid
   AND decision.status = 'approved'
 ORDER BY request.requested_at, request.id
`, tenantID, examID, nullUUID(policyID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Waiver{}
	for rows.Next() {
		item, scanErr := scanWaiver(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const waiverSelect = `SELECT request.id::text, request.tenant_id::text, request.exam_id::text, COALESCE(request.policy_id::text, ''), request.evidence_id::text,
request.issue_code, request.reason, request.requested_by::text, request.requested_at,
COALESCE(decision.status, 'requested'), COALESCE(decision.decided_by::text, ''), decision.decided_at, COALESCE(decision.reason, '')
FROM release_gate_waiver_request request
LEFT JOIN release_gate_waiver_decision decision ON decision.tenant_id = request.tenant_id AND decision.waiver_id = request.id`

type scanner interface{ Scan(...any) error }

func scanPolicy(row scanner) (Policy, error) {
	var policy Policy
	var raw []byte
	err := row.Scan(&policy.ID, &policy.TenantID, &policy.ExamID, &policy.Version, &policy.Status, &raw, &policy.CreatedBy, &policy.CreatedAt)
	if err != nil {
		return Policy{}, err
	}
	var config struct {
		RequireWarningAcknowledgement bool     `json:"require_warning_acknowledgement"`
		WaivableWarningCodes          []string `json:"waivable_warning_codes"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return Policy{}, fmt.Errorf("decode release gate policy: %w", err)
	}
	policy.RequireWarningAcknowledgement, policy.WaivableWarningCodes = config.RequireWarningAcknowledgement, append([]string(nil), config.WaivableWarningCodes...)
	return policy, nil
}
func scanEvidence(row scanner) (Evidence, error) {
	var evidence Evidence
	var raw []byte
	err := row.Scan(&evidence.ID, &evidence.TenantID, &evidence.ExamID, &evidence.ReleaseID, &evidence.Phase, &raw, &evidence.Hash, &evidence.CreatedBy, &evidence.CreatedAt)
	if err != nil {
		return Evidence{}, err
	}
	if err := json.Unmarshal(raw, &evidence.Evaluation); err != nil {
		return Evidence{}, fmt.Errorf("decode release gate evidence: %w", err)
	}
	return evidence, nil
}
func scanWaiver(row scanner) (Waiver, error) {
	var waiver Waiver
	err := row.Scan(&waiver.ID, &waiver.TenantID, &waiver.ExamID, &waiver.PolicyID, &waiver.EvidenceID, &waiver.IssueCode, &waiver.Reason, &waiver.RequestedBy, &waiver.RequestedAt, &waiver.Status, &waiver.DecidedBy, &waiver.DecidedAt, &waiver.DecisionReason)
	return waiver, err
}
func nullUUID(value string) string { return strings.TrimSpace(value) }
func mapUniqueDecision(err error) error {
	if strings.Contains(err.Error(), "release_gate_waiver_decision_tenant_id_waiver_id_key") {
		return ErrEvidenceImmutable
	}
	return err
}

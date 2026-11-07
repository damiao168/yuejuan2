package auth

// 风险事实与设备记录按租户、账户读取；写入和清理顺序保持在原事务内。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *PostgresStore) LoadLoginRiskContext(ctx context.Context, request LoginRiskContextRequest) (LoginRiskContext, error) {
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	var result LoginRiskContext
	var assuranceLevel int
	err := s.db.QueryRowContext(ctx, `
WITH recent_login AS (
  SELECT user_agent_hash, ip_prefix
  FROM auth_risk_event
  WHERE tenant_id=$1::uuid
    AND user_id=$2::uuid
    AND purpose='login'
    AND occurred_at >= $6::timestamptz - interval '180 days'
  ORDER BY occurred_at DESC
  LIMIT 20
)
SELECT
  (SELECT count(*) FROM recent_login),
  COALESCE((SELECT bool_or(user_agent_hash=$4 AND $4<>'') FROM recent_login), false),
  COALESCE((SELECT bool_or(ip_prefix=$5 AND $5<>'') FROM recent_login), false),
  (SELECT count(*) FROM audit_log
   WHERE tenant_id=$1::uuid AND actor_id=$2::uuid
     AND action IN ('auth.login_failed', 'auth.login_rate_limited')
     AND created_at >= $6::timestamptz - interval '15 minutes'),
  EXISTS (
    SELECT 1 FROM auth_recovery
    WHERE tenant_id=$1::uuid AND user_id=$2::uuid
      AND used_at IS NOT NULL AND used_at >= $6::timestamptz - interval '24 hours'
  ),
  COALESCE((
    SELECT assurance_level FROM auth_trusted_device
    WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND token_hash=$3
      AND revoked_at IS NULL AND expires_at>$6::timestamptz
    LIMIT 1
  ), 0)
`, request.TenantID, request.UserID, request.DeviceTokenHash, request.UserAgentHash, request.IPPrefix, now).Scan(
		&result.PriorSuccessfulLogins, &result.KnownUserAgent, &result.KnownNetwork,
		&result.RecentFailures, &result.RecentRecovery, &assuranceLevel,
	)
	if err != nil {
		return LoginRiskContext{}, err
	}
	result.KnownDevice = assuranceLevel >= 1
	result.TrustedDevice = assuranceLevel >= 2
	return result, nil
}

func (s *PostgresStore) RecordRiskEvent(ctx context.Context, event RiskEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	if event.ReasonCodes == nil {
		event.ReasonCodes = []string{}
	}
	if event.FamilyScores == nil {
		event.FamilyScores = map[string]int{}
	}
	reasonCodes, err := json.Marshal(event.ReasonCodes)
	if err != nil {
		return err
	}
	familyScores, err := json.Marshal(event.FamilyScores)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_risk_event (
  tenant_id, user_id, session_id, purpose, risk_level, decision_action, risk_score,
  reason_codes, family_scores, evidence_quality, policy_version, user_agent_hash,
  ip_prefix, device_recognized, device_trusted, occurred_at
)
VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10,$11,$12,$13,$14,$15,$16)
`, event.TenantID, event.UserID, event.SessionID, event.Purpose, normalizeRiskLevel(event.Level), normalizeRiskAction(event.Action),
		max(0, min(event.Score, 100)), string(reasonCodes), string(familyScores), event.EvidenceQuality, event.PolicyVersion,
		event.UserAgentHash, event.IPPrefix, event.DeviceRecognized, event.DeviceTrusted, event.OccurredAt); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM auth_risk_event target
WHERE (target.tenant_id=$1::uuid AND target.user_id=$2::uuid AND target.occurred_at < $3::timestamptz - interval '180 days')
   OR target.id IN (
     SELECT id FROM auth_risk_event
     WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND purpose='login'
     ORDER BY occurred_at DESC, id DESC
     OFFSET 20
   )
`, event.TenantID, event.UserID, event.OccurredAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) StoreObservedDevice(ctx context.Context, input ObservedDeviceInput) error {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	assuranceLevel := max(1, min(input.AssuranceLevel, 3))
	var id string
	err := s.db.QueryRowContext(ctx, `
INSERT INTO auth_trusted_device (
  tenant_id, user_id, token_hash, assurance_level, trust_basis, user_agent_hash,
  last_ip_prefix, last_seen_at, expires_at, trusted_at
)
VALUES (
  $1::uuid,$2::uuid,$3,$4::smallint,$5,$6,$7,$8::timestamptz,$9::timestamptz,
  CASE WHEN $4::smallint>=2 THEN $8::timestamptz ELSE NULL END
)
ON CONFLICT (token_hash) DO UPDATE SET
  user_agent_hash=EXCLUDED.user_agent_hash,
  last_ip_prefix=EXCLUDED.last_ip_prefix,
  last_seen_at=EXCLUDED.last_seen_at,
  expires_at=GREATEST(auth_trusted_device.expires_at, EXCLUDED.expires_at),
  assurance_level=GREATEST(auth_trusted_device.assurance_level, EXCLUDED.assurance_level),
  trust_basis=CASE WHEN EXCLUDED.assurance_level>auth_trusted_device.assurance_level THEN EXCLUDED.trust_basis ELSE auth_trusted_device.trust_basis END,
  trusted_at=CASE WHEN EXCLUDED.assurance_level>=2 THEN COALESCE(auth_trusted_device.trusted_at, EXCLUDED.trusted_at) ELSE auth_trusted_device.trusted_at END,
  updated_at=EXCLUDED.last_seen_at
WHERE auth_trusted_device.tenant_id=EXCLUDED.tenant_id
  AND auth_trusted_device.user_id=EXCLUDED.user_id
  AND auth_trusted_device.revoked_at IS NULL
RETURNING id::text
`, input.TenantID, input.UserID, input.TokenHash, assuranceLevel, input.TrustBasis, input.UserAgentHash,
		input.IPPrefix, now, input.ExpiresAt).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
DELETE FROM auth_trusted_device
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND expires_at<=$3
`, input.TenantID, input.UserID, now)
	return err
}

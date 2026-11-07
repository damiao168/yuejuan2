package auth

// 会话认证、锁屏和凭据撤销沿用原查询与事务边界，账号安全版本仍是失效依据。
import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

func (s *PostgresStore) CreateSession(ctx context.Context, input CreateSessionInput) (DeviceSession, error) {
	// 会话写入时再次比较登录验证阶段的安全版本，改密/禁用后的旧请求不能复活会话。
	var session DeviceSession
	riskEvaluatedAt := input.RiskEvaluatedAt
	if riskEvaluatedAt.IsZero() {
		riskEvaluatedAt = time.Now().UTC()
	}
	err := s.db.QueryRowContext(ctx, `
INSERT INTO auth_session (
  tenant_id, user_id, token_hash, session_type, device_id, device_name,
  user_agent_hash, ip_prefix, expires_at, last_seen_at, reauthenticated_at, security_epoch, auth_method, auth_level,
	  risk_level, risk_action, risk_score, risk_evaluated_at, risk_policy_version, risk_evidence_quality
)
SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, now(), now(), security_epoch,
       CASE WHEN $17 IN ('password', 'wechat') THEN $17 ELSE 'password' END, 1,
       $11, $12, $13, $14, $15, $16
FROM app_user
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='active' AND deleted_at IS NULL
  AND ($10::bigint <= 0 OR security_epoch=$10::bigint)
RETURNING id::text, session_type, device_name, created_at, last_seen_at, expires_at
`, input.TenantID, input.UserID, input.TokenHash, input.SessionType, input.DeviceID,
		input.DeviceName, input.UserAgentHash, input.IPPrefix, input.ExpiresAt, input.SecurityEpoch,
		normalizeRiskLevel(input.RiskLevel), normalizeRiskAction(input.RiskAction), max(0, min(input.RiskScore, 100)),
		riskEvaluatedAt, normalizeRiskPolicyVersion(input.RiskPolicyVersion), normalizeRiskEvidenceQuality(input.RiskEvidenceQuality), input.AuthMethod,
	).Scan(&session.ID, &session.SessionType, &session.DeviceName, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceSession{}, ErrInvalidCredentials
	}
	session.Current = true
	return session, err
}

func (s *PostgresStore) FindUserBySession(ctx context.Context, tokenHash string, now time.Time) (User, error) {
	return s.findUserBySession(ctx, tokenHash, now, false)
}

func (s *PostgresStore) FindUserBySessionForReauthentication(ctx context.Context, tokenHash string, now time.Time) (User, error) {
	return s.findUserBySession(ctx, tokenHash, now, true)
}

func (s *PostgresStore) findUserBySession(ctx context.Context, tokenHash string, now time.Time, allowLocked bool) (User, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT
  u.id::text,
  u.tenant_id::text,
  t.code,
  u.username,
  u.display_name,
	u.status,
	u.must_change_password,
	s.session_type,
	s.auth_level,
	s.reauthenticated_at,
	s.risk_level,
	s.risk_action,
	s.risk_policy_version,
  COALESCE(array_agg(DISTINCT r.code) FILTER (WHERE r.code IS NOT NULL), '{}') AS roles,
  COALESCE(array_agg(DISTINCT p.code) FILTER (WHERE p.code IS NOT NULL), '{}') AS permissions,
  COALESCE(jsonb_object_agg(r.code, ur.data_scope) FILTER (WHERE r.code IS NOT NULL), '{}') AS data_scope
FROM auth_session s
JOIN app_user u ON u.tenant_id = s.tenant_id AND u.id = s.user_id
JOIN tenant t ON t.id = u.tenant_id
LEFT JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
LEFT JOIN role r ON r.tenant_id = u.tenant_id AND r.id = ur.role_id AND r.deleted_at IS NULL
LEFT JOIN role_permission rp ON rp.tenant_id = u.tenant_id AND rp.role_id = r.id AND rp.deleted_at IS NULL
LEFT JOIN permission p ON p.tenant_id = u.tenant_id AND p.id = rp.permission_id AND p.deleted_at IS NULL
WHERE s.token_hash = $1
  AND s.expires_at > $2
  AND s.revoked_at IS NULL
  AND t.status = 'active'
  AND t.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND s.security_epoch = u.security_epoch
	AND ($3 OR s.locked_at IS NULL)
GROUP BY u.id, t.code, s.session_type, s.auth_level, s.reauthenticated_at, s.risk_level, s.risk_action, s.risk_policy_version
`, tokenHash, now, allowLocked)

	var user User
	var roles []string
	var permissions []string
	var dataScopeRaw []byte
	if err := row.Scan(&user.ID, &user.TenantID, &user.TenantCode, &user.Username, &user.DisplayName, &user.Status, &user.MustChangePassword, &user.CurrentSessionType, &user.CurrentAuthLevel, &user.ReauthenticatedAt, &user.CurrentRiskLevel, &user.CurrentRiskAction, &user.RiskPolicyVersion, pqArray(&roles), pqArray(&permissions), &dataScopeRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUnauthenticated
		}
		return User{}, err
	}
	user.Roles = roles
	user.Permissions = permissions
	user.DataScope = map[string]any{}
	_ = json.Unmarshal(dataScopeRaw, &user.DataScope)
	if user.Status != "active" {
		return User{}, ErrUnauthenticated
	}
	// 活跃时间最多每五分钟写一次；刷新失败记录日志，不把已成功验证的身份改成失败。
	cutoff := now.Add(-5 * time.Minute)
	if _, err := s.db.ExecContext(ctx, `
UPDATE auth_session
SET last_seen_at = $2, updated_at = $2
WHERE token_hash = $1
  AND last_seen_at < $3
`, tokenHash, now, cutoff); err != nil {
		sessionTouchFailureLogger.Warn(ctx, "auth_session_touch_failed", map[string]any{"error": err.Error()})
	}
	return user, nil
}

func (s *PostgresStore) LockSession(ctx context.Context, tenantID string, userID string, tokenHash string, now time.Time) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_session s
SET locked_at = COALESCE(locked_at, $4), updated_at = $4
FROM app_user u, tenant t
WHERE s.tenant_id = $1::uuid
  AND s.user_id = $2::uuid
  AND s.token_hash = $3
  AND s.session_type = 'public_device'
  AND s.revoked_at IS NULL
  AND s.expires_at > $4
  AND u.tenant_id = s.tenant_id
  AND u.id = s.user_id
  AND u.status = 'active'
  AND u.deleted_at IS NULL
  AND u.security_epoch = s.security_epoch
  AND t.id = u.tenant_id
  AND t.status = 'active'
  AND t.deleted_at IS NULL
`, tenantID, userID, tokenHash, now)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) MarkSessionReauthenticated(ctx context.Context, tenantID string, userID string, tokenHash string, startedAt time.Time, now time.Time) (bool, error) {
	// startedAt 限制可解除的锁屏时间；在途验证不能撤销它开始后发生的新锁屏。
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_session s
SET reauthenticated_at = $4, locked_at = NULL, last_seen_at = $4, updated_at = $4
FROM app_user u, tenant t
WHERE s.tenant_id = $1::uuid
  AND s.user_id = $2::uuid
  AND s.token_hash = $3
  AND s.revoked_at IS NULL
  AND s.expires_at > $4
  AND (s.locked_at IS NULL OR s.locked_at <= $5)
  AND u.tenant_id = s.tenant_id
  AND u.id = s.user_id
  AND u.status = 'active'
  AND u.deleted_at IS NULL
  AND u.security_epoch = s.security_epoch
  AND t.id = u.tenant_id
  AND t.status = 'active'
  AND t.deleted_at IS NULL
`, tenantID, userID, tokenHash, now, startedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) DeleteSession(ctx context.Context, tokenHash string, reason string) error {
	_, err := s.db.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at = now(), revoke_reason = NULLIF($2, ''), updated_at = now()
WHERE token_hash = $1 AND revoked_at IS NULL
`, tokenHash, reason)
	return err
}

func (s *PostgresStore) ListSessions(ctx context.Context, tenantID string, userID string, currentTokenHash string, now time.Time) ([]DeviceSession, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, session_type, device_name, created_at, last_seen_at, expires_at,
       token_hash = $4
FROM auth_session
WHERE tenant_id = $1
  AND user_id = $2
  AND revoked_at IS NULL
  AND expires_at > $3
ORDER BY last_seen_at DESC, created_at DESC
`, tenantID, userID, now, currentTokenHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceSession{}
	for rows.Next() {
		var session DeviceSession
		if err := rows.Scan(&session.ID, &session.SessionType, &session.DeviceName, &session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &session.Current); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *PostgresStore) RevokeSession(ctx context.Context, tenantID string, userID string, sessionID string, reason string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at = now(), revoke_reason = NULLIF($4, ''), updated_at = now()
WHERE tenant_id = $1
  AND user_id = $2
  AND id = $3
  AND revoked_at IS NULL
`, tenantID, userID, sessionID, reason)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) RevokeAllSessions(ctx context.Context, tenantID string, userID string, reason string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	revoked, err := revokeAllSessionsTx(ctx, tx, tenantID, userID, reason)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revoked, nil
}

// 调用方决定是否需要账户管理校验；撤销会话与受信设备使用同一事务，失败全部回滚。
func revokeAllSessionsTx(ctx context.Context, tx *sql.Tx, tenantID, userID, reason string) (int, error) {
	result, err := tx.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at = now(), revoke_reason = NULLIF($3, ''), updated_at = now()
WHERE tenant_id = $1
  AND user_id = $2
  AND revoked_at IS NULL
`, tenantID, userID, reason)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_trusted_device
SET revoked_at=now(), updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, tenantID, userID); err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *PostgresStore) UpdatePasswordAndRevokeSessions(ctx context.Context, tenantID, userID, expectedPasswordHash, newPasswordHash string) (bool, int, error) {
	// 新密码、安全版本及会话/设备撤销同事务，任一步失败都不留下部分改密状态。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE app_user
SET password_hash=$4, must_change_password=false, password_changed_at=now(), security_epoch=security_epoch+1, updated_at=now()
WHERE tenant_id=$1 AND id=$2 AND password_hash=$3 AND status='active' AND deleted_at IS NULL
`, tenantID, userID, expectedPasswordHash, newPasswordHash)
	if err != nil {
		return false, 0, err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return false, 0, err
	}
	result, err = tx.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at=now(), revoke_reason='password_changed', updated_at=now()
WHERE tenant_id=$1 AND user_id=$2 AND revoked_at IS NULL
`, tenantID, userID)
	if err != nil {
		return false, 0, err
	}
	revoked, err := result.RowsAffected()
	if err != nil {
		return false, 0, err
	}
	if _, err = tx.ExecContext(ctx, `
UPDATE auth_trusted_device
SET revoked_at=now(), updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, tenantID, userID); err != nil {
		return false, 0, err
	}
	if err = tx.Commit(); err != nil {
		return false, 0, err
	}
	return true, int(revoked), nil
}

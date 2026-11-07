package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) CreateRecovery(ctx context.Context, actor User, actorScope AccessScope, userID, tokenHash string, expiresAt time.Time) (RecoveryPreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryPreview{}, err
	}
	defer tx.Rollback()
	var targetID, displayName, tenantCode, schoolID, phone string
	var securityEpoch int64
	err = tx.QueryRowContext(ctx, `
SELECT u.id::text, u.display_name, t.code, COALESCE(u.school_id::text, ''), COALESCE(u.phone_normalized, ''), u.security_epoch
FROM app_user u
JOIN tenant t ON t.id=u.tenant_id
WHERE u.tenant_id=$1::uuid AND u.id::text=$2 AND u.status='active' AND u.deleted_at IS NULL
FOR UPDATE OF u
`, actor.TenantID, userID).Scan(&targetID, &displayName, &tenantCode, &schoolID, &phone, &securityEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryPreview{}, ErrManagedUserNotFound
	}
	if err != nil {
		return RecoveryPreview{}, err
	}
	if targetID == actor.ID {
		return RecoveryPreview{}, ErrUserStatusForbidden
	}
	rows, err := tx.QueryContext(ctx, `
SELECT r.code
FROM user_role ur
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
WHERE ur.tenant_id=$1::uuid AND ur.user_id=$2::uuid AND ur.deleted_at IS NULL
`, actor.TenantID, targetID)
	if err != nil {
		return RecoveryPreview{}, err
	}
	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			return RecoveryPreview{}, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return RecoveryPreview{}, err
	}
	if err := rows.Close(); err != nil {
		return RecoveryPreview{}, err
	}
	if !CanManageUserRoles(actor, roles) {
		return RecoveryPreview{}, ErrUserStatusForbidden
	}
	// 恢复令牌可直接修改目标凭据；外校教学可见范围不能用于发放令牌。
	if !actorScope.TenantWide && !actorScope.managesSchool(schoolID) {
		return RecoveryPreview{}, ErrOrganizationScope
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_recovery SET used_at=now(), updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL
`, actor.TenantID, targetID); err != nil {
		return RecoveryPreview{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_recovery (tenant_id, user_id, channel, token_hash, expires_at, created_by, security_epoch)
VALUES ($1::uuid, $2::uuid, 'admin_assisted', $3, $4, $5::uuid, $6)
`, actor.TenantID, targetID, tokenHash, expiresAt, actor.ID, securityEpoch); err != nil {
		return RecoveryPreview{}, err
	}
	if err := tx.Commit(); err != nil {
		return RecoveryPreview{}, err
	}
	return RecoveryPreview{DisplayName: displayName, TenantCode: tenantCode, PhoneMasked: MaskPhone(phone), ExpiresAt: expiresAt}, nil
}

func (s *PostgresStore) FindRecovery(ctx context.Context, tokenHash string, now time.Time) (RecoveryPreview, error) {
	var preview RecoveryPreview
	var phone string
	// 恢复请求尚未登录，必须从令牌归属读取目标账号要求，不能接受客户端自报角色。
	err := s.db.QueryRowContext(ctx, `
SELECT u.display_name, t.code, COALESCE(u.phone_normalized, ''), recovery.expires_at,
CASE WHEN EXISTS (
  SELECT 1 FROM user_role pur JOIN role pr ON pr.tenant_id=pur.tenant_id AND pr.id=pur.role_id AND pr.deleted_at IS NULL
  WHERE pur.tenant_id=u.tenant_id AND pur.user_id=u.id AND pur.deleted_at IS NULL AND pr.code IN ('school_admin','tenant_admin','platform_admin')
) THEN 12 ELSE 8 END
FROM auth_recovery recovery
JOIN app_user u ON u.tenant_id=recovery.tenant_id AND u.id=recovery.user_id
JOIN tenant t ON t.id=recovery.tenant_id
WHERE recovery.token_hash=$1
  AND recovery.used_at IS NULL
  AND recovery.expires_at>$2
  AND recovery.security_epoch=u.security_epoch
  AND u.status='active'
  AND u.deleted_at IS NULL
  AND t.status='active'
  AND t.deleted_at IS NULL
`, tokenHash, now).Scan(&preview.DisplayName, &preview.TenantCode, &phone, &preview.ExpiresAt, &preview.PasswordMinLength)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryPreview{}, ErrRecoveryInvalid
	}
	if err != nil {
		return RecoveryPreview{}, err
	}
	preview.PhoneMasked = MaskPhone(phone)
	return preview, nil
}

func (s *PostgresStore) CompleteRecovery(ctx context.Context, tokenHash string, passwordHash string, now time.Time) (RecoveryResult, error) {
	// 持锁校验恢复令牌和发放时的安全版本；令牌消费、改密及撤销旧登录状态一起提交。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RecoveryResult{}, err
	}
	defer tx.Rollback()
	var tenantID, userID string
	err = tx.QueryRowContext(ctx, `
SELECT recovery.tenant_id::text, recovery.user_id::text
FROM auth_recovery recovery
JOIN app_user u ON u.tenant_id=recovery.tenant_id AND u.id=recovery.user_id
JOIN tenant t ON t.id=recovery.tenant_id
WHERE recovery.token_hash=$1
  AND recovery.used_at IS NULL
  AND recovery.expires_at>$2
  AND recovery.security_epoch=u.security_epoch
  AND u.status='active'
  AND u.deleted_at IS NULL
  AND t.status='active'
  AND t.deleted_at IS NULL
FOR UPDATE OF recovery, u
`, tokenHash, now).Scan(&tenantID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return RecoveryResult{}, ErrRecoveryInvalid
	}
	if err != nil {
		return RecoveryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE app_user
SET password_hash=$3, must_change_password=false, password_changed_at=$4, security_epoch=security_epoch+1, updated_at=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='active'
`, tenantID, userID, passwordHash, now); err != nil {
		return RecoveryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_recovery SET used_at=$2, updated_at=$2 WHERE token_hash=$1 AND used_at IS NULL`, tokenHash, now); err != nil {
		return RecoveryResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at=$3, revoke_reason='credential_recovery', updated_at=$3
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
	`, tenantID, userID, now); err != nil {
		return RecoveryResult{}, err
	}
	scopedContext := WithUser(ctx, User{TenantID: tenantID})
	if _, err := tx.ExecContext(scopedContext, `
UPDATE auth_trusted_device
SET revoked_at=$3, updated_at=$3
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, tenantID, userID, now); err != nil {
		return RecoveryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RecoveryResult{}, err
	}
	return RecoveryResult{TenantID: tenantID, UserID: userID}, nil
}

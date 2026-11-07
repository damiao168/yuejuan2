package auth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

func (s *PostgresStore) FindActivation(ctx context.Context, tokenHash string, now time.Time) (ActivationPreview, error) {
	var preview ActivationPreview
	var phone string
	// 密码长度来自邀请目标的有效角色，与 PasswordMinimum 保持 8/12 的同一规则。
	err := s.db.QueryRowContext(ctx, `
SELECT u.display_name, t.code, COALESCE(u.school_id::text, ''), COALESCE(u.phone_normalized, ''), activation.expires_at,
CASE WHEN EXISTS (
  SELECT 1 FROM user_role pur JOIN role pr ON pr.tenant_id=pur.tenant_id AND pr.id=pur.role_id AND pr.deleted_at IS NULL
  WHERE pur.tenant_id=u.tenant_id AND pur.user_id=u.id AND pur.deleted_at IS NULL AND pr.code IN ('school_admin','tenant_admin','platform_admin')
) THEN 12 ELSE 8 END
FROM auth_activation activation
JOIN app_user u ON u.tenant_id=activation.tenant_id AND u.id=activation.user_id
JOIN tenant t ON t.id=activation.tenant_id
WHERE activation.token_hash=$1
  AND activation.used_at IS NULL
  AND activation.expires_at>$2
  AND u.status='invited'
  AND u.deleted_at IS NULL
  AND t.status='active'
  AND t.deleted_at IS NULL
`, tokenHash, now).Scan(&preview.DisplayName, &preview.TenantCode, &preview.SchoolID, &phone, &preview.ExpiresAt, &preview.PasswordMinLength)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationPreview{}, ErrActivationInvalid
	}
	if err != nil {
		return ActivationPreview{}, err
	}
	preview.PhoneMasked = MaskPhone(phone)
	return preview, nil
}

func (s *PostgresStore) CreateActivation(ctx context.Context, actor User, actorScope AccessScope, userID, tokenHash string, expiresAt time.Time) (ActivationPreview, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivationPreview{}, err
	}
	defer tx.Rollback()
	var targetID, displayName, tenantCode, schoolID, phone string
	err = tx.QueryRowContext(ctx, `
SELECT u.id::text, u.display_name, t.code, COALESCE(u.school_id::text, ''), COALESCE(u.phone_normalized, '')
FROM app_user u
JOIN tenant t ON t.id=u.tenant_id
WHERE u.tenant_id=$1::uuid AND u.id::text=$2 AND u.status='invited' AND u.deleted_at IS NULL
FOR UPDATE OF u
`, actor.TenantID, userID).Scan(&targetID, &displayName, &tenantCode, &schoolID, &phone)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationPreview{}, ErrManagedUserNotFound
	}
	if err != nil {
		return ActivationPreview{}, err
	}
	if targetID == actor.ID {
		return ActivationPreview{}, ErrUserStatusForbidden
	}
	rows, err := tx.QueryContext(ctx, `
SELECT r.code
FROM user_role ur
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
WHERE ur.tenant_id=$1::uuid AND ur.user_id=$2::uuid AND ur.deleted_at IS NULL
`, actor.TenantID, targetID)
	if err != nil {
		return ActivationPreview{}, err
	}
	roles := []string{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			return ActivationPreview{}, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return ActivationPreview{}, err
	}
	if err := rows.Close(); err != nil {
		return ActivationPreview{}, err
	}
	if !CanManageUserRoles(actor, roles) {
		return ActivationPreview{}, ErrUserStatusForbidden
	}
	if !actorScope.TenantWide && !actorScope.managesSchool(schoolID) {
		return ActivationPreview{}, ErrOrganizationScope
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_activation SET used_at=now(), updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL
`, actor.TenantID, targetID); err != nil {
		return ActivationPreview{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_activation (tenant_id, user_id, token_hash, expires_at)
VALUES ($1::uuid, $2::uuid, $3, $4)
`, actor.TenantID, targetID, tokenHash, expiresAt); err != nil {
		return ActivationPreview{}, err
	}
	if err := tx.Commit(); err != nil {
		return ActivationPreview{}, err
	}
	return ActivationPreview{DisplayName: displayName, TenantCode: tenantCode, SchoolID: schoolID, PhoneMasked: MaskPhone(phone), ExpiresAt: expiresAt}, nil
}

func (s *PostgresStore) ActivateUser(ctx context.Context, tokenHash string, passwordHash string, now time.Time) (ActivationResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ActivationResult{}, err
	}
	defer tx.Rollback()
	var tenantID, userID string
	err = tx.QueryRowContext(ctx, `
SELECT activation.tenant_id::text, activation.user_id::text
FROM auth_activation activation
JOIN app_user u ON u.tenant_id=activation.tenant_id AND u.id=activation.user_id
JOIN tenant t ON t.id=activation.tenant_id
WHERE activation.token_hash=$1
  AND activation.used_at IS NULL
  AND activation.expires_at>$2
  AND u.status='invited'
  AND u.deleted_at IS NULL
  AND t.status='active'
  AND t.deleted_at IS NULL
FOR UPDATE OF activation, u
`, tokenHash, now).Scan(&tenantID, &userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ActivationResult{}, ErrActivationInvalid
	}
	if err != nil {
		return ActivationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE app_user
SET status='active', password_hash=$3, must_change_password=false, activated_at=$4, password_changed_at=$4,
    security_epoch=security_epoch+1, updated_at=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='invited'
`, tenantID, userID, passwordHash, now); err != nil {
		return ActivationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_activation SET used_at=$2, updated_at=$2 WHERE token_hash=$1 AND used_at IS NULL
`, tokenHash, now); err != nil {
		return ActivationResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at=$3, revoke_reason='account_activated', updated_at=$3
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, tenantID, userID, now); err != nil {
		return ActivationResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ActivationResult{}, err
	}
	return ActivationResult{TenantID: tenantID, UserID: userID}, nil
}

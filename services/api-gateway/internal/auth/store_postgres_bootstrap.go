package auth

import (
	"context"
)

func (s *PostgresStore) ActiveAdminExists(ctx context.Context, tenantCode string, roleCode string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM app_user u
  JOIN tenant t ON t.id = u.tenant_id
  JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
  JOIN role r ON r.tenant_id = u.tenant_id AND r.id = ur.role_id AND r.deleted_at IS NULL
  WHERE t.code = $1
    AND r.code = $2
    AND u.status = 'active'
    AND u.deleted_at IS NULL
)
`, tenantCode, roleCode)
	var exists bool
	if err := row.Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (s *PostgresStore) UpsertBootstrapAdmin(ctx context.Context, input BootstrapAdminInput, passwordHash string) (BootstrapAdminResult, error) {
	if input.TenantCode != "platform" || input.RoleCode != "platform_admin" {
		return BootstrapAdminResult{}, ErrInvalidBootstrapInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	defer tx.Rollback()

	var tenantID string
	if err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM tenant
WHERE code = $1 AND deleted_at IS NULL
`, input.TenantCode).Scan(&tenantID); err != nil {
		return BootstrapAdminResult{}, err
	}

	var roleID string
	if err := tx.QueryRowContext(ctx, `
SELECT id::text
FROM role
WHERE tenant_id::text = $1 AND code = $2 AND deleted_at IS NULL
`, tenantID, input.RoleCode).Scan(&roleID); err != nil {
		return BootstrapAdminResult{}, err
	}

	var userID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO app_user (tenant_id, username, display_name, password_hash, status, deleted_at)
VALUES ($1::uuid, $2, $3, $4, 'active', NULL)
ON CONFLICT (tenant_id, username) DO UPDATE
SET display_name = EXCLUDED.display_name,
    password_hash = EXCLUDED.password_hash,
    status = 'active',
    deleted_at = NULL,
    updated_at = now()
RETURNING id::text
`, tenantID, input.Username, input.DisplayName, passwordHash).Scan(&userID); err != nil {
		return BootstrapAdminResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_role (tenant_id, user_id, role_id, data_scope, deleted_at)
VALUES ($1::uuid, $2::uuid, $3::uuid, jsonb_build_object('scope', 'platform'), NULL)
ON CONFLICT (tenant_id, user_id, role_id) DO UPDATE
SET data_scope = EXCLUDED.data_scope,
    deleted_at = NULL,
    updated_at = now()
`, tenantID, userID, roleID); err != nil {
		return BootstrapAdminResult{}, err
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO audit_log (tenant_id, actor_id, action, target_type, target_id, after_value, reason)
VALUES ($1::uuid, NULL, 'auth.bootstrap_admin_created', 'user', $2::uuid, $3::jsonb, 'bootstrap initial admin')
`, tenantID, userID, auditJSON(map[string]any{
		"username":    input.Username,
		"role_code":   input.RoleCode,
		"tenant_code": input.TenantCode,
	})); err != nil {
		return BootstrapAdminResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return BootstrapAdminResult{}, err
	}
	return BootstrapAdminResult{
		TenantID:   tenantID,
		TenantCode: input.TenantCode,
		UserID:     userID,
		Username:   input.Username,
		RoleCode:   input.RoleCode,
	}, nil
}

func (s *PostgresStore) ProvisionStory060User(ctx context.Context, username, displayName, roleCode, passwordHash string) error {
	expectedScope := map[string]string{"school_admin": "school", "subjective_grading_worker": "service"}
	scope, ok := expectedScope[roleCode]
	if !ok {
		return ErrStory060ProvisioningInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var tenantID, roleID, actualScope string
	if err := tx.QueryRowContext(ctx, `SELECT id::text FROM tenant WHERE code='platform' AND deleted_at IS NULL`).Scan(&tenantID); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT id::text,scope_type FROM role WHERE tenant_id=$1::uuid AND code=$2 AND deleted_at IS NULL`, tenantID, roleCode).Scan(&roleID, &actualScope); err != nil {
		return err
	}
	if actualScope != scope {
		return ErrStory060ProvisioningInput
	}
	var userID string
	if err := tx.QueryRowContext(ctx, `
INSERT INTO app_user (tenant_id,username,display_name,password_hash,status,deleted_at)
VALUES ($1::uuid,$2,$3,$4,'active',NULL)
ON CONFLICT (tenant_id,username) DO UPDATE SET display_name=EXCLUDED.display_name,password_hash=EXCLUDED.password_hash,status='active',deleted_at=NULL,updated_at=now()
RETURNING id::text
`, tenantID, username, displayName, passwordHash).Scan(&userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO user_role (tenant_id,user_id,role_id,data_scope,deleted_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,jsonb_build_object('scope',$4::text),NULL)
ON CONFLICT (tenant_id,user_id,role_id) DO UPDATE SET data_scope=EXCLUDED.data_scope,deleted_at=NULL,updated_at=now()
`, tenantID, userID, roleID, scope); err != nil {
		return err
	}
	return tx.Commit()
}

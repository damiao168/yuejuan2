package auth

// 账号管理仍先核对角色和实际学校管理范围；状态变更、凭据失效与最后一名管理员检查沿用原事务。
import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"
)

func (s *PostgresStore) ListManagedUsers(ctx context.Context, tenantID string, filter ManagedUserFilter) ([]ManagedUser, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT u.id::text, u.username, u.display_name, u.status,
       COALESCE(array_agg(DISTINCT r.code) FILTER (WHERE r.code IS NOT NULL), '{}'),
	   COALESCE(u.school_id::text, ''), COALESCE(u.phone_normalized, ''), COALESCE(u.employee_no, ''),
	   u.activated_at, u.last_login_at,
       u.created_at,
       u.teaching_subjects, u.must_change_password
FROM app_user u
LEFT JOIN user_role ur ON ur.tenant_id = u.tenant_id AND ur.user_id = u.id AND ur.deleted_at IS NULL
LEFT JOIN role r ON r.tenant_id = u.tenant_id AND r.id = ur.role_id AND r.deleted_at IS NULL
WHERE u.tenant_id = $1::uuid AND u.deleted_at IS NULL
  AND ($2 = '' OR u.id::text = $2)
  AND ($3 = '' OR u.username ILIKE '%' || $3 || '%' OR u.display_name ILIKE '%' || $3 || '%'
       OR COALESCE(u.phone_normalized, '') ILIKE '%' || $3 || '%'
       OR COALESCE(u.employee_no, '') ILIKE '%' || $3 || '%')
  AND ($4 = '' OR EXISTS (
    SELECT 1 FROM user_role fur JOIN role fr ON fr.tenant_id=fur.tenant_id AND fr.id=fur.role_id AND fr.deleted_at IS NULL
    WHERE fur.tenant_id=u.tenant_id AND fur.user_id=u.id AND fur.deleted_at IS NULL AND fr.code=$4
  ))
  AND ($6 = '' OR u.created_at < $5 OR (u.created_at = $5 AND u.id::text < $6))
  AND (NOT $8 OR u.school_id::text = ANY(string_to_array($9, ',')))
  AND ($10 = '' OR u.status = $10)
GROUP BY u.id
ORDER BY u.created_at DESC, u.id::text DESC
LIMIT NULLIF($7, 0)
`, tenantID, filter.UserID, filter.Query, filter.Role, filter.CursorCreatedAt, filter.CursorID, filter.Limit, filter.RestrictSchools, strings.Join(filter.SchoolIDs, ","), filter.Status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManagedUser{}
	for rows.Next() {
		var user ManagedUser
		var phone string
		if err := rows.Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, pqArray(&user.Roles), &user.SchoolID, &phone, &user.EmployeeNo, &user.ActivatedAt, &user.LastLoginAt, &user.CreatedAt, pqArray(&user.TeachingSubjects), &user.MustChangePassword); err != nil {
			return nil, err
		}
		user.PhoneMasked = MaskPhone(phone)
		out = append(out, user)
	}
	return out, rows.Err()
}

func (s *PostgresStore) ListAssignableRoles(ctx context.Context, actor User) ([]AssignableRole, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT code, name, scope_type, COALESCE(description, '')
FROM role
WHERE tenant_id = $1::uuid
  AND deleted_at IS NULL
  AND code NOT IN ('platform_admin', 'tenant_admin')
ORDER BY name, code
`, actor.TenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AssignableRole{}
	for rows.Next() {
		var role AssignableRole
		if err := rows.Scan(&role.Code, &role.Name, &role.ScopeType, &role.Description); err != nil {
			return nil, err
		}
		if policy := RolePolicy(role.Code); CanAssignManagedRole(actor, role.Code) && role.ScopeType == policy.CanonicalScope {
			out = append(out, role)
		}
	}
	return out, rows.Err()
}

// CreateManagedUser 在同一事务内创建账户、职责、任教学科和可选激活记录，任一步失败都回滚。
func (s *PostgresStore) CreateManagedUser(ctx context.Context, actor User, actorScope AccessScope, input CreateManagedUserInput, passwordHash string) (ManagedUser, error) {
	roles := managedRoleCodes(input)
	subjects, err := NormalizeTeachingSubjects(input.TeachingSubjects)
	if err != nil {
		return ManagedUser{}, err
	}
	input.TeachingSubjects = subjects
	schoolID, err := managedUserCreationSchool(actor, actorScope, input, roles)
	if err != nil {
		return ManagedUser{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedUser{}, err
	}
	defer tx.Rollback()
	roleIDs, err := resolveManagedUserCreationRoles(ctx, tx, actor.TenantID, roles)
	if err != nil {
		return ManagedUser{}, err
	}
	if err := validateManagedUserCreationOrganization(ctx, tx, actor.TenantID, schoolID); err != nil {
		return ManagedUser{}, err
	}
	user, err := insertManagedUser(ctx, tx, actor.TenantID, schoolID, input, passwordHash)
	if err != nil {
		return ManagedUser{}, err
	}
	if err := insertManagedUserCreationRoles(ctx, tx, actor.TenantID, user.ID, schoolID, roles, roleIDs); err != nil {
		return ManagedUser{}, err
	}
	if input.ActivationTokenHash != "" {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO auth_activation (tenant_id, user_id, token_hash, expires_at)
VALUES ($1::uuid, $2::uuid, $3, $4)
`, actor.TenantID, user.ID, input.ActivationTokenHash, input.ActivationExpiresAt); err != nil {
			return ManagedUser{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ManagedUser{}, err
	}
	user.Roles = roles
	user.TeachingSubjects = append([]string{}, input.TeachingSubjects...)
	user.PhoneMasked = MaskPhone(input.Phone)
	user.EmployeeNo = input.EmployeeNo
	return user, nil
}

func managedUserCreationSchool(actor User, actorScope AccessScope, input CreateManagedUserInput, roles []string) (string, error) {
	if err := validateManagedAccess(actor, roles); err != nil {
		return "", err
	}
	schoolID := strings.TrimSpace(input.SchoolID)
	// 只从实际管理范围推断学校；兼任外校教师不能改变默认学校或授予账号管理权限。
	managedSchools := actorScope.ManagedSchoolIDs()
	if schoolID == "" && HasRole(actor, "school_admin") && len(managedSchools) == 1 {
		schoolID = managedSchools[0]
	}
	for _, code := range roles {
		if RolePolicy(code).SchoolRequired && schoolID == "" {
			return "", ErrInvalidRoleBinding
		}
	}
	if schoolID != "" && !actorScope.TenantWide && !actorScope.managesSchool(schoolID) {
		return "", ErrOrganizationScope
	}
	if err := validateTeachingSubjects(roles, input.TeachingSubjects); err != nil {
		return "", err
	}
	return schoolID, nil
}

func resolveManagedUserCreationRoles(ctx context.Context, tx *sql.Tx, tenantID string, roles []string) (map[string]string, error) {
	roleIDs := map[string]string{}
	for _, code := range roles {
		var roleID, roleScopeType string
		if err := tx.QueryRowContext(ctx, `
SELECT id::text, scope_type FROM role
WHERE tenant_id = $1::uuid AND code = $2 AND deleted_at IS NULL
`, tenantID, code).Scan(&roleID, &roleScopeType); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrRoleNotFound
			}
			return nil, err
		}
		if roleScopeType != RolePolicy(code).CanonicalScope {
			return nil, ErrRoleAssignment
		}
		roleIDs[code] = roleID
	}
	return roleIDs, nil
}

// 学校归属仍由管理员管理边界核对，任教学科不替代学校授权。
func validateManagedUserCreationOrganization(ctx context.Context, tx *sql.Tx, tenantID, schoolID string) error {
	if schoolID == "" {
		return nil
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM school WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL)`, tenantID, schoolID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalidRoleBinding
	}
	return nil
}

func insertManagedUser(ctx context.Context, tx *sql.Tx, tenantID, schoolID string, input CreateManagedUserInput, passwordHash string) (ManagedUser, error) {
	var user ManagedUser
	status := "active"
	var activatedAt any = time.Now().UTC()
	// 邀请账户尚未激活，激活时间和密码变更时间共用 NULL；直接创建则共用本次 UTC 时间。
	if input.ActivationTokenHash != "" {
		status = "invited"
		activatedAt = nil
	}
	err := tx.QueryRowContext(ctx, `
INSERT INTO app_user (tenant_id, school_id, username, display_name, password_hash, phone_normalized, employee_no, status, activated_at, password_changed_at, teaching_subjects, must_change_password)
VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9, $9, string_to_array($10,','), $11)
RETURNING id::text, username, display_name, status, COALESCE(school_id::text,''), activated_at, created_at, must_change_password
`, tenantID, schoolID, input.Username, input.DisplayName, passwordHash, input.Phone, input.EmployeeNo, status, activatedAt, strings.Join(input.TeachingSubjects, ","), input.MustChangePassword).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.SchoolID, &user.ActivatedAt, &user.CreatedAt, &user.MustChangePassword)
	if err != nil {
		lowerError := strings.ToLower(err.Error())
		if strings.Contains(lowerError, "uq_app_user_tenant_phone") || strings.Contains(lowerError, "uq_app_user_tenant_employee_no") {
			return ManagedUser{}, ErrIdentityExists
		}
		if strings.Contains(lowerError, "unique") || strings.Contains(lowerError, "duplicate") {
			return ManagedUser{}, ErrUsernameExists
		}
		return ManagedUser{}, err
	}
	return user, nil
}

func insertManagedUserCreationRoles(ctx context.Context, tx *sql.Tx, tenantID, userID, schoolID string, roles []string, roleIDs map[string]string) error {
	for _, code := range roles {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO user_role (tenant_id, user_id, role_id, data_scope)
VALUES ($1::uuid, $2::uuid, $3::uuid, $4::jsonb)
`, tenantID, userID, roleIDs[code], auditJSON(canonicalRoleDataScope(code, schoolID))); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresStore) UpdateManagedUserStatus(ctx context.Context, actor User, actorScope AccessScope, userID string, status string) (ManagedUser, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedUser{}, "", err
	}
	defer tx.Rollback()

	var user ManagedUser
	err = tx.QueryRowContext(ctx, `
SELECT id::text, username, display_name, status, COALESCE(school_id::text, ''),
       COALESCE(phone_normalized, ''), COALESCE(employee_no, ''), activated_at, last_login_at, created_at, must_change_password
FROM app_user
WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL
FOR UPDATE
`, actor.TenantID, userID).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.SchoolID, &user.PhoneMasked, &user.EmployeeNo, &user.ActivatedAt, &user.LastLoginAt, &user.CreatedAt, &user.MustChangePassword)
	user.PhoneMasked = MaskPhone(user.PhoneMasked)
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedUser{}, "", ErrManagedUserNotFound
	}
	if err != nil {
		return ManagedUser{}, "", err
	}
	if user.ID == actor.ID {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	user.Roles, err = loadManagedUserRolesTx(ctx, tx, actor.TenantID, user.ID)
	if err != nil {
		return ManagedUser{}, "", err
	}
	if !CanManageUserRoles(actor, user.Roles) {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if !actorScope.TenantWide && !actorScope.managesSchool(user.SchoolID) {
		return ManagedUser{}, "", ErrOrganizationScope
	}
	previousStatus := user.Status
	if previousStatus == "invited" {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if previousStatus == status {
		return user, previousStatus, nil
	}
	if status == "active" && previousStatus != "disabled" {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if status == "disabled" && slices.Contains(user.Roles, "school_admin") {
		// 同校管理员可被不同请求同时禁用；学校级锁把存量检查和禁用串行化。
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, actor.TenantID+"|"+user.SchoolID+"|school-admin-status"); err != nil {
			return ManagedUser{}, "", err
		}
		var activeAdmins int
		if err := tx.QueryRowContext(ctx, `
SELECT count(DISTINCT u.id)
FROM app_user u
JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
WHERE u.tenant_id=$1::uuid AND COALESCE(u.school_id::text, '')=$2
  AND u.status='active' AND u.deleted_at IS NULL AND r.code='school_admin'
`, actor.TenantID, user.SchoolID).Scan(&activeAdmins); err != nil {
			return ManagedUser{}, "", err
		}
		if activeAdmins <= 1 {
			return ManagedUser{}, "", ErrLastSchoolAdmin
		}
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE app_user
SET status=$3,
    security_epoch=CASE WHEN $3='disabled' THEN security_epoch+1 ELSE security_epoch END,
    updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
`, actor.TenantID, user.ID, status); err != nil {
		return ManagedUser{}, "", err
	}
	if status == "disabled" {
		if _, err := tx.ExecContext(ctx, `
UPDATE auth_session
SET revoked_at=now(), revoke_reason='user_disabled', updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, actor.TenantID, user.ID); err != nil {
			return ManagedUser{}, "", err
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE auth_trusted_device
SET revoked_at=now(), updated_at=now()
WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL
`, actor.TenantID, user.ID); err != nil {
			return ManagedUser{}, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return ManagedUser{}, "", err
	}
	user.Status = status
	return user, previousStatus, nil
}

// 角色读取使用账户状态事务；行读取或关闭失败时，调用方不能继续执行权限判断和状态写入。
func loadManagedUserRolesTx(ctx context.Context, tx *sql.Tx, tenantID, userID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT r.code
FROM user_role ur
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
WHERE ur.tenant_id=$1::uuid AND ur.user_id=$2::uuid AND ur.deleted_at IS NULL
ORDER BY r.code
`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	var roles []string
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return roles, nil
}

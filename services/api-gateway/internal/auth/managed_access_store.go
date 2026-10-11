package auth

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
)

func sameManagedAccess(roles, classes, expectedRoles, expectedClasses []string) bool {
	return slices.Equal(uniqueSortedIDs(roles), uniqueSortedIDs(expectedRoles)) && slices.Equal(uniqueSortedIDs(classes), uniqueSortedIDs(expectedClasses))
}

// UpdateManagedUserAccess 在账户锁下核对旧授权；移除最后管理员与停用操作共用学校级锁。
// 职责、班级绑定、安全版本及凭据撤销一起提交，失败不会留下半份授权。
func (s *PostgresStore) UpdateManagedUserAccess(ctx context.Context, actor User, scope AccessScope, userID string, input UpdateManagedUserAccessInput) (ManagedUser, error) {
	roles, classes := uniqueSortedIDs(input.RoleCodes), uniqueSortedIDs(input.ClassIDs)
	if err := validateManagedAccess(actor, roles, classes); err != nil {
		return ManagedUser{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedUser{}, err
	}
	defer tx.Rollback()
	user, err := loadManagedAccessUser(ctx, tx, actor.TenantID, userID)
	if err != nil {
		return ManagedUser{}, err
	}
	if err := validateManagedAccessTarget(actor, scope, user, input, roles); err != nil {
		return ManagedUser{}, err
	}
	// 无变更仍要核对旧授权和学校要求，但不撤销凭据，也不重写职责绑定。
	if sameManagedAccess(user.Roles, user.ClassIDs, roles, classes) {
		return user, nil
	}
	roleIDs, err := resolveManagedAccessBindings(ctx, tx, actor.TenantID, user.SchoolID, roles, classes)
	if err != nil {
		return ManagedUser{}, err
	}
	if err := protectLastManagedSchoolAdmin(ctx, tx, actor.TenantID, user, roles); err != nil {
		return ManagedUser{}, err
	}
	if err := writeManagedAccessBindings(ctx, tx, actor.TenantID, user, roles, classes, roleIDs); err != nil {
		return ManagedUser{}, err
	}
	if err := tx.Commit(); err != nil {
		return ManagedUser{}, err
	}
	user.Roles, user.ClassIDs = roles, classes
	return user, nil
}

func loadManagedAccessUser(ctx context.Context, tx *sql.Tx, tenantID, userID string) (ManagedUser, error) {
	var user ManagedUser
	err := tx.QueryRowContext(ctx, `SELECT u.id::text,u.username,u.display_name,u.status,COALESCE(u.school_id::text,''),COALESCE(u.phone_normalized,''),COALESCE(u.employee_no,''),u.activated_at,u.last_login_at,u.created_at,
ARRAY(SELECT r.code FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL ORDER BY r.code),
ARRAY(SELECT tc.class_id::text FROM teacher_class tc WHERE tc.tenant_id=u.tenant_id AND tc.teacher_id=u.id AND tc.deleted_at IS NULL ORDER BY tc.class_id::text)
FROM app_user u WHERE u.tenant_id=$1::uuid AND u.id::text=$2 AND u.deleted_at IS NULL FOR UPDATE OF u`, tenantID, userID).Scan(&user.ID, &user.Username, &user.DisplayName, &user.Status, &user.SchoolID, &user.PhoneMasked, &user.EmployeeNo, &user.ActivatedAt, &user.LastLoginAt, &user.CreatedAt, pqArray(&user.Roles), pqArray(&user.ClassIDs))
	if errors.Is(err, sql.ErrNoRows) {
		return ManagedUser{}, ErrManagedUserNotFound
	}
	if err != nil {
		return ManagedUser{}, err
	}
	user.PhoneMasked = MaskPhone(user.PhoneMasked)
	return user, nil
}

func validateManagedAccessTarget(actor User, scope AccessScope, user ManagedUser, input UpdateManagedUserAccessInput, roles []string) error {
	if user.ID == actor.ID || !CanManageUserRoles(actor, user.Roles) {
		return ErrUserStatusForbidden
	}
	if !scope.TenantWide && (user.SchoolID == "" || !scope.AllowsSchool(user.SchoolID)) {
		return ErrOrganizationScope
	}
	// 先拒绝过期旧值，再验证新绑定，不能让重试覆盖其他管理员已提交的授权。
	if !sameManagedAccess(user.Roles, user.ClassIDs, input.ExpectedRoles, input.ExpectedClassIDs) {
		return ErrManagedUserAccessConflict
	}
	for _, role := range roles {
		if RolePolicy(role).SchoolRequired && user.SchoolID == "" {
			return ErrInvalidRoleBinding
		}
	}
	return nil
}

func resolveManagedAccessBindings(ctx context.Context, tx *sql.Tx, tenantID, schoolID string, roles, classes []string) (map[string]string, error) {
	roleIDs := map[string]string{}
	for _, role := range roles {
		var id, scopeType string
		err := tx.QueryRowContext(ctx, `SELECT id::text,scope_type FROM role WHERE tenant_id=$1::uuid AND code=$2 AND deleted_at IS NULL`, tenantID, role).Scan(&id, &scopeType)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRoleNotFound
		}
		if err != nil {
			return nil, err
		}
		if scopeType != RolePolicy(role).CanonicalScope {
			return nil, ErrRoleAssignment
		}
		roleIDs[role] = id
	}
	if len(classes) > 0 {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM school_class WHERE tenant_id=$1::uuid AND school_id::text=$2 AND id::text=ANY(string_to_array($3,',')) AND deleted_at IS NULL`, tenantID, schoolID, strings.Join(classes, ",")).Scan(&count); err != nil {
			return nil, err
		}
		if count != len(classes) {
			return nil, ErrInvalidRoleBinding
		}
	}
	return roleIDs, nil
}

func protectLastManagedSchoolAdmin(ctx context.Context, tx *sql.Tx, tenantID string, user ManagedUser, roles []string) error {
	if user.Status != "active" || !slices.Contains(user.Roles, "school_admin") || slices.Contains(roles, "school_admin") {
		return nil
	}
	// 与停用共用学校级事务锁；锁后计数，不能同时移除最后两名启用管理员。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+"|"+user.SchoolID+"|school-admin-status"); err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(DISTINCT u.id) FROM app_user u JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL WHERE u.tenant_id=$1::uuid AND COALESCE(u.school_id::text,'')=$2 AND u.status='active' AND u.deleted_at IS NULL AND r.code='school_admin'`, tenantID, user.SchoolID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastSchoolAdmin
	}
	return nil
}

// 调用方持有账户锁并负责提交；任一步失败都由外层回滚，不能只更新授权而保留旧凭据。
func writeManagedAccessBindings(ctx context.Context, tx *sql.Tx, tenantID string, user ManagedUser, roles, classes []string, roleIDs map[string]string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE user_role SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND deleted_at IS NULL`, tenantID, user.ID); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,$4::jsonb) ON CONFLICT(tenant_id,user_id,role_id) DO UPDATE SET data_scope=EXCLUDED.data_scope,deleted_at=NULL,updated_at=now()`, tenantID, user.ID, roleIDs[role], auditJSON(canonicalRoleDataScope(role, user.SchoolID))); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE teacher_class SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND teacher_id=$2::uuid AND deleted_at IS NULL`, tenantID, user.ID); err != nil {
		return err
	}
	if len(classes) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO teacher_class(tenant_id,teacher_id,class_id) SELECT $1::uuid,$2::uuid,value::uuid FROM unnest(string_to_array($3,',')) AS value ON CONFLICT(tenant_id,teacher_id,class_id) DO UPDATE SET deleted_at=NULL,updated_at=now()`, tenantID, user.ID, strings.Join(classes, ",")); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE app_user SET security_epoch=security_epoch+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_session SET revoked_at=now(),revoke_reason='access_changed',updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE auth_trusted_device SET revoked_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, user.ID); err != nil {
		return err
	}
	return nil
}

func (s *MemoryStore) UpdateManagedUserAccess(_ context.Context, actor User, scope AccessScope, userID string, input UpdateManagedUserAccessInput) (ManagedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	roles, classes := uniqueSortedIDs(input.RoleCodes), uniqueSortedIDs(input.ClassIDs)
	if err := validateManagedAccess(actor, roles, classes); err != nil {
		return ManagedUser{}, err
	}
	for key, target := range s.users {
		if target.TenantID != actor.TenantID || target.ID != userID || target.Status == "deleted" {
			continue
		}
		schoolID, err := managedUserAuthority(actor, scope, target)
		if err != nil {
			return ManagedUser{}, err
		}
		if !sameManagedAccess(target.Roles, target.ManagedClassIDs, input.ExpectedRoles, input.ExpectedClassIDs) {
			return ManagedUser{}, ErrManagedUserAccessConflict
		}
		if _, err := s.managedUserBinding(actor, scope, CreateManagedUserInput{RoleCodes: roles, SchoolID: schoolID, ClassIDs: classes}); err != nil {
			return ManagedUser{}, err
		}
		if err := s.validateManagedAccessClassesLocked(actor.TenantID, schoolID, classes); err != nil {
			return ManagedUser{}, err
		}
		if target.Status == "active" && slices.Contains(target.Roles, "school_admin") && !slices.Contains(roles, "school_admin") {
			if err := s.validateManagedUserStatus(target, schoolID, "disabled"); err != nil {
				return ManagedUser{}, err
			}
		}
		if sameManagedAccess(target.Roles, target.ManagedClassIDs, roles, classes) {
			return managedUserFromMemory(target, schoolID), nil
		}
		target.Roles, target.ManagedClassIDs = roles, classes
		target.DataScope = managedRoleScopes(roles, schoolID)
		target.SecurityEpoch++
		s.revokeUserCredentials(target.TenantID, target.ID)
		s.users[key] = target
		return managedUserFromMemory(target, schoolID), nil
	}
	return ManagedUser{}, ErrManagedUserNotFound
}

// 调用方持有账户写锁；班级必须属于同一租户和学校，兼容边界表的两个资源名称。
func (s *MemoryStore) validateManagedAccessClassesLocked(tenantID, schoolID string, classes []string) error {
	for _, classID := range classes {
		boundary, ok := s.boundaries["class|"+classID]
		if !ok {
			boundary, ok = s.boundaries["classes|"+classID]
		}
		if !ok || boundary.TenantID != tenantID || boundary.SchoolID != schoolID {
			return ErrInvalidRoleBinding
		}
	}
	return nil
}

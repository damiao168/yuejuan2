package auth

import (
	"context"
	"database/sql"
	"errors"
)

func validateManagedSessionTarget(actor User, scope AccessScope, target ManagedUser) error {
	if scope.TenantID != actor.TenantID || scope.ActorID != actor.ID || actor.ID == "" {
		return ErrForbidden
	}
	if target.ID == actor.ID || !CanManageUserRoles(actor, target.Roles) {
		return ErrUserStatusForbidden
	}
	if !scope.TenantWide && !scope.managesSchool(target.SchoolID) {
		return ErrOrganizationScope
	}
	return nil
}

// RevokeManagedUserSessions 按受管用户的角色层级和学校校验目标；自助退出走 LogoutAll。
// 目标账户锁覆盖校验与凭据撤销，不能在角色变更后仍按旧角色撤销其会话。
func (s *PostgresStore) RevokeManagedUserSessions(ctx context.Context, actor User, scope AccessScope, userID, reason string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var target ManagedUser
	err = tx.QueryRowContext(ctx, `SELECT id::text,COALESCE(school_id::text,'') FROM app_user WHERE tenant_id=$1::uuid AND id::text=$2 AND deleted_at IS NULL FOR UPDATE`, actor.TenantID, userID).Scan(&target.ID, &target.SchoolID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrManagedUserNotFound
	}
	if err != nil {
		return 0, err
	}
	// 先取得账户锁再单独读取职责；等待另一次授权修改后必须按其已提交的新职责判断。
	target.Roles, err = loadManagedUserRolesTx(ctx, tx, actor.TenantID, userID)
	if err != nil {
		return 0, err
	}
	if err := validateManagedSessionTarget(actor, scope, target); err != nil {
		return 0, err
	}
	revoked, err := revokeAllSessionsTx(ctx, tx, actor.TenantID, userID, reason)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revoked, nil
}

// RevokeManagedUserSessions 与内存中的授权变更共用写锁，校验失败时保留全部凭据。
func (s *MemoryStore) RevokeManagedUserSessions(_ context.Context, actor User, scope AccessScope, userID, _ string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.TenantID != actor.TenantID || user.ID != userID || user.Status == "deleted" {
			continue
		}
		target := managedUserFromMemory(user, managedUserSchoolID(user))
		if err := validateManagedSessionTarget(actor, scope, target); err != nil {
			return 0, err
		}
		return s.revokeAllSessionsLocked(actor.TenantID, userID), nil
	}
	return 0, ErrManagedUserNotFound
}

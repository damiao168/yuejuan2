package auth

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// UpdateManagedUserProfile 在账户锁内核对旧资料，身份变更和旧凭据失效一起提交。
// 返回的布尔值基于完整登录身份比较，不会将仅修改姓名误报为需要重新登录。
func (s *PostgresStore) UpdateManagedUserProfile(ctx context.Context, actor User, scope AccessScope, userID string, input UpdateManagedUserProfileInput) (ManagedUser, bool, error) {
	input, err := normalizeManagedProfileInput(input)
	if err != nil {
		return ManagedUser{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ManagedUser{}, false, err
	}
	defer tx.Rollback()
	user, err := loadManagedAccessUser(ctx, tx, actor.TenantID, userID)
	if err != nil {
		return ManagedUser{}, false, err
	}
	if err := validateManagedProfileTarget(actor, scope, user, input); err != nil {
		return ManagedUser{}, false, err
	}
	var oldPhone string
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(phone_normalized,'') FROM app_user WHERE tenant_id=$1::uuid AND id=$2::uuid`, actor.TenantID, user.ID).Scan(&oldPhone); err != nil {
		return ManagedUser{}, false, err
	}
	phone := oldPhone
	if input.Phone != nil {
		phone = *input.Phone
	}
	// 工号允许清空；稳定用户名不随资料修改，仍可作为原有登录入口。
	// 仅在异常账户连用户名也没有时，拒绝移除最后一个登录标识。
	if user.Username == "" && phone == "" && input.EmployeeNo == "" {
		return ManagedUser{}, false, errInvalidManagedProfile
	}
	identityChanged := phone != oldPhone || input.EmployeeNo != user.EmployeeNo
	if !identityChanged && input.DisplayName == user.DisplayName {
		return user, false, nil
	}
	// 手机号和工号是租户内登录别名；唯一索引负责并发查重，不能先查再写后假定无冲突。
	_, err = tx.ExecContext(ctx, `UPDATE app_user SET display_name=$3,employee_no=NULLIF($4,''),phone_normalized=NULLIF($5,''),security_epoch=security_epoch+CASE WHEN $6 THEN 1 ELSE 0 END,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, actor.TenantID, user.ID, input.DisplayName, input.EmployeeNo, phone, identityChanged)
	if err != nil {
		lowerError := strings.ToLower(err.Error())
		if strings.Contains(lowerError, "uq_app_user_tenant_phone") || strings.Contains(lowerError, "uq_app_user_tenant_employee_no") {
			return ManagedUser{}, false, ErrIdentityExists
		}
		return ManagedUser{}, false, err
	}
	if identityChanged {
		if err := revokeManagedProfileCredentials(ctx, tx, actor.TenantID, user.ID); err != nil {
			return ManagedUser{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return ManagedUser{}, false, err
	}
	user.DisplayName, user.EmployeeNo, user.PhoneMasked = input.DisplayName, input.EmployeeNo, MaskPhone(phone)
	return user, identityChanged, nil
}

// 调用方持有账户锁；邀请链接没有安全版本，因此必须显式作废，待激活人员需要重新生成链接。
// 恢复链接同时作废，不能在登录身份已变更后继续用于重置凭据。
func revokeManagedProfileCredentials(ctx context.Context, tx *sql.Tx, tenantID, userID string) error {
	for _, statement := range []string{
		`UPDATE auth_session SET revoked_at=now(),revoke_reason='profile_changed',updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`,
		`UPDATE auth_trusted_device SET revoked_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND revoked_at IS NULL`,
		`UPDATE auth_recovery SET used_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL`,
		`UPDATE auth_activation SET used_at=now(),updated_at=now() WHERE tenant_id=$1::uuid AND user_id=$2::uuid AND used_at IS NULL`,
	} {
		if _, err := tx.ExecContext(ctx, statement, tenantID, userID); err != nil {
			return err
		}
	}
	return nil
}

func (s *MemoryStore) UpdateManagedUserProfile(_ context.Context, actor User, scope AccessScope, userID string, input UpdateManagedUserProfileInput) (ManagedUser, bool, error) {
	input, err := normalizeManagedProfileInput(input)
	if err != nil {
		return ManagedUser{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, target := range s.users {
		if target.TenantID != actor.TenantID || target.ID != userID || target.Status == "deleted" {
			continue
		}
		schoolID, err := managedUserAuthority(actor, scope, target)
		if err != nil {
			return ManagedUser{}, false, err
		}
		user := managedUserFromMemory(target, schoolID)
		if err := validateManagedProfileTarget(actor, scope, user, input); err != nil {
			return ManagedUser{}, false, err
		}
		phone := target.PhoneNormalized
		if input.Phone != nil {
			phone = *input.Phone
		}
		if target.Username == "" && phone == "" && input.EmployeeNo == "" {
			return ManagedUser{}, false, errInvalidManagedProfile
		}
		identityChanged := phone != target.PhoneNormalized || input.EmployeeNo != target.EmployeeNo
		if !identityChanged && input.DisplayName == target.DisplayName {
			return user, false, nil
		}
		// 同一写锁内排除目标后查重；失败时不改变资料、安全版本或链接。
		for _, existing := range s.users {
			if existing.TenantID == actor.TenantID && existing.ID != target.ID && existing.Status != "deleted" &&
				((phone != "" && phone == existing.PhoneNormalized) || (input.EmployeeNo != "" && input.EmployeeNo == existing.EmployeeNo)) {
				return ManagedUser{}, false, ErrIdentityExists
			}
		}
		target.DisplayName, target.EmployeeNo, target.PhoneNormalized = input.DisplayName, input.EmployeeNo, phone
		if identityChanged {
			target.SecurityEpoch++
			s.revokeUserCredentials(target.TenantID, target.ID)
			now := time.Now().UTC()
			for hash, activation := range s.activations {
				if activation.TenantID == target.TenantID && activation.UserID == target.ID && activation.UsedAt.IsZero() {
					activation.UsedAt = now
					s.activations[hash] = activation
				}
			}
			for hash, recovery := range s.recoveries {
				if recovery.TenantID == target.TenantID && recovery.UserID == target.ID && recovery.UsedAt.IsZero() {
					recovery.UsedAt = now
					s.recoveries[hash] = recovery
				}
			}
		}
		s.users[key] = target
		return managedUserFromMemory(target, schoolID), identityChanged, nil
	}
	return ManagedUser{}, false, ErrManagedUserNotFound
}

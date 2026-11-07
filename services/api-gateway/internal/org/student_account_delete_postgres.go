package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// 调用方已锁住学生并排除其业务引用；账户行软删除保留，历史操作者外键仍能追溯。
// 只清理本流程生成且绑定完整的纯学生账户，旧账号和混合职责不能自动撤权或改绑。
func retireUnusedStudentAccount(ctx context.Context, tx *sql.Tx, tenantID, schoolID, studentID string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id::text,username,status,COALESCE(school_id::text,''),deleted_at IS NOT NULL
FROM app_user WHERE tenant_id=$1 AND student_id::text=$2 ORDER BY id FOR UPDATE`, tenantID, studentID)
	if err != nil {
		return err
	}
	var userID string
	count := 0
	eligible := true
	for rows.Next() {
		var username, status, accountSchool string
		var deleted bool
		if err = rows.Scan(&userID, &username, &status, &accountSchool, &deleted); err != nil {
			rows.Close()
			return err
		}
		count++
		eligible = eligible && !deleted && status == "disabled" && accountSchool == schoolID && username == "student_"+strings.ReplaceAll(studentID, "-", "")
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if count == 0 {
		return nil
	}
	if count != 1 || !eligible {
		return ErrStudentInUse
	}
	roleRows, err := tx.QueryContext(ctx, `SELECT r.code,r.scope_type,ur.data_scope
FROM user_role ur JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
WHERE ur.tenant_id=$1 AND ur.user_id=$2::uuid AND ur.deleted_at IS NULL FOR UPDATE OF ur`, tenantID, userID)
	if err != nil {
		return err
	}
	roleCount := 0
	for roleRows.Next() {
		var code, scopeType string
		var raw []byte
		if err = roleRows.Scan(&code, &scopeType, &raw); err != nil {
			roleRows.Close()
			return err
		}
		var scope map[string]any
		if err = json.Unmarshal(raw, &scope); err != nil {
			roleRows.Close()
			return err
		}
		roleCount++
		eligible = eligible && code == "student" && scopeType == "self" && scope["scope"] == "self" && scope["student_id"] == studentID && len(scope) == 2
	}
	if err = roleRows.Err(); err != nil {
		roleRows.Close()
		return err
	}
	roleRows.Close()
	if roleCount != 1 || !eligible {
		return ErrStudentInUse
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_session SET revoked_at=now(),revoke_reason='student_deleted',updated_at=now() WHERE tenant_id=$1 AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE auth_trusted_device SET revoked_at=now(),updated_at=now() WHERE tenant_id=$1 AND user_id=$2::uuid AND revoked_at IS NULL`, tenantID, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE user_role SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND user_id=$2::uuid AND deleted_at IS NULL`, tenantID, userID); err != nil {
		return err
	}
	// 先清除指向名册的外键，再删除名册；若后续发现新的学生业务引用，外层事务会恢复账号和角色。
	_, err = tx.ExecContext(ctx, `UPDATE app_user SET status='disabled',student_id=NULL,security_epoch=security_epoch+1,deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, userID)
	return err
}

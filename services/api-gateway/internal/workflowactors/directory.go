// Package workflowactors 提供工作流分派所需的最少人员信息，不开放组织账号管理资料。
package workflowactors

import (
	"context"
	"database/sql"
)

type Actor struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// List 同时限定考试所在学校、启用账号和实际任务权限；手机号、账号权限配置等不进入选人响应。
func List(ctx context.Context, db *sql.DB, tenantID, examID, permission string) ([]Actor, error) {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT u.id::text,u.username,COALESCE(u.display_name,'') FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id AND e.id=$2::uuid AND e.deleted_at IS NULL
JOIN user_role ur ON ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL
JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL AND p.code=$3
WHERE u.tenant_id=$1::uuid AND u.status='active' AND u.deleted_at IS NULL ORDER BY u.username,u.id::text`, tenantID, examID, permission)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Actor{}
	for rows.Next() {
		var value Actor
		if err = rows.Scan(&value.ID, &value.Username, &value.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

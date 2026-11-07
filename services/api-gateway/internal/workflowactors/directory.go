// Package workflowactors 提供工作流分派所需的最少人员信息，不开放组织账号管理资料。
package workflowactors

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/auth"
)

var ErrAssigneeIneligible = errors.New("assignee must be an active, authorized teacher of this exam subject and school")

type Actor struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// List 同时限定考试所在学校、启用账号和实际任务权限；手机号、账号权限配置等不进入选人响应。
func List(ctx context.Context, db *sql.DB, tenantID, examID, permission string) ([]Actor, error) {
	if requiredRole := reviewRole(permission); requiredRole != "" {
		return listReviewActors(ctx, db, tenantID, examID, permission, requiredRole)
	}
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

// 任教学科仅决定能否接收新阅卷任务；它不授予考试读取权限，也不限制采集、资料处理等岗位。
func listReviewActors(ctx context.Context, db *sql.DB, tenantID, examID, permission, requiredRole string) ([]Actor, error) {
	rows, err := db.QueryContext(ctx, `SELECT u.id::text,u.username,COALESCE(u.display_name,''),e.subject,
  COALESCE(to_json(u.teaching_subjects)::text,'[]'),`+actorRolesJSON+`
FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id AND e.id=$2::uuid AND e.deleted_at IS NULL
WHERE u.tenant_id=$1::uuid AND u.status='active' AND u.deleted_at IS NULL
  AND `+actorPermissionPredicate+`
ORDER BY u.username,u.id::text`, tenantID, examID, permission, requiredRole)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Actor{}
	for rows.Next() {
		var value Actor
		var subject, subjectsJSON, rolesJSON string
		if err := rows.Scan(&value.ID, &value.Username, &value.DisplayName, &subject, &subjectsJSON, &rolesJSON); err != nil {
			return nil, err
		}
		eligible, err := matchesSubject(subject, subjectsJSON, rolesJSON)
		if err != nil {
			return nil, err
		}
		if eligible {
			out = append(out, value)
		}
	}
	return out, rows.Err()
}

type AssigneeQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// ValidateReviewAssignee 必须与实际分派共用事务。锁住账号可让个人资料的学科变更与分派串行，
// 避免候选列表打开后权限或学科已变化，仍把新任务分给原候选人。管理员只豁免学科，不豁免同校和岗位权限。
func ValidateReviewAssignee(ctx context.Context, tx AssigneeQuerier, tenantID, examID, userID, permission string) error {
	requiredRole := reviewRole(permission)
	if requiredRole == "" || userID == "" {
		return ErrAssigneeIneligible
	}
	return validateSubjectAssignee(ctx, tx, tenantID, examID, userID, permission, requiredRole, actorPermissionPredicate)
}

// ValidateCalibrationAssignee 保留教师和管理员原有的试评入口；它只派发校准样本，不能代替实际阅卷分派的 grader 岗位。
// 普通教师仍需匹配任教学科，管理员只豁免学科，双方都须启用、同校且实际持有 review:work 权限。
func ValidateCalibrationAssignee(ctx context.Context, tx AssigneeQuerier, tenantID, examID, userID string) error {
	if userID == "" {
		return ErrAssigneeIneligible
	}
	predicate := strings.Replace(actorPermissionPredicate, "r.code=$4", "r.code=ANY(string_to_array($4,','))", 1)
	return validateSubjectAssignee(ctx, tx, tenantID, examID, userID, "review:work", "teacher,grader,platform_admin,tenant_admin,school_admin", predicate)
}

// 岗位谓词只能由上述固定用途提供；候选人账号锁与实际写入共用事务，防止资料变更交错。
func validateSubjectAssignee(ctx context.Context, tx AssigneeQuerier, tenantID, examID, userID, permission, requiredRole, rolePredicate string) error {
	var subject, subjectsJSON, rolesJSON string
	err := tx.QueryRowContext(ctx, `SELECT e.subject,COALESCE(to_json(u.teaching_subjects)::text,'[]'),`+actorRolesJSON+`
FROM app_user u
JOIN exam e ON e.tenant_id=u.tenant_id AND e.school_id=u.school_id AND e.id=$2::uuid AND e.deleted_at IS NULL
WHERE u.tenant_id=$1::uuid AND u.id=$5::uuid AND u.status='active' AND u.deleted_at IS NULL
  AND `+rolePredicate+`
FOR SHARE OF u`, tenantID, examID, permission, requiredRole, userID).Scan(&subject, &subjectsJSON, &rolesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAssigneeIneligible
	}
	if err != nil {
		return err
	}
	eligible, err := matchesSubject(subject, subjectsJSON, rolesJSON)
	if err != nil {
		return err
	}
	if !eligible {
		return ErrAssigneeIneligible
	}
	return nil
}

const actorRolesJSON = `COALESCE((SELECT json_agg(r.code)::text FROM user_role ur
  JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
  WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL),'[]')`

const actorPermissionPredicate = `EXISTS (SELECT 1 FROM user_role ur
  JOIN role r ON r.tenant_id=ur.tenant_id AND r.id=ur.role_id AND r.deleted_at IS NULL
  JOIN role_permission rp ON rp.tenant_id=r.tenant_id AND rp.role_id=r.id AND rp.deleted_at IS NULL
  JOIN permission p ON p.tenant_id=rp.tenant_id AND p.id=rp.permission_id AND p.deleted_at IS NULL AND p.code=$3
  WHERE ur.tenant_id=u.tenant_id AND ur.user_id=u.id AND ur.deleted_at IS NULL AND r.code=$4)`

func reviewRole(permission string) string {
	switch permission {
	case "review:work":
		return "grader"
	case "arbitration:work":
		return "arbitrator"
	case "appeal:work":
		return "teacher"
	default:
		return ""
	}
}

func matchesSubject(subject, subjectsJSON, rolesJSON string) (bool, error) {
	var subjects, roles []string
	if err := json.Unmarshal([]byte(subjectsJSON), &subjects); err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(rolesJSON), &roles); err != nil {
		return false, err
	}
	if auth.IsSchoolAdministratorRoles(roles) {
		return true, nil
	}
	examSubject, ok := assessment.NormalizeSubjectCode(subject)
	if !ok {
		return false, nil
	}
	for _, value := range subjects {
		if teacherSubject, valid := assessment.NormalizeSubjectCode(value); valid && teacherSubject == examSubject {
			return true, nil
		}
	}
	return false, nil
}

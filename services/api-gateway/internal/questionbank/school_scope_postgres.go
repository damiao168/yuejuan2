package questionbank

import "fmt"

// bankSchoolScopeSQL 只补充题库的学校归属，不修改认证作用域或赋予考试、班级管理权限。
// 普通教师从可信账号取本校；题库查询仍须另行检查 owner/显式 ACL，不能因同校直接读题。
// 参数均为本包固定的列名和 SQL 占位符，不能传入请求中的字符串。
func bankSchoolScopeSQL(school, tenant, actor, tenantWide, schools string) string {
	return fmt.Sprintf(`(%s OR %s::text=ANY(%s::text[]) OR EXISTS(
 SELECT 1 FROM app_user qb_actor
 JOIN school qb_school ON qb_school.tenant_id=qb_actor.tenant_id AND qb_school.id=qb_actor.school_id
 WHERE qb_actor.tenant_id=%s::uuid AND qb_actor.id=%s::uuid
 AND qb_actor.status='active' AND qb_actor.deleted_at IS NULL
 AND qb_school.status='active' AND qb_school.deleted_at IS NULL AND qb_actor.school_id=%s
 AND EXISTS(SELECT 1 FROM user_role qb_ur
 JOIN role qb_role ON qb_role.tenant_id=qb_ur.tenant_id AND qb_role.id=qb_ur.role_id AND qb_role.deleted_at IS NULL
 WHERE qb_ur.tenant_id=qb_actor.tenant_id AND qb_ur.user_id=qb_actor.id AND qb_ur.deleted_at IS NULL
 AND qb_role.code IN('teacher','grader','arbitrator'))))`, tenantWide, school, schools, tenant, actor, school)
}

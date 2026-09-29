package auth

import "strings"

type RoleAssignmentPolicy struct {
	Code              string
	Human             bool
	Service           bool
	ManagedAssignable bool
	CanonicalScope    string
	SchoolRequired    bool
	ClassBinding      bool
	AssignableBy      []string
}

var roleAssignmentPolicies = map[string]RoleAssignmentPolicy{
	"platform_admin":            {Code: "platform_admin", Human: true, CanonicalScope: "platform"},
	"tenant_admin":              {Code: "tenant_admin", Human: true, CanonicalScope: "tenant"},
	"school_admin":              {Code: "school_admin", Human: true, ManagedAssignable: true, CanonicalScope: "school", SchoolRequired: true, AssignableBy: []string{"tenant_admin"}},
	"teacher":                   {Code: "teacher", Human: true, ManagedAssignable: true, CanonicalScope: "class", SchoolRequired: true, ClassBinding: true, AssignableBy: []string{"tenant_admin", "school_admin"}},
	"grader":                    {Code: "grader", Human: true, ManagedAssignable: true, CanonicalScope: "exam_task", SchoolRequired: true, AssignableBy: []string{"tenant_admin", "school_admin"}},
	"arbitrator":                {Code: "arbitrator", Human: true, ManagedAssignable: true, CanonicalScope: "exam_task", SchoolRequired: true, AssignableBy: []string{"tenant_admin", "school_admin"}},
	"auditor":                   {Code: "auditor", Human: true, ManagedAssignable: true, CanonicalScope: "tenant", AssignableBy: []string{"tenant_admin"}},
	"student":                   {Code: "student", Human: true, CanonicalScope: "self"},
	"page_processing_worker":    {Code: "page_processing_worker", Service: true, CanonicalScope: "service"},
	"subjective_grading_worker": {Code: "subjective_grading_worker", Service: true, CanonicalScope: "service"},
}

func RolePolicy(code string) RoleAssignmentPolicy {
	code = strings.TrimSpace(code)
	if policy, ok := roleAssignmentPolicies[code]; ok {
		return policy
	}
	if strings.HasSuffix(code, "_worker") {
		return RoleAssignmentPolicy{Code: code, Service: true, CanonicalScope: "service"}
	}
	return RoleAssignmentPolicy{Code: code}
}

func CanAssignManagedRole(actor User, targetRole string) bool {
	policy := RolePolicy(targetRole)
	if !policy.ManagedAssignable || policy.Service || !policy.Human {
		return false
	}
	for _, actorRole := range actor.Roles {
		for _, allowed := range policy.AssignableBy {
			if actorRole == allowed {
				return true
			}
		}
	}
	return false
}

// CanManageUserRoles 要求操作者能管理目标用户的全部角色，不能凭其中一个低权限角色放行。
func CanManageUserRoles(actor User, targetRoles []string) bool {
	if len(targetRoles) == 0 {
		return false
	}
	for _, role := range targetRoles {
		if !CanAssignManagedRole(actor, role) {
			return false
		}
	}
	return true
}

func canonicalRoleDataScope(roleCode, schoolID string) map[string]any {
	policy := RolePolicy(roleCode)
	scope := map[string]any{"scope": policy.CanonicalScope}
	if policy.CanonicalScope == "school" && strings.TrimSpace(schoolID) != "" {
		scope["school_id"] = strings.TrimSpace(schoolID)
	}
	return scope
}

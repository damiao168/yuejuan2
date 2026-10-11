package auth

import "slices"

// managedRoleCodes 保留旧单职责请求；多个职责分别生成原有作用域，不合并成学校级授权。
func managedRoleCodes(input CreateManagedUserInput) []string {
	if len(input.RoleCodes) > 0 {
		return uniqueSortedIDs(input.RoleCodes)
	}
	return uniqueSortedIDs([]string{input.RoleCode})
}

func managedRoleScopes(roles []string, schoolID string) map[string]any {
	scopes := map[string]any{}
	for _, role := range roles {
		scopes[role] = canonicalRoleDataScope(role, schoolID)
	}
	return scopes
}

func validateManagedAccess(actor User, roles, classIDs []string) error {
	if len(roles) == 0 || len(roles) > 8 || !CanManageUserRoles(actor, roles) {
		return ErrRoleAssignment
	}
	if len(classIDs) > 0 && !slices.Contains(roles, "teacher") {
		return ErrInvalidRoleBinding
	}
	return nil
}

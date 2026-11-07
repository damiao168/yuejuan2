package auth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *MemoryStore) ListManagedUsers(_ context.Context, tenantID string, filter ManagedUserFilter) ([]ManagedUser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []ManagedUser{}
	for _, user := range s.users {
		if !matchesManagedUser(user, tenantID, filter) {
			continue
		}
		out = append(out, managedUserFromMemory(user, managedUserSchoolID(user)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if filter.CursorID != "" {
		out = managedUsersAfterCursor(out, filter)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func matchesManagedUser(user UserWithPassword, tenantID string, filter ManagedUserFilter) bool {
	if user.TenantID != tenantID || (filter.UserID != "" && user.ID != filter.UserID) {
		return false
	}
	if filter.Status != "" && user.Status != filter.Status {
		return false
	}
	if filter.RestrictSchools && !containsID(filter.SchoolIDs, managedUserSchoolID(user)) {
		return false
	}
	if !matchesManagedUserQuery(user, filter.Query) {
		return false
	}
	return filter.Role == "" || HasRole(user.User, filter.Role)
}

func matchesManagedUserQuery(user UserWithPassword, query string) bool {
	query = strings.ToLower(query)
	for _, value := range []string{user.Username, user.DisplayName, user.PhoneNormalized, user.EmployeeNo} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func managedUsersAfterCursor(items []ManagedUser, filter ManagedUserFilter) []ManagedUser {
	for index, item := range items {
		if item.CreatedAt.Before(filter.CursorCreatedAt) || (item.CreatedAt.Equal(filter.CursorCreatedAt) && item.ID < filter.CursorID) {
			return items[index:]
		}
	}
	return items[len(items):]
}

func (s *MemoryStore) ListAssignableRoles(_ context.Context, actor User) ([]AssignableRole, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []AssignableRole{}
	for _, role := range s.roles[actor.TenantID] {
		if policy := RolePolicy(role.Code); CanAssignManagedRole(actor, role.Code) && role.ScopeType == policy.CanonicalScope {
			out = append(out, role)
		}
	}
	return out, nil
}

func (s *MemoryStore) CreateManagedUser(_ context.Context, actor User, actorScope AccessScope, input CreateManagedUserInput, passwordHash string) (ManagedUser, error) {
	subjects, err := NormalizeTeachingSubjects(input.TeachingSubjects)
	if err != nil {
		return ManagedUser{}, err
	}
	input.TeachingSubjects = subjects
	s.mu.Lock()
	defer s.mu.Unlock()
	key := actor.TenantCode + "|" + input.Username
	if existing, exists := s.users[key]; exists && existing.User.Status != "deleted" {
		return ManagedUser{}, ErrUsernameExists
	}
	if err := s.validateManagedUserIdentity(actor.TenantID, input); err != nil {
		return ManagedUser{}, err
	}
	schoolID, err := s.managedUserBinding(actor, actorScope, input)
	if err != nil {
		return ManagedUser{}, err
	}
	s.userSeq++
	status := "active"
	now := time.Now().UTC()
	activatedAt := now
	if input.ActivationTokenHash != "" {
		status = "invited"
		activatedAt = time.Time{}
	}
	user := UserWithPassword{
		User: User{
			ID: fmt.Sprintf("managed-user-%d", s.userSeq), TenantID: actor.TenantID, TenantCode: actor.TenantCode,
			Username: input.Username, DisplayName: input.DisplayName, Status: status, MustChangePassword: input.MustChangePassword,
			Roles: managedRoleCodes(input), Permissions: []string{}, DataScope: managedRoleScopes(managedRoleCodes(input), schoolID),
		},
		PasswordHash: passwordHash, SchoolID: schoolID, PhoneNormalized: input.Phone, EmployeeNo: input.EmployeeNo, SecurityEpoch: 1, ActivatedAt: activatedAt, CreatedAt: now, TeachingSubjects: append([]string{}, input.TeachingSubjects...),
	}
	s.users[key] = user
	if input.ActivationTokenHash != "" {
		s.activations[input.ActivationTokenHash] = memoryActivation{TenantID: actor.TenantID, UserID: user.ID, ExpiresAt: input.ActivationExpiresAt}
	}
	return managedUserFromMemory(user, schoolID), nil
}

// 调用方持有写锁，身份查重、角色校验与新增账户之间不能插入另一次创建。
func (s *MemoryStore) validateManagedUserIdentity(tenantID string, input CreateManagedUserInput) error {
	for _, existing := range s.users {
		if existing.TenantID != tenantID || existing.Status == "deleted" {
			continue
		}
		if (input.Phone != "" && existing.PhoneNormalized == input.Phone) || (input.EmployeeNo != "" && existing.EmployeeNo == input.EmployeeNo) {
			return ErrIdentityExists
		}
	}
	return nil
}

func (s *MemoryStore) managedUserBinding(actor User, actorScope AccessScope, input CreateManagedUserInput) (string, error) {
	roles := managedRoleCodes(input)
	if err := validateManagedAccess(actor, roles); err != nil {
		return "", err
	}
	for _, code := range roles {
		role, exists := s.roles[actor.TenantID][code]
		if !exists {
			return "", ErrRoleNotFound
		}
		policy := RolePolicy(role.Code)
		if !CanAssignManagedRole(actor, role.Code) || role.ScopeType != policy.CanonicalScope {
			return "", ErrRoleAssignment
		}
	}
	schoolID := strings.TrimSpace(input.SchoolID)
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

func (s *MemoryStore) UpdateManagedUserStatus(_ context.Context, actor User, actorScope AccessScope, userID string, status string) (ManagedUser, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var targetKey string
	var target UserWithPassword
	for key, user := range s.users {
		if user.TenantID == actor.TenantID && user.ID == userID && user.Status != "deleted" {
			targetKey = key
			target = user
			break
		}
	}
	if targetKey == "" {
		return ManagedUser{}, "", ErrManagedUserNotFound
	}
	schoolID, err := managedUserAuthority(actor, actorScope, target)
	if err != nil {
		return ManagedUser{}, "", err
	}
	previousStatus := target.Status
	// 邀请账户只能通过激活流程设密码，不能靠状态管理直接变成启用账户。
	if previousStatus == "invited" {
		return ManagedUser{}, "", ErrUserStatusForbidden
	}
	if previousStatus == status {
		return managedUserFromMemory(target, schoolID), previousStatus, nil
	}
	if err := s.validateManagedUserStatus(target, schoolID, status); err != nil {
		return ManagedUser{}, "", err
	}
	target.Status = status
	if status == "disabled" {
		target.SecurityEpoch++
		s.revokeUserCredentials(target.TenantID, target.ID)
	}
	s.users[targetKey] = target
	return managedUserFromMemory(target, schoolID), previousStatus, nil
}

func managedUserAuthority(actor User, actorScope AccessScope, target UserWithPassword) (string, error) {
	if target.ID == actor.ID || !CanManageUserRoles(actor, target.Roles) {
		return "", ErrUserStatusForbidden
	}
	schoolID := managedUserSchoolID(target)
	// 教学关系派生出的学校可见范围不授予改密、激活或停用账号的权限。
	if !actorScope.TenantWide && !actorScope.managesSchool(schoolID) {
		return "", ErrOrganizationScope
	}
	return schoolID, nil
}

func (s *MemoryStore) validateManagedUserStatus(target UserWithPassword, schoolID, status string) error {
	if status == "active" && target.Status != "disabled" {
		return ErrUserStatusForbidden
	}
	if status != "disabled" || !HasRole(target.User, "school_admin") {
		return nil
	}
	// 在状态变更的同一写锁内计数，不能并发禁用同一学校的最后两名管理员。
	activeAdmins := 0
	for _, user := range s.users {
		if user.TenantID == target.TenantID && user.Status == "active" && HasRole(user.User, "school_admin") && managedUserSchoolID(user) == schoolID {
			activeAdmins++
		}
	}
	if activeAdmins <= 1 {
		return ErrLastSchoolAdmin
	}
	return nil
}

func managedUserFromMemory(user UserWithPassword, schoolID string) ManagedUser {
	return ManagedUser{MustChangePassword: user.MustChangePassword,
		ID: user.ID, Username: user.Username, DisplayName: user.DisplayName,
		PhoneMasked: MaskPhone(user.PhoneNormalized), EmployeeNo: user.EmployeeNo,
		Status: user.Status, Roles: append([]string(nil), user.Roles...), SchoolID: schoolID, TeachingSubjects: append([]string{}, user.TeachingSubjects...),
		ActivatedAt: timePointer(user.ActivatedAt), LastLoginAt: timePointer(user.LastLoginAt), CreatedAt: user.CreatedAt,
	}
}

func managedUserSchoolID(user UserWithPassword) string {
	if user.SchoolID != "" {
		return user.SchoolID
	}
	return firstScopeID(user.DataScope, "school_id")
}

func timePointer(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

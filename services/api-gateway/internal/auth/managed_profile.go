package auth

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

var (
	errInvalidManagedProfile = errors.New("invalid managed user profile")
	errInvalidProfilePhone   = errors.New("invalid managed user phone")
)

func normalizeManagedProfileInput(input UpdateManagedUserProfileInput) (UpdateManagedUserProfileInput, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.EmployeeNo = strings.TrimSpace(input.EmployeeNo)
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 80 || utf8.RuneCountInString(input.EmployeeNo) > 64 ||
		len(input.DisplayName) > maxDisplayNameBytes || len(input.EmployeeNo) > maxEmployeeNoBytes ||
		len(input.ExpectedDisplayName) > maxDisplayNameBytes || len(input.ExpectedEmployeeNo) > maxEmployeeNoBytes || len(input.ExpectedPhoneMasked) > maxPhoneBytes {
		return input, errInvalidManagedProfile
	}
	if (input.TeachingSubjects == nil) != (input.ExpectedTeachingSubjects == nil) {
		return input, errInvalidManagedProfile
	}
	if input.TeachingSubjects != nil {
		var err error
		input.TeachingSubjects, err = NormalizeTeachingSubjects(input.TeachingSubjects)
		if err != nil {
			return input, err
		}
		input.ExpectedTeachingSubjects, err = NormalizeTeachingSubjects(input.ExpectedTeachingSubjects)
		if err != nil {
			return input, err
		}
	}
	if input.Phone != nil {
		if len(*input.Phone) > maxPhoneBytes || strings.TrimSpace(*input.Phone) == "" {
			return input, errInvalidProfilePhone
		}
		phone, err := NormalizePhone(*input.Phone)
		if err != nil {
			return input, errInvalidProfilePhone
		}
		input.Phone = &phone
	}
	return input, nil
}

func validateManagedProfileTarget(actor User, scope AccessScope, user ManagedUser, input UpdateManagedUserProfileInput) error {
	if user.ID == actor.ID || !CanManageUserRoles(actor, user.Roles) {
		return ErrUserStatusForbidden
	}
	if !scope.TenantWide && !scope.managesSchool(user.SchoolID) {
		return ErrOrganizationScope
	}
	// 不返回原始手机号；旧资料比较只能发现掩码可见的号码变化，不能区分同掩码的两个号码。
	// 行锁仍保证本次写入、身份查重和凭据撤销不会与同一账户的管理修改交错提交。
	if user.DisplayName != input.ExpectedDisplayName || user.EmployeeNo != input.ExpectedEmployeeNo || user.PhoneMasked != input.ExpectedPhoneMasked {
		return ErrManagedUserProfileConflict
	}
	if input.TeachingSubjects != nil {
		if !slices.Equal(uniqueSortedIDs(user.TeachingSubjects), input.ExpectedTeachingSubjects) {
			return ErrManagedUserProfileConflict
		}
		return validateTeachingSubjects(user.Roles, input.TeachingSubjects)
	}
	return nil
}

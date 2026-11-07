package auth

import (
	"errors"
	"slices"

	"edugrade-enterprise/services/api-gateway/internal/subjectcode"
)

var ErrInvalidTeachingSubjects = errors.New("review teacher teaching subjects are invalid")

// IsSchoolAdministratorRoles 保留管理员的学校管理职责；兼任阅卷教师不要求填写任教学科。
func IsSchoolAdministratorRoles(roles []string) bool {
	return slices.Contains(roles, "platform_admin") || slices.Contains(roles, "tenant_admin") || slices.Contains(roles, "school_admin")
}

// IsReviewTeacherRoles 只识别普通阅卷人员，学科资格本身不会增加其可访问的考试或学校。
func IsReviewTeacherRoles(roles []string) bool {
	return !IsSchoolAdministratorRoles(roles) && (slices.Contains(roles, "teacher") || slices.Contains(roles, "grader") || slices.Contains(roles, "arbitrator"))
}

// NormalizeTeachingSubjects 使用考试学科的统一代码，别名在保存前归一并去重。
func NormalizeTeachingSubjects(subjects []string) ([]string, error) {
	if len(subjects) > 9 {
		return nil, ErrInvalidTeachingSubjects
	}
	out := make([]string, 0, len(subjects))
	for _, value := range subjects {
		code, ok := subjectcode.Normalize(value)
		if !ok {
			return nil, ErrInvalidTeachingSubjects
		}
		out = append(out, string(code))
	}
	return uniqueSortedIDs(out), nil
}

func validateTeachingSubjects(roles, subjects []string) error {
	if IsReviewTeacherRoles(roles) && len(subjects) == 0 {
		return ErrInvalidTeachingSubjects
	}
	return nil
}

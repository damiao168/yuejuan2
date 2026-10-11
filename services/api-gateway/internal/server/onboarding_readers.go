package server

import (
	"context"
	"slices"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/handlers"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/onboarding"
	"edugrade-enterprise/services/api-gateway/internal/org"
)

type onboardingTenantReader struct{ store org.Store }

func (r onboardingTenantReader) HasManagedTenant(ctx context.Context) (bool, error) {
	if readinessStore, ok := r.store.(org.OnboardingReadinessStore); ok {
		return readinessStore.HasManagedTenant(ctx)
	}
	tenants, err := r.store.ListTenants(ctx, auth.PlatformTenantID, true, org.TenantListFilter{})
	if err != nil {
		return false, err
	}
	for _, tenant := range tenants {
		if tenant.ID != auth.PlatformTenantID && tenant.Code != "platform" && tenant.Status == "active" {
			return true, nil
		}
	}
	return false, nil
}

type onboardingOrganizationReader struct{ store org.Store }

// 回退实现逐项读取并按 scope 过滤；不能使用 readiness 专用查询时，也不能把其他学校的组织数据算入当前向导。
func (r onboardingOrganizationReader) Summary(ctx context.Context, tenantID string, scope onboarding.ResourceScope) (onboarding.OrganizationSummary, error) {
	if readinessStore, ok := r.store.(org.OnboardingReadinessStore); ok {
		summary, err := readinessStore.OnboardingReadiness(ctx, tenantID, scope.SchoolIDs, scope.TenantWide)
		return onboarding.OrganizationSummary{
			HasSchool:            summary.HasSchool,
			HasTeachingStructure: summary.HasTeachingStructure,
			HasStudents:          summary.HasStudents,
		}, err
	}

	var summary onboarding.OrganizationSummary
	schools, err := r.store.ListSchools(ctx, tenantID)
	if err != nil {
		return summary, err
	}
	summary.HasSchool = hasActiveOnboardingSchool(schools, scope)

	// 四次读取保留原顺序和已累计的结果；即使没有年级，也要观察班级查询的错误。
	grades, err := r.store.ListGrades(ctx, tenantID, "")
	if err != nil {
		return summary, err
	}
	hasGrade := hasActiveOnboardingGrade(grades, scope)
	classes, err := r.store.ListClasses(ctx, tenantID, "")
	if err != nil {
		return summary, err
	}
	// 年级和班级分别满足同一 scope 即可，不要求它们来自同一所学校。
	summary.HasTeachingStructure = hasGrade && hasActiveOnboardingClass(classes, scope)

	students, err := r.store.ListStudents(ctx, tenantID, org.StudentListFilter{})
	if err != nil {
		return summary, err
	}
	summary.HasStudents = hasActiveOnboardingStudent(students, scope)
	return summary, nil
}

func hasActiveOnboardingSchool(items []org.School, scope onboarding.ResourceScope) bool {
	for _, item := range items {
		if item.Status == "active" && allowsOnboardingSchool(scope, item.ID) {
			return true
		}
	}
	return false
}

func hasActiveOnboardingGrade(items []org.Grade, scope onboarding.ResourceScope) bool {
	for _, item := range items {
		if item.Status == "active" && allowsOnboardingSchool(scope, item.SchoolID) {
			return true
		}
	}
	return false
}

func hasActiveOnboardingClass(items []org.Class, scope onboarding.ResourceScope) bool {
	for _, item := range items {
		if item.Status == "active" && allowsOnboardingSchool(scope, item.SchoolID) {
			return true
		}
	}
	return false
}

func hasActiveOnboardingStudent(items []org.Student, scope onboarding.ResourceScope) bool {
	for _, item := range items {
		if item.Status == "active" && allowsOnboardingSchool(scope, item.SchoolID) {
			return true
		}
	}
	return false
}

type onboardingUserReader struct{ store auth.Store }

// 向导只关心仍可用的业务账号；每个角色最多取一条，避免为布尔结论加载整租户用户列表。
func (r onboardingUserReader) HasBusinessUser(ctx context.Context, tenantID string, scope onboarding.ResourceScope) (bool, error) {
	for _, role := range []string{"school_admin", "teacher", "grader", "arbitrator"} {
		users, err := r.store.ListManagedUsers(ctx, tenantID, auth.ManagedUserFilter{
			Role: role, Limit: 1, RestrictSchools: !scope.TenantWide, SchoolIDs: append([]string(nil), scope.SchoolIDs...),
		})
		if err != nil {
			return false, err
		}
		for _, user := range users {
			if user.Status == "active" {
				return true, nil
			}
		}
	}
	return false, nil
}

type onboardingExamReader struct{ store exam.Store }

func (r onboardingExamReader) HasExam(ctx context.Context, tenantID string, scope onboarding.ResourceScope) (bool, error) {
	items, err := r.store.ListExams(ctx, auth.AccessScope{
		TenantID: tenantID, TenantWide: scope.TenantWide, SchoolIDs: append([]string(nil), scope.SchoolIDs...),
		GradeIDs: append([]string(nil), scope.GradeIDs...), ClassIDs: append([]string(nil), scope.ClassIDs...), ExamIDs: append([]string(nil), scope.ExamIDs...),
	}, exam.ListFilter{Limit: 1})
	return len(items) > 0, err
}

type onboardingSystemReader struct{ system *handlers.Handlers }

func (r onboardingSystemReader) Summary(ctx context.Context) (onboarding.SystemReadinessSummary, error) {
	summary, err := r.system.ReadinessSummary(ctx)
	return onboarding.SystemReadinessSummary{
		CoreReady: summary.CoreReady, AIMode: summary.AIMode, AIConfigured: summary.AIConfigured,
		AIAvailable: summary.AIAvailable, AIModel: summary.AIModel,
	}, err
}

type onboardingGovernanceReader struct{ store modelgovernance.Store }

func (r onboardingGovernanceReader) DataPolicy(ctx context.Context, tenantID string) (onboarding.DataPolicySummary, error) {
	policy, err := r.store.GetPolicy(ctx, tenantID)
	if err != nil {
		return onboarding.DataPolicySummary{}, err
	}
	return onboarding.DataPolicySummary{
		ExternalEnabled: policy.ExternalEnabled, TextExportEnabled: policy.TextExportEnabled,
		ImageExportEnabled: policy.ImageExportEnabled, FallbackMode: policy.FallbackMode,
	}, nil
}

// tenant-wide 作用域允许全租户，否则学校 ID 必须来自身份解析出的白名单。
func allowsOnboardingSchool(scope onboarding.ResourceScope, schoolID string) bool {
	return scope.TenantWide || slices.Contains(scope.SchoolIDs, schoolID)
}

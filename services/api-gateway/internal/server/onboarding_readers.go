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
	for _, school := range schools {
		if school.Status == "active" && allowsOnboardingSchool(scope, school.ID) {
			summary.HasSchool = true
			break
		}
	}

	grades, err := r.store.ListGrades(ctx, tenantID, "")
	if err != nil {
		return summary, err
	}
	hasGrade := false
	for _, grade := range grades {
		if grade.Status == "active" && allowsOnboardingSchool(scope, grade.SchoolID) {
			hasGrade = true
			break
		}
	}
	classes, err := r.store.ListClasses(ctx, tenantID, "")
	if err != nil {
		return summary, err
	}
	for _, class := range classes {
		if class.Status == "active" && allowsOnboardingSchool(scope, class.SchoolID) {
			summary.HasTeachingStructure = hasGrade
			break
		}
	}

	students, err := r.store.ListStudents(ctx, tenantID, org.StudentListFilter{})
	if err != nil {
		return summary, err
	}
	for _, student := range students {
		if student.Status == "active" && allowsOnboardingSchool(scope, student.SchoolID) {
			summary.HasStudents = true
			break
		}
	}
	return summary, nil
}

type onboardingUserReader struct{ store auth.Store }

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

func allowsOnboardingSchool(scope onboarding.ResourceScope, schoolID string) bool {
	return scope.TenantWide || slices.Contains(scope.SchoolIDs, schoolID)
}

package onboarding

import (
	"context"
	"errors"
	"testing"
)

type fakeTenantReader struct {
	ready bool
	err   error
}

func (f fakeTenantReader) HasManagedTenant(context.Context) (bool, error) { return f.ready, f.err }

type fakeOrganizationReader struct {
	school, structure, students bool
	err                         error
}

func (f fakeOrganizationReader) Summary(context.Context, string, ResourceScope) (OrganizationSummary, error) {
	return OrganizationSummary{HasSchool: f.school, HasTeachingStructure: f.structure, HasStudents: f.students}, f.err
}

type fakeUserReader struct {
	ready bool
	err   error
}

func (f fakeUserReader) HasBusinessUser(context.Context, string, ResourceScope) (bool, error) {
	return f.ready, f.err
}

type fakeExamReader struct {
	ready bool
	err   error
}

func (f fakeExamReader) HasExam(context.Context, string, ResourceScope) (bool, error) {
	return f.ready, f.err
}

type fakeSystemReader struct {
	summary SystemReadinessSummary
	err     error
}

func (f fakeSystemReader) Summary(context.Context) (SystemReadinessSummary, error) {
	return f.summary, f.err
}

type fakeGovernanceReader struct {
	summary DataPolicySummary
	err     error
}

func (f fakeGovernanceReader) DataPolicy(context.Context, string) (DataPolicySummary, error) {
	return f.summary, f.err
}

func TestPlatformReadinessRequiresFirstSchoolButNotAI(t *testing.T) {
	service := NewService(Dependencies{
		Tenants:      fakeTenantReader{},
		Organization: fakeOrganizationReader{}, Users: fakeUserReader{}, Exams: fakeExamReader{},
		System:     fakeSystemReader{summary: SystemReadinessSummary{CoreReady: true, AIMode: "local", AIConfigured: true, AIAvailable: false}},
		Governance: fakeGovernanceReader{summary: DataPolicySummary{FallbackMode: "manual_only"}},
	})
	result, err := service.Readiness(context.Background(), Actor{TenantID: "platform", Status: "active", Roles: []string{"platform_admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.ReadyForUse {
		t.Fatal("platform without a school must not be ready")
	}
	assertCheck(t, result, "first_school", CheckAction, SeverityBlocking)
	assertCheck(t, result, "ai_mode", CheckWarning, SeverityRecommended)
	if result.CompletedCount != 2 || result.TotalRequired != 3 {
		t.Fatalf("unexpected blocking progress: %d/%d", result.CompletedCount, result.TotalRequired)
	}
}

func TestPlatformReadinessTreatsPartialReaderFailureAsUnavailable(t *testing.T) {
	service := NewService(Dependencies{
		Tenants: fakeTenantReader{ready: true}, Organization: fakeOrganizationReader{}, Users: fakeUserReader{}, Exams: fakeExamReader{},
		System: fakeSystemReader{err: errors.New("status offline")}, Governance: fakeGovernanceReader{err: errors.New("policy offline")},
	})
	result, err := service.Readiness(context.Background(), Actor{Status: "active", Roles: []string{"platform_admin"}})
	if err != nil {
		t.Fatal(err)
	}
	assertCheck(t, result, "system_core", CheckUnavailable, SeverityBlocking)
	assertCheck(t, result, "ai_mode", CheckUnavailable, SeverityRecommended)
	assertCheck(t, result, "data_policy", CheckUnavailable, SeverityRecommended)
}

func TestSchoolReadinessDoesNotBlockOnFirstExam(t *testing.T) {
	service := NewService(Dependencies{
		Tenants: fakeTenantReader{}, Organization: fakeOrganizationReader{school: true, structure: true, students: true},
		Users: fakeUserReader{ready: true}, Exams: fakeExamReader{}, System: fakeSystemReader{}, Governance: fakeGovernanceReader{},
	})
	result, err := service.Readiness(context.Background(), Actor{TenantID: "tenant-1", Roles: []string{"school_admin"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ReadyForUse {
		t.Fatal("school basics are complete; first exam is only recommended")
	}
	assertCheck(t, result, "first_exam", CheckAction, SeverityRecommended)
	if result.CompletedCount != 4 || result.TotalRequired != 4 {
		t.Fatalf("unexpected blocking progress: %d/%d", result.CompletedCount, result.TotalRequired)
	}
}

func TestSchoolReadinessKeepsIndependentFailuresLocal(t *testing.T) {
	service := NewService(Dependencies{
		Tenants: fakeTenantReader{}, Organization: fakeOrganizationReader{school: true, err: errors.New("organization offline")},
		Users: fakeUserReader{}, Exams: fakeExamReader{}, System: fakeSystemReader{}, Governance: fakeGovernanceReader{},
	})
	result, err := service.Readiness(context.Background(), Actor{Roles: []string{"tenant_admin"}})
	if err != nil {
		t.Fatal(err)
	}
	assertCheck(t, result, "school", CheckUnavailable, SeverityBlocking)
	assertCheck(t, result, "students", CheckUnavailable, SeverityBlocking)
}

func assertCheck(t *testing.T, result OnboardingReadiness, key string, state CheckState, severity Severity) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Key == key {
			if check.State != state || check.Severity != severity {
				t.Fatalf("%s = %s/%s, want %s/%s", key, check.State, check.Severity, state, severity)
			}
			return
		}
	}
	t.Fatalf("missing check %q", key)
}

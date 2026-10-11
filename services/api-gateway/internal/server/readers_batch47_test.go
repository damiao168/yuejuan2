package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/aieligibility"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/dashboard"
	"edugrade-enterprise/services/api-gateway/internal/onboarding"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/qualitydashboard"
)

type batch47OrganizationCall struct {
	operation string
	tenantID  string
	selector  string
	filter    org.StudentListFilter
	ctx       context.Context
}

// 只内嵌 org.Store，避免 MemoryStore 的专用 readiness 能力绕过待验证的列表回退。
type batch47OrganizationStore struct {
	org.Store
	schools  []org.School
	grades   []org.Grade
	classes  []org.Class
	students []org.Student
	calls    []batch47OrganizationCall
	failAt   string
	failure  error
}

func (s *batch47OrganizationStore) record(call batch47OrganizationCall) error {
	s.calls = append(s.calls, call)
	if call.operation == s.failAt {
		return s.failure
	}
	return nil
}

func (s *batch47OrganizationStore) ListSchools(ctx context.Context, tenantID string) ([]org.School, error) {
	err := s.record(batch47OrganizationCall{operation: "schools", tenantID: tenantID, ctx: ctx})
	return s.schools, err
}

func (s *batch47OrganizationStore) ListGrades(ctx context.Context, tenantID, schoolID string) ([]org.Grade, error) {
	err := s.record(batch47OrganizationCall{operation: "grades", tenantID: tenantID, selector: schoolID, ctx: ctx})
	return s.grades, err
}

func (s *batch47OrganizationStore) ListClasses(ctx context.Context, tenantID, gradeID string) ([]org.Class, error) {
	err := s.record(batch47OrganizationCall{operation: "classes", tenantID: tenantID, selector: gradeID, ctx: ctx})
	return s.classes, err
}

func (s *batch47OrganizationStore) ListStudents(ctx context.Context, tenantID string, filter org.StudentListFilter) ([]org.Student, error) {
	err := s.record(batch47OrganizationCall{operation: "students", tenantID: tenantID, filter: filter, ctx: ctx})
	return s.students, err
}

func TestBatch47OnboardingOrganizationFallbackPreservesScope(t *testing.T) {
	cases := []batch47OrganizationScopeCase{
		{name: "allowed_cross_school_structure", scope: onboarding.ResourceScope{SchoolIDs: []string{"school-a", "school-b"}}, want: onboarding.OrganizationSummary{HasSchool: true, HasTeachingStructure: true, HasStudents: true}},
		{name: "class_outside_scope", scope: onboarding.ResourceScope{SchoolIDs: []string{"school-a"}}, want: onboarding.OrganizationSummary{HasSchool: true, HasStudents: true}},
		{name: "empty_whitelist", scope: onboarding.ResourceScope{}},
		{name: "tenant_wide", scope: onboarding.ResourceScope{TenantWide: true}, want: onboarding.OrganizationSummary{HasSchool: true, HasTeachingStructure: true, HasStudents: true}},
		{name: "inactive_allowed_rows_and_active_outside_rows", scope: onboarding.ResourceScope{SchoolIDs: []string{"school-a", "school-b"}}, inactive: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assertBatch47OrganizationScopeCase(t, test)
		})
	}
}

type batch47OrganizationScopeCase struct {
	name     string
	scope    onboarding.ResourceScope
	inactive bool
	want     onboarding.OrganizationSummary
}

// 专用 readiness 缺席时，列表回退仍须保留租户、上下文及学校白名单。
func assertBatch47OrganizationScopeCase(t *testing.T, test batch47OrganizationScopeCase) {
	t.Helper()
	status := "active"
	if test.inactive {
		status = "inactive"
	}
	store := &batch47OrganizationStore{
		schools:  []org.School{{ID: "school-a", Status: "inactive"}, {ID: "outside", Status: "active"}, {ID: "school-a", Status: status}},
		grades:   []org.Grade{{SchoolID: "school-a", Status: "inactive"}, {SchoolID: "outside", Status: "active"}, {SchoolID: "school-a", Status: status}},
		classes:  []org.Class{{SchoolID: "school-b", Status: "inactive"}, {SchoolID: "outside", Status: "active"}, {SchoolID: "school-b", Status: status}},
		students: []org.Student{{SchoolID: "school-a", Status: "inactive"}, {SchoolID: "outside", Status: "active"}, {SchoolID: "school-a", Status: status}},
	}
	if _, ok := any(store).(org.OnboardingReadinessStore); ok {
		t.Fatal("fallback stub unexpectedly implements readiness")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	summary, err := (onboardingOrganizationReader{store: store}).Summary(ctx, "tenant-1", test.scope)
	if err != nil || summary != test.want {
		t.Fatalf("summary=%#v err=%v, want %#v", summary, err, test.want)
	}
	assertBatch47OrganizationCalls(t, store.calls, []string{"schools", "grades", "classes", "students"})
	for _, call := range store.calls {
		if call.tenantID != "tenant-1" || call.ctx != ctx || call.selector != "" || !reflect.DeepEqual(call.filter, org.StudentListFilter{}) {
			t.Fatalf("original read arguments changed: %#v", call)
		}
	}
}

func TestBatch47OnboardingOrganizationFallbackRetainsPartialSummaryAndFirstError(t *testing.T) {
	failure := errors.New("batch47 organization read failed")
	cases := []struct {
		failAt string
		want   onboarding.OrganizationSummary
		calls  []string
	}{
		{failAt: "schools", calls: []string{"schools"}},
		{failAt: "grades", want: onboarding.OrganizationSummary{HasSchool: true}, calls: []string{"schools", "grades"}},
		{failAt: "classes", want: onboarding.OrganizationSummary{HasSchool: true}, calls: []string{"schools", "grades", "classes"}},
		{failAt: "students", want: onboarding.OrganizationSummary{HasSchool: true, HasTeachingStructure: true}, calls: []string{"schools", "grades", "classes", "students"}},
	}
	for _, test := range cases {
		t.Run(test.failAt, func(t *testing.T) {
			store := &batch47OrganizationStore{
				schools: []org.School{{ID: "school-a", Status: "active"}}, grades: []org.Grade{{SchoolID: "school-a", Status: "active"}},
				classes: []org.Class{{SchoolID: "school-a", Status: "active"}}, students: []org.Student{{SchoolID: "school-a", Status: "active"}},
				failAt: test.failAt, failure: failure,
			}
			summary, err := (onboardingOrganizationReader{store: store}).Summary(context.Background(), "tenant-1", onboarding.ResourceScope{SchoolIDs: []string{"school-a"}})
			if err != failure || summary != test.want {
				t.Fatalf("summary=%#v err=%v, want partial=%#v and original error", summary, err, test.want)
			}
			assertBatch47OrganizationCalls(t, store.calls, test.calls)
		})
	}
}

func TestBatch47OnboardingOrganizationReadsClassesWithoutActiveGrade(t *testing.T) {
	store := &batch47OrganizationStore{
		schools: []org.School{{ID: "school-a", Status: "active"}},
		grades:  []org.Grade{{SchoolID: "school-a", Status: "inactive"}},
		classes: []org.Class{{SchoolID: "school-a", Status: "active"}},
	}
	reader := onboardingOrganizationReader{store: store}
	summary, err := reader.Summary(context.Background(), "tenant-1", onboarding.ResourceScope{SchoolIDs: []string{"school-a"}})
	if err != nil || summary != (onboarding.OrganizationSummary{HasSchool: true}) {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	assertBatch47OrganizationCalls(t, store.calls, []string{"schools", "grades", "classes", "students"})
	store.calls = nil
	store.failAt = "classes"
	store.failure = errors.New("batch47 class read failed without grade")
	summary, err = reader.Summary(context.Background(), "tenant-1", onboarding.ResourceScope{SchoolIDs: []string{"school-a"}})
	if err != store.failure || summary != (onboarding.OrganizationSummary{HasSchool: true}) {
		t.Fatalf("class error skipped: summary=%#v err=%v", summary, err)
	}
	assertBatch47OrganizationCalls(t, store.calls, []string{"schools", "grades", "classes"})
}

type batch47ReadinessOrganizationStore struct {
	*batch47OrganizationStore
	summary    org.OnboardingReadinessSummary
	failure    error
	tenantID   string
	schoolIDs  []string
	tenantWide bool
}

func (s *batch47ReadinessOrganizationStore) HasManagedTenant(context.Context) (bool, error) {
	return false, nil
}

func (s *batch47ReadinessOrganizationStore) OnboardingReadiness(ctx context.Context, tenantID string, schoolIDs []string, tenantWide bool) (org.OnboardingReadinessSummary, error) {
	s.calls = append(s.calls, batch47OrganizationCall{operation: "readiness", tenantID: tenantID, ctx: ctx})
	s.tenantID, s.schoolIDs, s.tenantWide = tenantID, append([]string(nil), schoolIDs...), tenantWide
	return s.summary, s.failure
}

func TestBatch47OnboardingOrganizationPrefersReadinessIncludingItsError(t *testing.T) {
	failure := errors.New("batch47 dedicated readiness failed")
	for _, test := range []struct {
		name string
		err  error
	}{{name: "success"}, {name: "error", err: failure}} {
		t.Run(test.name, func(t *testing.T) {
			store := &batch47ReadinessOrganizationStore{
				batch47OrganizationStore: &batch47OrganizationStore{},
				summary:                  org.OnboardingReadinessSummary{HasTeachingStructure: true, HasStudents: true},
				failure:                  test.err,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			scope := onboarding.ResourceScope{SchoolIDs: []string{"school-a", "school-b"}, TenantWide: true}
			summary, err := (onboardingOrganizationReader{store: store}).Summary(ctx, "tenant-1", scope)
			if err != test.err || summary != (onboarding.OrganizationSummary{HasTeachingStructure: true, HasStudents: true}) {
				t.Fatalf("dedicated summary/error changed: summary=%#v err=%v", summary, err)
			}
			assertBatch47OrganizationCalls(t, store.calls, []string{"readiness"})
			if store.tenantID != "tenant-1" || !reflect.DeepEqual(store.schoolIDs, scope.SchoolIDs) || !store.tenantWide || store.calls[0].ctx != ctx {
				t.Fatalf("dedicated scope/context changed: %#v", store)
			}
		})
	}
}

type batch47DashboardReferences struct {
	dashboard.OrganizationSummaryStore
	dashboard.ActivityStore
}

func TestBatch47ProductionStoreGraphPreservesOptionalQueueAndFirstMissingField(t *testing.T) {
	stores := NewMemoryApplicationStores()
	// 校验只检查接口引用；补齐内存工厂的可选槽位，不调用这些替身或宣称生产持久化可用。
	dashboardReferences := &batch47DashboardReferences{}
	stores.Exam.DashboardOrganizations = dashboardReferences
	stores.Exam.DashboardActivities = dashboardReferences
	stores.AIFoundation.Eligibility = aieligibility.NewMemoryStore()
	stores.Grading.QualityDashboard = qualitydashboard.NewService(qualitydashboard.Sources{})
	stores.Capture.OCRQueue = nil
	if err := validatePostgresStoreGraph(stores); err != nil {
		t.Fatalf("configured graph rejected optional OCRQueue: %v", err)
	}
	missing := stores
	missing.Identity.Auth = nil
	missing.Identity.Org = nil
	if err := validatePostgresStoreGraph(missing); err == nil || err.Error() != "production application store stores.Identity.Auth is not configured" {
		t.Fatalf("first missing field changed: %v", err)
	}
	missing = stores
	var typedNil *auth.MemoryStore
	missing.Identity.Auth = typedNil
	if err := validatePostgresStoreGraph(missing); err == nil || err.Error() != "production application store stores.Identity.Auth is not configured" {
		t.Fatalf("typed nil accepted: %v", err)
	}
	missing = stores
	missing.Capture.OCR = nil
	if err := validatePostgresStoreGraph(missing); err == nil || err.Error() != "production application store stores.Capture.OCR is not configured" {
		t.Fatalf("OCRQueue exception expanded to OCR: %v", err)
	}
}

func assertBatch47OrganizationCalls(t *testing.T, calls []batch47OrganizationCall, want []string) {
	t.Helper()
	got := make([]string, 0, len(calls))
	for _, call := range calls {
		got = append(got, call.operation)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%v, want %v", got, want)
	}
}

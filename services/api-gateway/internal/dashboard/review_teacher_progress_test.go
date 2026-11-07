package dashboard

import (
	"context"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

type teacherProgressSummaryStub struct {
	organizationSummaryStub
	calls int
}

func (s *teacherProgressSummaryStub) DashboardReviewTeacherProgress(context.Context, string, auth.AccessScope, time.Time) ([]ReviewTeacherProgress, error) {
	s.calls++
	return []ReviewTeacherProgress{{UserID: "teacher-1", IsOnline: true}, {UserID: "teacher-2"}}, nil
}

func TestTeacherProgressRequiresBothAdministratorAndSchoolManagementScope(t *testing.T) {
	admin := auth.User{ID: "admin", TenantID: "tenant", Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": "school"}}}
	scope, err := auth.ResolveDeclaredAccessScope(admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		user   auth.User
		scope  auth.AccessScope
		called bool
	}{
		{"school administrator", admin, scope, true},
		{"ordinary teacher with school navigation", auth.User{ID: "teacher", TenantID: "tenant", Roles: []string{"grader"}}, scope, false},
		{"navigation school is not management scope", admin, auth.AccessScope{TenantID: "tenant", SchoolIDs: []string{"school"}}, false},
		{"tenant scope cannot enumerate this school view", admin, auth.AccessScope{TenantID: "tenant", TenantWide: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &teacherProgressSummaryStub{}
			service := NewService(Dependencies{Organizations: stub})
			result := Summary{UpdatedAt: time.Now().UTC(), ReviewTeachers: []ReviewTeacherProgress{}, ReviewTeacherSummary: initialReviewTeacherSummary()}
			service.applyReviewTeacherProgress(t.Context(), tc.user, tc.scope, &result)
			if (stub.calls == 1) != tc.called {
				t.Fatalf("store calls=%d want read=%v", stub.calls, tc.called)
			}
			if tc.called && (result.ReviewTeacherSummary.TeacherCount != 2 || result.ReviewTeacherSummary.OnlineCount != 1) {
				t.Fatalf("summary=%#v", result.ReviewTeacherSummary)
			}
			if !tc.called && len(result.ReviewTeachers) != 0 {
				t.Fatal("unauthorized user received colleague progress")
			}
		})
	}
}

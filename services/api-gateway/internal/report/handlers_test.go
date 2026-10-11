package report

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// tenantOnlyScope 复现 CreateManagedUser 为每个通过 POST /api/v1/users 创建的账户写入的 data_scope：
// 这是按角色分键的对象，任何嵌套层级都没有 student_id，因此 ScopedStudentID 无法解析出学生。
func tenantOnlyScope() map[string]any {
	return map[string]any{"student": map[string]any{"scope": "tenant"}}
}

func scopedUser(id string, scope map[string]any, permissions ...string) auth.User {
	if permissions == nil {
		permissions = []string{}
	}
	return auth.User{
		ID:          id,
		TenantID:    "tenant-1",
		Roles:       []string{"student"},
		Permissions: permissions,
		DataScope:   scope,
	}
}

func TestStudentReportDeniesUnresolvableStudentScope(t *testing.T) {
	handler := NewHandler(seededReportStore(), auth.NewMemoryStore())

	cases := []struct {
		name       string
		user       auth.User
		studentID  string
		wantStatus int
	}{
		{
			name:       "tenant only scope cannot read another student report",
			user:       scopedUser("student-user-1", tenantOnlyScope(), "student:report:read"),
			studentID:  "student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "tenant only scope cannot read any report",
			user:       scopedUser("student-user-1", tenantOnlyScope(), "student:report:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "empty data scope cannot read any report",
			user:       scopedUser("student-user-1", nil, "student:report:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nested scope without student_id cannot read any report",
			user:       scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "class", "class_id": "class-a"}}, "student:report:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope cannot read another student report",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}, "student:report:read"),
			studentID:  "student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope without report permission is denied",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope reads own report",
			user:       scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "self", "student_id": "student-1"}}, "student:report:read"),
			studentID:  "student-1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "report permission reads any report",
			user:       scopedUser("teacher-user-1", tenantOnlyScope(), "report:read"),
			studentID:  "student-2",
			wantStatus: http.StatusOK,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/exams/exam-1/reports/students/"+testCase.studentID, nil)
			request.SetPathValue("examId", "exam-1")
			request.SetPathValue("studentId", testCase.studentID)
			request = request.WithContext(auth.WithUser(request.Context(), testCase.user))
			recorder := httptest.NewRecorder()
			handler.StudentReport(recorder, request)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
		})
	}
}

package score

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// tenantOnlyScope reproduces the data_scope that CreateManagedUser writes for
// every account created through POST /api/v1/users: a role-keyed object with no
// student_id at any nesting level, so ScopedStudentID cannot resolve a student.
func tenantOnlyScope() map[string]any {
	return map[string]any{"student": map[string]any{"scope": "tenant"}}
}

func scopedUser(id string, scope map[string]any, permissions ...string) auth.User {
	if permissions == nil {
		permissions = []string{}
	}
	return auth.User{
		ID:          id,
		TenantID:    tenantID,
		Roles:       []string{"student"},
		Permissions: permissions,
		DataScope:   scope,
	}
}

func newPublishedGradeHandler(t *testing.T) *Handler {
	t.Helper()
	store := NewMemoryStore()
	for _, seed := range []SegmentSeed{
		{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5},
		{ExamID: "exam-1", SubmissionID: "submission-2", StudentID: "student-2", AnonymousCode: "ANON-002", AnswerSegmentID: "segment-2", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5},
	} {
		store.AddSegment(seed)
	}
	store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-1", Score: 4, MaxScore: 5})
	store.AddHumanGrade(GradeSeed{AnswerSegmentID: "segment-2", Score: 2, MaxScore: 5})
	if _, err := store.FinalizeExam(context.Background(), tenantID, "exam-1", "manager-1"); err != nil {
		t.Fatalf("finalize exam: %v", err)
	}
	if _, err := store.ConfirmGrades(context.Background(), tenantID, "exam-1", "leader-1", ConfirmInput{Reason: "checked"}); err != nil {
		t.Fatalf("confirm grades: %v", err)
	}
	if _, err := store.PublishGrades(context.Background(), tenantID, "exam-1", "admin-1", PublishInput{Reason: "release"}); err != nil {
		t.Fatalf("publish grades: %v", err)
	}
	return NewHandler(store, auth.NewMemoryStore())
}

func TestGetStudentGradeDeniesUnresolvableStudentScope(t *testing.T) {
	handler := newPublishedGradeHandler(t)

	cases := []struct {
		name       string
		user       auth.User
		studentID  string
		wantStatus int
	}{
		{
			name:       "tenant only scope cannot read another student grade",
			user:       scopedUser("student-user-1", tenantOnlyScope(), "student:grade:read"),
			studentID:  "student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "tenant only scope cannot read any grade",
			user:       scopedUser("student-user-1", tenantOnlyScope(), "student:grade:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "empty data scope cannot read any grade",
			user:       scopedUser("student-user-1", nil, "student:grade:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "nested scope without student_id cannot read any grade",
			user:       scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "class", "class_id": "class-a"}}, "student:grade:read"),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope cannot read another student grade",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}, "student:grade:read"),
			studentID:  "student-2",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope without read permission is denied",
			user:       scopedUser("student-user-1", map[string]any{"student_id": "student-1"}),
			studentID:  "student-1",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "resolved scope reads own grade",
			user:       scopedUser("student-user-1", map[string]any{"student": map[string]any{"scope": "self", "student_id": "student-1"}}, "student:grade:read"),
			studentID:  "student-1",
			wantStatus: http.StatusOK,
		},
		{
			name:       "manage permission reads any grade",
			user:       scopedUser("admin-user-1", tenantOnlyScope(), "score:manage"),
			studentID:  "student-2",
			wantStatus: http.StatusOK,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/"+testCase.studentID, nil)
			request.SetPathValue("examId", "exam-1")
			request.SetPathValue("studentId", testCase.studentID)
			request = request.WithContext(auth.WithUser(request.Context(), testCase.user))
			recorder := httptest.NewRecorder()
			handler.GetStudentGrade(recorder, request)
			if recorder.Code != testCase.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, testCase.wantStatus, recorder.Body.String())
			}
		})
	}
}

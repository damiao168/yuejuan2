package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

func TestStudentPreviewRouteChecksPermissionAndReleaseSchoolBoundary(t *testing.T) {
	password, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, role, school string
		permissions        []string
		student            string
		want               int
	}{
		{"authorized administrator", "school_admin", "school-a", []string{"score:manage"}, "student-1", http.StatusOK},
		{"other school", "school_admin", "school-b", []string{"score:manage"}, "student-1", http.StatusForbidden},
		{"missing permission", "school_admin", "school-a", []string{"system:read"}, "student-1", http.StatusForbidden},
		{"student cannot preview drafts", "student", "school-a", []string{"student:grade:read"}, "student-1", http.StatusForbidden},
		{"student absent from snapshot", "school_admin", "school-a", []string{"score:manage"}, "student-other", http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			identity := auth.NewMemoryStore()
			identity.AddUser(auth.UserWithPassword{User: auth.User{ID: "user-preview", TenantID: "tenant-1", TenantCode: "demo", Username: "preview", DisplayName: "管理员", Status: "active", Roles: []string{tc.role}, Permissions: tc.permissions,
				DataScope: map[string]any{tc.role: map[string]any{"scope": "school", "school_id": tc.school}}}, PasswordHash: password})
			frozen := scorerelease.NewMemoryStore()
			frozen.SeedFacts("exam-1", []scorerelease.SubmissionFact{{StudentID: "student-1", SubmissionID: "submission-1", TotalScore: 4, MaxScore: 5, Status: "confirmed", Questions: []scorerelease.QuestionFact{{QuestionID: "question-1", QuestionNo: "1", FinalGradeID: "internal-grade", Score: 4, MaxScore: 5, SourceType: "human", SourceID: "private-source"}}}})
			release, err := scorerelease.NewService(frozen).Create(context.Background(), "tenant-1", "exam-1", "user-preview", scorerelease.CreateInput{Reason: "preview", IdempotencyKey: "preview-route-key", VisibilityPolicy: scorerelease.VisibilityPolicy{ShowQuestionScores: true}})
			if err != nil {
				t.Fatal(err)
			}
			// 新增预览路由必须穿过与发布详情相同的资源边界，score:manage 不能单独越过学校范围。
			identity.AddResourceBoundary("score_release", release.ID, auth.ResourceBoundary{TenantID: "tenant-1", SchoolID: "school-a", ExamID: "exam-1"})
			stores := NewMemoryApplicationStores()
			stores.Identity.Auth, stores.Release.ScoreRelease = identity, frozen
			router := NewRouterWithApplicationStores(testConfig(), logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), stores)
			token := reviewLogin(t, router, "preview")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, reviewAuthedRequest(http.MethodGet, "/api/v1/score-releases/"+release.ID+"/student-preview?student_id="+tc.student, "", token))
			if recorder.Code != tc.want {
				t.Fatalf("preview got %d want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
			if tc.want == http.StatusOK && (strings.Contains(recorder.Body.String(), "internal-grade") || strings.Contains(recorder.Body.String(), "private-source")) {
				t.Fatal("preview leaked internal grading facts")
			}
		})
	}
}

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

// 学校管理员兼任阅卷也只能访问本校，旧外校分派不能授予材料读取或任务提交权限。
func TestAuditManagementGuardsPreserveTeachingAndAssignedReview(t *testing.T) {
	store := auth.NewMemoryStore()
	actor := auth.User{
		ID: "mixed-manager", TenantID: "tenant", TenantCode: "demo", Username: "mixed-manager", Status: "active",
		Roles:       []string{"school_admin", "teacher", "grader"},
		Permissions: []string{"grading:manage", "score:manage", "review:work"},
		DataScope: map[string]any{
			"school_admin": map[string]any{"scope": "school", "school_id": "school-a"},
			"teacher":      map[string]any{"scope": "class", "school_id": "school-b", "class_id": "class-b", "exam_id": "exam-b"},
			"grader":       map[string]any{"scope": "exam_task", "review_task_id": "review-b", "exam_id": "exam-b"},
		},
	}
	store.AddUser(auth.UserWithPassword{User: actor, SchoolID: "school-a"})
	for _, school := range []string{"a", "b"} {
		store.AddResourceBoundary("exam", "exam-"+school, auth.ResourceBoundary{TenantID: actor.TenantID, SchoolID: "school-" + school, ExamID: "exam-" + school})
	}
	store.AddResourceBoundary("review_task", "review-b", auth.ResourceBoundary{TenantID: actor.TenantID, SchoolID: "school-b", ExamID: "exam-b", AssignedTo: actor.ID})
	store.AddResourceBoundary("review_task", "review-a", auth.ResourceBoundary{TenantID: actor.TenantID, SchoolID: "school-a", ExamID: "exam-a", AssignedTo: actor.ID})
	const token = "synthetic-mixed-manager-session"
	if _, err := store.CreateSession(t.Context(), auth.CreateSessionInput{TenantID: actor.TenantID, UserID: actor.ID, TokenHash: auth.HashToken(token), SessionType: auth.SessionTypeStandard, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	guards := buildRouterGuards(testConfig(), ApplicationModules{Identity: &IdentityModule{AuthStore: store}, Capture: &CaptureProcessingModule{}})
	mux := http.NewServeMux()
	accept := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.Handle("GET /api/v1/exams/{examId}/grading-ai-plan", guards.requireGradingAIPlanManage(accept))
	mux.Handle("GET /api/v1/exams/{examId}/materials", guards.requireAssessmentRead(accept))
	mux.Handle("GET /api/v1/exams/{examId}/grades/export", guards.requireScoreManage(accept))
	mux.Handle("POST /api/v1/exams/{examId}/confirm-grades", guards.requireScoreManage(accept))
	mux.Handle("POST /api/v1/exams/{examId}/publish", guards.requireRecentPermission(permissionScoreManage, accept))
	mux.Handle("POST /api/v1/review-tasks/{id}/submit", guards.requireReviewWork(accept))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/v1/exams/exam-a/grading-ai-plan", http.StatusNoContent},
		{"GET", "/api/v1/exams/exam-b/grading-ai-plan", http.StatusForbidden},
		{"GET", "/api/v1/exams/exam-a/materials", http.StatusNoContent},
		{"GET", "/api/v1/exams/exam-b/materials", http.StatusForbidden},
		{"GET", "/api/v1/exams/exam-a/grades/export", http.StatusNoContent},
		{"GET", "/api/v1/exams/exam-b/grades/export", http.StatusForbidden},
		{"POST", "/api/v1/exams/exam-a/confirm-grades", http.StatusNoContent},
		{"POST", "/api/v1/exams/exam-b/confirm-grades", http.StatusForbidden},
		{"POST", "/api/v1/exams/exam-a/publish", http.StatusNoContent},
		{"POST", "/api/v1/exams/exam-b/publish", http.StatusForbidden},
		{"POST", "/api/v1/review-tasks/review-a/submit", http.StatusNoContent},
		{"POST", "/api/v1/review-tasks/review-b/submit", http.StatusForbidden},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

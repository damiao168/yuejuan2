package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/score"
)

func TestScoreRoutesFinalizeConfirmPublishStudentLookupAndExport(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"score:manage", "student:grade:read"})
	scoreStore := seededRouteScoreStore()
	router := scoreRouter(authStore, scoreStore)
	token := reviewLogin(t, router, "review_manager")

	scoreAssertUnconfirmedPublicationBlocked(t, router, token)

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/confirm-grades", `{"reason":"checked by subject lead"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm expected confirmed, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/quality?stage=publish", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"can_publish":true`) || !strings.Contains(rec.Body.String(), `"passed":true`) {
		t.Fatalf("quality after confirmation expected can publish, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/publish", `{"reason":"approved"}`, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"published"`) || !strings.Contains(rec.Body.String(), `"locked":true`) {
		t.Fatalf("publish expected published locked grades, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/quality?stage=publish", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"can_publish":true`) || strings.Contains(rec.Body.String(), `"grades_not_confirmed"`) {
		t.Fatalf("quality after publish expected passed terminal state, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/students/student-1/exams/exam-1/grade", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total_score":8`) || !strings.Contains(rec.Body.String(), `"items"`) {
		t.Fatalf("student grade expected published detail, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/export", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Body.String(), "watermark") || !strings.Contains(rec.Body.String(), "EduGrade export") {
		t.Fatalf("export expected CSV with watermark, got %d headers=%#v body=%s", rec.Code, rec.Header(), rec.Body.String())
	}

	reviewAssertAuditAction(t, authStore, "score.finalized")
	reviewAssertAuditAction(t, authStore, "score.confirmed")
	reviewAssertAuditAction(t, authStore, "score.published")
	reviewAssertAuditAction(t, authStore, "score.exported")
}

// 先形成待确认成绩，再验证质量门禁与发布均拒绝；此阶段不能跳过直接确认。
func scoreAssertUnconfirmedPublicationBlocked(t *testing.T, router http.Handler, token string) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/finalize", "", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"pending_confirmation"`) || !strings.Contains(rec.Body.String(), `"total_score":8`) {
		t.Fatalf("finalize expected pending confirmation grade, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/quality?stage=publish", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"can_publish":false`) || !strings.Contains(rec.Body.String(), `"grades_not_confirmed"`) {
		t.Fatalf("quality before confirmation expected publish block, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/publish", `{"reason":"too early"}`, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"grades_not_confirmed"`) {
		t.Fatalf("publish before confirmation expected quality conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestScoreRoutesRequirePermission(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"system:read"})
	router := scoreRouter(authStore, seededRouteScoreStore())
	token := reviewLogin(t, router, "review_manager")

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/finalize", "", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("finalize expected 403, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/students/student-1/exams/exam-1/grade", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("student grade expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestStudentGradeRouteEnforcesStudentScope(t *testing.T) {
	authStore := scoreAuthStoreWithStudents(t)
	scoreStore := seededRouteScoreStore()
	router := scoreRouter(authStore, scoreStore)
	managerToken := reviewLogin(t, router, "score_manager")
	studentToken := reviewLogin(t, router, "score_student")
	otherStudentToken := reviewLogin(t, router, "other_student")

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/finalize", "", managerToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("finalize expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/confirm-grades", `{"reason":"checked"}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	req = reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/publish", `{"reason":"approved"}`, managerToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/students/student-1/exams/exam-1/grade", "", studentToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total_score":8`) {
		t.Fatalf("own student grade expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/students/student-1/exams/exam-1/grade", "", otherStudentToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "student_grade_scope_violation") {
		t.Fatalf("other student lookup expected 403 scope violation, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRosterRoutesRequireAdministrativeRole(t *testing.T) {
	authStore := scoreAuthStoreWithStudents(t)
	scoreStore := seededRouteScoreStore()
	scoreStore.AddRosterStudent(score.RosterSeed{
		ExamID: "exam-1", StudentID: "student-1", StudentNo: "001",
		StudentName: "Scoped Student", ClassID: "class-1", ClassName: "Class 1",
	})
	router := scoreRouter(authStore, scoreStore)
	teacherToken := reviewLogin(t, router, "score_manager")

	req := reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/roster", "", teacherToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher must not read administrative roster identities, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPut, "/api/v1/exams/exam-1/roster/student-1/attendance", `{"status":"absent","reason":"not authorized"}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher must not change attendance, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRosterRoutesRejectMalformedIdentifiers(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"score:manage"})
	router := scoreRouter(authStore, seededRouteScoreStore())
	token := reviewLogin(t, router, "review_manager")

	req := reviewAuthedRequest(http.MethodGet, "/api/v1/exams/not-a-uuid/roster", "", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_score_input") {
		t.Fatalf("malformed roster exam id expected 400, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodPut, "/api/v1/exams/00000000-0000-0000-0000-000000000901/roster/not-a-uuid/attendance", `{"status":"absent","reason":"verified"}`, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_score_input") {
		t.Fatalf("malformed roster student id expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func scoreRouter(authStore *auth.MemoryStore, scoreStore score.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Release.Score = scoreStore
	})
}

func seededRouteScoreStore() *score.MemoryStore {
	store := score.NewMemoryStore()
	store.AddSegment(score.SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1", MaxScore: 5})
	store.AddSegment(score.SegmentSeed{ExamID: "exam-1", SubmissionID: "submission-1", StudentID: "student-1", AnonymousCode: "ANON-001", AnswerSegmentID: "segment-2", QuestionID: "question-2", QuestionNo: "Q2", MaxScore: 5})
	store.AddHumanGrade(score.GradeSeed{AnswerSegmentID: "segment-1", Score: 4, MaxScore: 5})
	store.AddRuleGrade(score.GradeSeed{AnswerSegmentID: "segment-2", Score: 4, MaxScore: 5, AutoPass: true})
	return store
}

func scoreAuthStoreWithStudents(t *testing.T) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	for _, user := range []auth.User{
		{
			ID:          reviewManagerID,
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "score_manager",
			DisplayName: "Score Manager",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: []string{"score:manage"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          "00000000-0000-0000-0000-000000000951",
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "score_student",
			DisplayName: "Score Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": "student-1", "synthetic": true},
		},
		{
			ID:          "00000000-0000-0000-0000-000000000952",
			TenantID:    reviewTenantID,
			TenantCode:  "demo",
			Username:    "other_student",
			DisplayName: "Other Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"student:grade:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": "student-2", "synthetic": true},
		},
	} {
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}

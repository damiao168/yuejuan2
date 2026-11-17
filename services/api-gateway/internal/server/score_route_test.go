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
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/score"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
)

func TestScoreRoutesFinalizeConfirmPublishStudentLookupAndExport(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"score:manage", "student:grade:read"})
	scoreStore := seededRouteScoreStore()
	releases := scorerelease.NewMemoryStore()
	router := scoreRouter(t, authStore, scoreStore, releases)
	token := reviewLogin(t, router, "review_manager")

	scoreAssertUnconfirmedPublicationBlocked(t, router, token, scoreStore, releases)

	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/confirm-grades", `{"reason":"checked by subject lead"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"confirmed"`) {
		t.Fatalf("confirm expected confirmed, got %d %s", rec.Code, rec.Body.String())
	}
	syncMemoryPublicationFacts(t, scoreStore, releases, reviewTenantID, "exam-1")

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
	reviewAssertAuditAction(t, authStore, "score_release.published")
	reviewAssertAuditAction(t, authStore, "score.exported")
}

// 先形成待确认成绩。发布前读取（publish 阶段）在可自动准备时按“创建草稿会自动确认”判断，不再报未确认；
// confirmation 阶段读取不给出可发布结论；本内存来源未确认时，统一发布门禁也必须拒绝。
func scoreAssertUnconfirmedPublicationBlocked(t *testing.T, router http.Handler, token string, working score.Store, releases *scorerelease.MemoryStore) {
	t.Helper()
	req := reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/finalize", "", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"pending_confirmation"`) || !strings.Contains(rec.Body.String(), `"total_score":8`) {
		t.Fatalf("finalize expected pending confirmation grade, got %d %s", rec.Code, rec.Body.String())
	}
	syncMemoryPublicationFacts(t, working, releases, reviewTenantID, "exam-1")

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/quality?stage=publish", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"can_publish":true`) || strings.Contains(rec.Body.String(), `"grades_not_confirmed"`) {
		t.Fatalf("publish-stage read expected automatic preparation to confirm pending grades, got %d %s", rec.Code, rec.Body.String())
	}

	req = reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/grades/quality?stage=confirmation", "", token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"stage":"confirmation"`) || !strings.Contains(rec.Body.String(), `"can_publish":false`) {
		t.Fatalf("confirmation-stage read must not report publishable grades, got %d %s", rec.Code, rec.Body.String())
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
	router := scoreRouter(t, authStore, seededRouteScoreStore(), nil)
	token := reviewLogin(t, router, "review_manager")
	// 正式文件同样经过成绩管理 guard，不能因为新增只读导出而绕过学校成绩权限。
	formal := httptest.NewRecorder()
	router.ServeHTTP(formal, reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/score-releases/export", "", token))
	if formal.Code != http.StatusForbidden {
		t.Fatalf("formal export expected 403, got %d %s", formal.Code, formal.Body.String())
	}

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
	releases := scorerelease.NewMemoryStore()
	router := scoreRouter(t, authStore, scoreStore, releases)
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
	syncMemoryPublicationFacts(t, scoreStore, releases, reviewTenantID, "exam-1")
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
	router := scoreRouter(t, authStore, scoreStore, nil)
	teacherToken := reviewLogin(t, router, "score_manager")

	req := reviewAuthedRequest(http.MethodGet, "/api/v1/exams/exam-1/roster", "", teacherToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher must not read administrative roster identities, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRosterRouteRejectsMalformedExamID(t *testing.T) {
	authStore := reviewAuthStore(t, []string{"score:manage"})
	router := scoreRouter(t, authStore, seededRouteScoreStore(), nil)
	token := reviewLogin(t, router, "review_manager")

	req := reviewAuthedRequest(http.MethodGet, "/api/v1/exams/not-a-uuid/roster", "", token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_score_input") {
		t.Fatalf("malformed roster exam id expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func scoreRouter(t *testing.T, authStore *auth.MemoryStore, scoreStore score.Store, releases *scorerelease.MemoryStore) http.Handler {
	t.Helper()
	exams := exam.NewMemoryStore()
	// 兼容首发读取考试的申诉策略，成绩夹具必须对应真实存在的考试。
	item, err := exams.CreateExam(t.Context(), auth.AccessScope{TenantID: reviewTenantID, TenantWide: true}, reviewManagerID,
		exam.CreateInput{SchoolID: "school-1", Name: "Synthetic score exam", Subject: "math", ExamType: "quiz", TotalScore: 10})
	if err != nil || item.ID != "exam-1" {
		t.Fatalf("seed score route exam: %+v %v", item, err)
	}
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Exam.Exam = exams
		stores.Release.Score = scoreStore
		if releases != nil {
			stores.Release.ScoreRelease = releases
		}
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

package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appealpkg "edugrade-enterprise/services/api-gateway/internal/appeal"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
)

const appealTenantID = "00000000-0000-0000-0000-000000000002"
const appealStudentUserID = "00000000-0000-0000-0000-000000000801"
const appealTeacherUserID = "00000000-0000-0000-0000-000000000802"
const appealAdminUserID = "00000000-0000-0000-0000-000000000803"
const appealOriginalGraderUserID = "00000000-0000-0000-0000-000000000804"
const appealTenantAdminUserID = "00000000-0000-0000-0000-000000000805"

func TestAppealRoutesCreateReviewAdjustCloseAndStatistics(t *testing.T) {
	authStore := appealAuthStore(t)
	appealStore := seededRouteAppealStore()
	router := appealRouter(authStore, appealStore)
	studentToken := appealLogin(t, router, "appeal_student")
	adminToken := appealLogin(t, router, "appeal_admin")

	req := appealAuthedRequest(http.MethodPost, "/api/v1/appeals", `{"exam_id":"exam-1","student_id":"student-2","target_type":"question","final_grade_id":"final-1","reason":"not mine"}`, studentToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("student should not appeal another student grade, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals", `{"exam_id":"exam-1","student_id":"student-1","target_type":"question","final_grade_id":"final-1","reason":"Q1 should receive full credit","attachment":{"file_id":"file-1"}}`, studentToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"status":"submitted"`) || !strings.Contains(rec.Body.String(), `"raw_answer":"student wrote a correct explanation"`) {
		t.Fatalf("create appeal expected submitted appeal with evidence, got %d %s", rec.Code, rec.Body.String())
	}
	appealID := decodeAppealID(t, rec.Body.Bytes())

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/review", `{"status":"score_adjusted","reason":"rubric evidence supports full credit","adjusted_score":5,"expected_revision":1}`, adminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"score_adjustment"`) || !strings.Contains(rec.Body.String(), `"adjusted_score":5`) {
		t.Fatalf("review appeal expected score adjustment, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodGet, "/api/v1/appeals/"+appealID, "", studentToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"score_adjusted"`) || !strings.Contains(rec.Body.String(), `"adjustments"`) {
		t.Fatalf("student should see appeal result, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodGet, "/api/v1/appeals/statistics?exam_id=exam-1", "", adminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"score_adjusted_count":1`) {
		t.Fatalf("statistics expected score adjusted count, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/close", `{"reason":"resolved","expected_revision":2}`, adminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"closed"`) {
		t.Fatalf("close appeal expected closed, got %d %s", rec.Code, rec.Body.String())
	}

	reviewAssertAuditAction(t, authStore, "appeal.created")
	reviewAssertAuditAction(t, authStore, "appeal.reviewed")
	reviewAssertAuditAction(t, authStore, "appeal.score_adjusted")
	reviewAssertAuditAction(t, authStore, "appeal.closed")
}

func TestAppealTeacherOnlyWorksAssignedAppealsAndCannotFinalize(t *testing.T) {
	authStore := appealAuthStore(t)
	appealStore := seededRouteAppealStore()
	router := appealRouter(authStore, appealStore)
	studentToken := appealLogin(t, router, "appeal_student")
	teacherToken := appealLogin(t, router, "appeal_teacher")
	adminToken := appealLogin(t, router, "appeal_admin")
	tenantAdminToken := appealLogin(t, router, "appeal_tenant_admin")
	originalGraderToken := appealLogin(t, router, "appeal_original_grader")

	req := appealAuthedRequest(http.MethodPost, "/api/v1/appeals", `{"exam_id":"exam-1","student_id":"student-1","target_type":"question","final_grade_id":"final-1","reason":"Q1 should receive full credit"}`, studentToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create appeal expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	appealID := decodeAppealID(t, rec.Body.Bytes())

	appealAssertAssignmentBoundaries(t, router, appealID, teacherToken, adminToken, tenantAdminToken)
	appealAssertTeacherRecommendationBoundaries(t, router, appealID, teacherToken)

	req = appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", originalGraderToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"appeals":[]`) {
		t.Fatalf("unassigned original grader should have an empty queue, got %d %s", rec.Code, rec.Body.String())
	}

	reviewAssertAuditAction(t, authStore, "appeal.assigned")
	reviewAssertAuditAction(t, authStore, "appeal.teacher_recommendation_submitted")
}

// 未派发时不可读，原阅卷员不可接回申诉；拒绝请求不能推进 expected_revision。
func appealAssertAssignmentBoundaries(t *testing.T, router http.Handler, appealID, teacherToken, adminToken, tenantAdminToken string) {
	t.Helper()
	req := appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", teacherToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"appeals":[]`) {
		t.Fatalf("teacher should have an empty queue before assignment, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodGet, "/api/v1/appeals/"+appealID, "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher should not read an unassigned appeal, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/assign", `{"assigned_to":"`+appealOriginalGraderUserID+`","expected_revision":1}`, adminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("original grader assignment should be forbidden, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/assign", `{"assigned_to":"`+appealTeacherUserID+`","expected_revision":1}`, tenantAdminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("canonical tenant administrator must not manage appeals, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/assign", `{"assigned_to":"`+appealTeacherUserID+`","expected_revision":1}`, adminToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"assigned_to":"`+appealTeacherUserID+`"`) {
		t.Fatalf("admin assignment expected assigned appeal, got %d %s", rec.Code, rec.Body.String())
	}
}

// 教师只看匿名证据并提出建议；建议提交后仍不能关闭申诉或修改最终分数。
func appealAssertTeacherRecommendationBoundaries(t *testing.T, router http.Handler, appealID, teacherToken string) {
	t.Helper()
	req := appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", teacherToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"`+appealID+`"`) || !strings.Contains(rec.Body.String(), `"anonymous_code":"ANON-001"`) || strings.Contains(rec.Body.String(), `"student_id"`) {
		t.Fatalf("teacher should see assigned anonymized appeal, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodGet, "/api/v1/appeals/"+appealID, "", teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), appealOriginalGraderUserID) || strings.Contains(rec.Body.String(), `"student_id"`) {
		t.Fatalf("teacher detail should redact student and original reviewer identities, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/review", `{"status":"score_adjusted","reason":"direct change","adjusted_score":5,"expected_revision":2}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher must not call final review endpoint, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/recommendation", `{"recommendation":"adjust_score","reason":"rubric supports full credit","recommended_score":5,"expected_revision":2}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"teacher_recommendation":"adjust_score"`) || !strings.Contains(rec.Body.String(), `"teacher_recommended_score":5`) || !strings.Contains(rec.Body.String(), `"score":4`) {
		t.Fatalf("teacher recommendation should not mutate final score, got %d %s", rec.Code, rec.Body.String())
	}

	req = appealAuthedRequest(http.MethodPost, "/api/v1/appeals/"+appealID+"/close", `{"reason":"teacher cannot close","expected_revision":3}`, teacherToken)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("teacher must not close appeal, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAppealRoutesRequirePermission(t *testing.T) {
	authStore := appealAuthStoreWithPermissions(t, []string{"system:read"}, map[string]any{"student_id": "student-1"})
	router := appealRouter(authStore, seededRouteAppealStore())
	token := appealLogin(t, router, "appeal_student")

	req := appealAuthedRequest(http.MethodPost, "/api/v1/appeals", `{"exam_id":"exam-1","student_id":"student-1","target_type":"exam","reason":"score issue"}`, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("create appeal expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func appealRouter(authStore *auth.MemoryStore, appealStore appealpkg.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Release.Appeal = appealStore
	})
}

func appealAuthStore(t *testing.T) *auth.MemoryStore {
	store := auth.NewMemoryStore()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := []auth.User{
		{
			ID:          appealStudentUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_student",
			DisplayName: "Appeal Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: []string{"appeal:create", "appeal:read"},
			DataScope:   map[string]any{"scope": "self", "student_id": "student-1", "synthetic": true},
		},
		{
			ID:          appealTeacherUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_teacher",
			DisplayName: "Appeal Teacher",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: []string{"appeal:read", "appeal:work"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          appealAdminUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_admin",
			DisplayName: "Appeal Admin",
			Status:      "active",
			Roles:       []string{"school_admin"},
			Permissions: []string{"appeal:read", "appeal:manage"},
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		{
			ID:          appealTenantAdminUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_tenant_admin",
			DisplayName: "Appeal Tenant Admin",
			Status:      "active",
			Roles:       []string{"tenant_admin"},
			Permissions: []string{"org:manage", "exam:manage", "dashboard:read"},
			DataScope:   map[string]any{"scope": "tenant"},
		},
		{
			ID:          appealOriginalGraderUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_original_grader",
			DisplayName: "Original Grader",
			Status:      "active",
			Roles:       []string{"grader"},
			Permissions: []string{"appeal:read", "appeal:work"},
			DataScope:   map[string]any{"scope": "assigned", "synthetic": true},
		},
	}
	for _, user := range users {
		store.AddUser(auth.UserWithPassword{User: user, PasswordHash: hash})
	}
	return store
}

func appealAuthStoreWithPermissions(t *testing.T, permissions []string, dataScope map[string]any) *auth.MemoryStore {
	store := auth.NewMemoryStore()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          appealStudentUserID,
			TenantID:    appealTenantID,
			TenantCode:  "demo",
			Username:    "appeal_student",
			DisplayName: "Appeal Student",
			Status:      "active",
			Roles:       []string{"student"},
			Permissions: permissions,
			DataScope:   dataScope,
		},
		PasswordHash: hash,
	})
	return store
}

func appealLogin(t *testing.T, router http.Handler, username string) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": username, "password": "ChangeMe123!"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/token", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return response.AccessToken
}

func appealAuthedRequest(method string, path string, body string, token string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func decodeAppealID(t *testing.T, raw []byte) string {
	t.Helper()
	var response struct {
		Appeal struct {
			ID string `json:"id"`
		} `json:"appeal"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatalf("decode appeal: %v", err)
	}
	if response.Appeal.ID == "" {
		t.Fatalf("missing appeal id in %s", string(raw))
	}
	return response.Appeal.ID
}

func seededRouteAppealStore() *appealpkg.MemoryStore {
	store := appealpkg.NewMemoryStore()
	store.AddSubmissionGrade(appealpkg.SubmissionGradeSeed{
		ID:            "submission-grade-1",
		ExamID:        "exam-1",
		ExamName:      "期中考试",
		Subject:       "数学",
		SubmissionID:  "submission-1",
		StudentID:     "student-1",
		AnonymousCode: "ANON-001",
		TotalScore:    8,
		MaxScore:      10,
		Status:        "published",
		Locked:        true,
	})
	store.AddFinalGrade(appealpkg.FinalGradeSeed{
		ID:              "final-1",
		ExamID:          "exam-1",
		SubmissionID:    "submission-1",
		AnswerSegmentID: "segment-1",
		QuestionID:      "question-1",
		QuestionNo:      "Q1",
		Score:           4,
		MaxScore:        5,
		Status:          "locked",
		Locked:          true,
		RawAnswer:       "student wrote a correct explanation",
		OCRText:         "student wrote a correct explanation",
		AIGrades:        []map[string]any{{"suggested_score": 4.0, "mock": false}},
		HumanGrades:     []map[string]any{{"score": 4.0, "reviewer_id": appealOriginalGraderUserID}},
		Rubric:          map[string]any{"points": []any{"main idea"}},
	})
	return store
}

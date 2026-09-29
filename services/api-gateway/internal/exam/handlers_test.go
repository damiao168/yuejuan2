package exam_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/org"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
)

func TestCreateListDetailExam(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	examStore := exam.NewMemoryStore()
	router := testRouter(authStore, examStore)
	token := login(t, router)

	created := createExam(t, router, token)
	if created.Status != "draft" {
		t.Fatalf("expected draft status, got %s", created.Status)
	}
	if len(created.ClassIDs) != 2 {
		t.Fatalf("expected class ids to be persisted, got %#v", created.ClassIDs)
	}

	listReq := authedRequest(http.MethodGet, "/api/v1/exams", nil, token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), created.ID) {
		t.Fatalf("expected created exam in list, got %d %s", listRec.Code, listRec.Body.String())
	}

	detailReq := authedRequest(http.MethodGet, "/api/v1/exams/"+created.ID, nil, token)
	detailRec := httptest.NewRecorder()
	router.ServeHTTP(detailRec, detailReq)
	if detailRec.Code != http.StatusOK || !strings.Contains(detailRec.Body.String(), `"name":"高二物理期末考试"`) {
		t.Fatalf("expected exam detail, got %d %s", detailRec.Code, detailRec.Body.String())
	}
}

func TestRefreshCandidateSnapshotOnlyWhileExamIsEditable(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	store := exam.NewMemoryStore()
	router := testRouter(authStore, store)
	token := login(t, router)
	created := createExam(t, router, token)

	req := authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/candidates/refresh", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"before_count":0`) {
		t.Fatalf("draft candidate refresh expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	if err := store.SetStatusForTest("tenant-exam", created.ID, "ready"); err != nil {
		t.Fatalf("seed ready exam: %v", err)
	}
	req = authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/candidates/refresh", nil, token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "exam_candidates_frozen") {
		t.Fatalf("ready candidate refresh expected frozen conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCreateMultiSubjectExamSession(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	router := testRouter(authStore, exam.NewMemoryStore())
	token := login(t, router)
	payload := `{
  "school_id":"school-1","grade_id":"grade-1","name":"高二期中考试","exam_type":"midterm_exam",
  "template_id":"00000000-0000-0000-0000-000000000601",
  "grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"after_admin_approval",
  "class_ids":["class-1","class-2"],
  "subjects":[
    {"subject":"math","total_score":100,"duration_minutes":90,"candidate_rule":"all_selected_classes","class_ids":[],"sections":[{"title":"客观题","question_type":"single_choice","question_count":10,"score_per_question":4},{"title":"解答题","question_type":"calculation","question_count":6,"score_per_question":10}]},
    {"subject":"physics","total_score":100,"duration_minutes":75,"candidate_rule":"subject_selected_classes","class_ids":["class-1"],"sections":[{"title":"全卷","question_type":"short_answer","question_count":10,"score_per_question":10}]}
  ]
}`
	req := authedRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewBufferString(payload), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create exam session expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Session exam.ExamSession `json:"exam_session"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Session.Exams) != 2 || response.Session.Exams[0].Subject != "math" || len(response.Session.Exams[1].ClassIDs) != 1 {
		t.Fatalf("unexpected session children: %#v", response.Session.Exams)
	}
	if response.Session.TemplateID != "00000000-0000-0000-0000-000000000601" || response.Session.TemplateVersion != 1 {
		t.Fatalf("expected selected template version to be recorded, got %#v", response.Session)
	}
	listReq := authedRequest(http.MethodGet, "/api/v1/exams", nil, token)
	listRec := httptest.NewRecorder()
	router.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list subject exams expected 200, got %d %s", listRec.Code, listRec.Body.String())
	}
	var listed struct {
		Exams []exam.Exam `json:"exams"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode subject exams: %v", err)
	}
	if len(listed.Exams) != 2 {
		t.Fatalf("expected both subject exams in list, got %#v", listed.Exams)
	}
	for _, child := range listed.Exams {
		if child.SessionID != response.Session.ID || child.SessionName != response.Session.Name || child.SessionGradeID != response.Session.GradeID {
			t.Fatalf("missing parent exam information on subject %s: %#v", child.Subject, child)
		}
	}
}

func TestListExamTemplatesFiltersEducationStage(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), exam.NewMemoryStore())
	token := login(t, router)
	req := authedRequest(http.MethodGet, "/api/v1/exam-templates?education_stage=junior", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list templates expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "system.junior.standard") || strings.Contains(rec.Body.String(), "system.senior.standard") {
		t.Fatalf("expected only junior templates, got %s", rec.Body.String())
	}
}

func TestExamSessionRejectsScoreMismatch(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), exam.NewMemoryStore())
	token := login(t, router)
	payload := `{"school_id":"school-1","grade_id":"grade-1","name":"测试","exam_type":"quiz","grading_mode":"ai_assisted","publish_policy":"after_admin_approval","class_ids":["class-1"],"subjects":[{"subject":"math","total_score":100,"duration_minutes":60,"sections":[{"title":"全卷","question_type":"short_answer","question_count":9,"score_per_question":10}]}]}`
	req := authedRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewBufferString(payload), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_exam_session") {
		t.Fatalf("score mismatch expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestExamSessionAllowsSubjectsWithoutPaperSections(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), exam.NewMemoryStore())
	token := login(t, router)
	payload := `{"school_id":"school-1","grade_id":"grade-1","name":"轻量创建测试","exam_type":"quiz","grading_mode":"ai_assisted","publish_policy":"after_admin_approval","class_ids":["class-1"],"subjects":[{"subject":"math","total_score":100,"duration_minutes":60,"sections":[]}]}`
	req := authedRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewBufferString(payload), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("session without sections expected 201, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestExamSessionRejectsMismatchedCommandIdentity(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), exam.NewMemoryStore())
	token := login(t, router)
	payload := `{"school_id":"school-1","grade_id":"grade-1","name":"测试","exam_type":"quiz","grading_mode":"ai_assisted","publish_policy":"after_admin_approval","class_ids":["class-1"],"command_id":"command-in-body","subjects":[{"subject":"math","total_score":100,"duration_minutes":60,"sections":[{"title":"全卷","question_type":"short_answer","question_count":10,"score_per_question":10}]}]}`
	req := authedRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewBufferString(payload), token)
	req.Header.Set("Idempotency-Key", "command-in-header")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "command_id") {
		t.Fatalf("mismatched command identity expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestInvalidStatusTransitionRejected(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	router := testRouter(authStore, exam.NewMemoryStore())
	token := login(t, router)
	created := createExam(t, router, token)

	req := authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/status", bytes.NewBufferString(`{"status":"published","expected_revision":1}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for invalid transition, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestFinalizedExamCannotPublishThroughStatusRoute(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	store := exam.NewMemoryStore()
	router := testRouter(authStore, store)
	token := login(t, router)
	created := createExam(t, router, token)
	if err := store.SetStatusForTest("tenant-exam", created.ID, "finalized"); err != nil {
		t.Fatalf("seed finalized exam: %v", err)
	}

	req := authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/status", bytes.NewBufferString(`{"status":"published","expected_revision":2}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "invalid_status_transition") {
		t.Fatalf("generic status route must not bypass score publication workflow, got %d %s", rec.Code, rec.Body.String())
	}
	unchanged, err := store.GetExam(t.Context(), tenantScope("tenant-exam", "u-exam"), created.ID)
	if err != nil || unchanged.Status != "finalized" {
		t.Fatalf("rejected publication must leave exam finalized, exam=%#v err=%v", unchanged, err)
	}
}

func TestPublishedExamCannotBeModified(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	store := exam.NewMemoryStore()
	router := testRouter(authStore, store)
	token := login(t, router)
	created := createExam(t, router, token)

	if err := store.SetStatusForTest("tenant-exam", created.ID, "published"); err != nil {
		t.Fatalf("seed published exam: %v", err)
	}

	req := authedRequest(http.MethodPatch, "/api/v1/exams/"+created.ID, bytes.NewBufferString(`{"name":"不应修改","expected_revision":2}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected published exam update to be rejected, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestExamUpdateRejectsChangedRosterAfterReadiness(t *testing.T) {
	statuses := []string{"ready", "collecting", "grading", "reviewing", "finalized"}
	for _, status := range statuses {
		t.Run(status, func(t *testing.T) {
			store := exam.NewMemoryStore()
			router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), store)
			token := login(t, router)
			created := createExam(t, router, token)
			if err := store.SetStatusForTest("tenant-exam", created.ID, status); err != nil {
				t.Fatalf("seed %s exam: %v", status, err)
			}

			// 进入准备完成阶段后，真正改变班级集合必须返回冻结错误，并保留原集合。
			req := authedRequest(http.MethodPatch, "/api/v1/exams/"+created.ID, bytes.NewBufferString(`{"class_ids":["class-1"],"expected_revision":2}`), token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "exam_candidates_frozen") {
				t.Fatalf("changed roster in %s expected frozen conflict, got %d %s", status, rec.Code, rec.Body.String())
			}
			unchanged, err := store.GetExam(t.Context(), tenantScope("tenant-exam", "u-exam"), created.ID)
			if err != nil || len(unchanged.ClassIDs) != 2 {
				t.Fatalf("rejected roster update changed stored classes: %#v err=%v", unchanged.ClassIDs, err)
			}

			// 顺序或重复变化代表同一集合，不应触发名册重建，也不应误判为冻结后的变更。
			req = authedRequest(http.MethodPatch, "/api/v1/exams/"+created.ID, bytes.NewBufferString(`{"class_ids":["class-2","class-1","class-1"],"expected_revision":2}`), token)
			rec = httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("equivalent roster in %s expected 200, got %d %s", status, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestCollectionCannotBypassReadinessGate(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	router := testRouter(authStore, exam.NewMemoryStore())
	token := login(t, router)
	created := createExam(t, router, token)

	revision := int64(1)
	for _, status := range []string{"configured", "collecting"} {
		req := authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/status", bytes.NewBufferString(fmt.Sprintf(`{"status":%q,"expected_revision":%d}`, status, revision)), token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if status == "configured" && rec.Code != http.StatusOK {
			t.Fatalf("draft to configured expected 200, got %d %s", rec.Code, rec.Body.String())
		}
		if rec.Code == http.StatusOK {
			revision++
		}
		if status == "collecting" && rec.Code != http.StatusConflict {
			t.Fatalf("configured to collecting must require readiness gate, got %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestArchiveExam(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	router := testRouter(authStore, exam.NewMemoryStore())
	token := login(t, router)
	created := createExam(t, router, token)

	req := authedRequest(http.MethodPost, "/api/v1/exams/"+created.ID+"/archive", bytes.NewBufferString(`{"expected_revision":1}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"archived"`) {
		t.Fatalf("expected archive success, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestExamPermissionDenied(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"system:read"})
	router := testRouter(authStore, exam.NewMemoryStore())
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/exams", bytes.NewBufferString(validCreateExamJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestExamTenantIsolation(t *testing.T) {
	store := exam.NewMemoryStore()
	ctx := context.Background()
	if _, err := store.CreateExam(ctx, tenantScope("tenant-a", "user-a"), "user-a", exam.CreateInput{SchoolID: "school-a", Name: "A", Subject: "physics", ExamType: "formal_exam", TotalScore: 100, GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval"}); err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := store.CreateExam(ctx, tenantScope("tenant-b", "user-b"), "user-b", exam.CreateInput{SchoolID: "school-b", Name: "B", Subject: "math", ExamType: "formal_exam", TotalScore: 100, GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval"}); err != nil {
		t.Fatalf("create b: %v", err)
	}
	items, err := store.ListExams(ctx, tenantScope("tenant-a", "user-a"), exam.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].Name != "A" {
		t.Fatalf("tenant isolation failed: %#v", items)
	}
}

func testRouter(authStore *auth.MemoryStore, examStore *exam.MemoryStore) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewRouter(cfg, logger.New(io.Discard, "error"), nil, authStore, org.NewMemoryStore(), examStore, paper.NewMemoryStore())
}

func authStoreWithPermissions(t *testing.T, permissions []string) *auth.MemoryStore {
	t.Helper()
	hash, err := auth.HashPassword("ChangeMe123!")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := auth.NewMemoryStore()
	store.AddUser(auth.UserWithPassword{
		User: auth.User{
			ID:          "u-exam",
			TenantID:    "tenant-exam",
			TenantCode:  "demo",
			Username:    "school_admin",
			DisplayName: "School Admin",
			Status:      "active",
			Roles:       []string{"school_admin"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "school_id": "school-1", "class_ids": []any{"class-1", "class-2"}, "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "school_admin", "password": "ChangeMe123!"})
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
		t.Fatalf("decode: %v", err)
	}
	return response.AccessToken
}

func createExam(t *testing.T, router http.Handler, token string) exam.Exam {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/exams", bytes.NewBufferString(validCreateExamJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create exam expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Exam exam.Exam `json:"exam"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Exam
}

func authedRequest(method string, path string, body *bytes.Buffer, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func validCreateExamJSON() string {
	return `{"school_id":"school-1","name":"高二物理期末考试","subject":"physics","exam_type":"formal_exam","total_score":100,"grading_mode":"ai_assisted","appeal_enabled":true,"publish_policy":"after_admin_approval","class_ids":["class-1","class-2"]}`
}

func tenantScope(tenantID, actorID string) auth.AccessScope {
	return auth.AccessScope{TenantID: tenantID, ActorID: actorID, TenantWide: true}
}

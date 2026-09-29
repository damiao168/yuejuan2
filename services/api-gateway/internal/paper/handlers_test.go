package paper_test

import (
	"bytes"
	"encoding/json"
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

func TestCreatePaperAndQuestion(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	router := testRouter(authStore, paperStore)
	token := login(t, router)

	paperResp := createPaper(t, router, token, "exam-1")
	if paperResp.VersionNo != 1 {
		t.Fatalf("expected paper version 1, got %d", paperResp.VersionNo)
	}

	question := createQuestion(t, router, token, "exam-1", 100)
	if question.QuestionType != "short_answer" {
		t.Fatalf("unexpected question: %#v", question)
	}

	req := authedRequest(http.MethodGet, "/api/v1/exams/exam-1/questions", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), question.ID) {
		t.Fatalf("expected question in list, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCreatePaperFromUploadedFileAssetID(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	router := testRouter(authStore, paperStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/papers", bytes.NewBufferString(`{"file_asset_id":"file-upload-1"}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create paper from file_asset_id expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Paper paper.Paper `json:"paper"`
		Note  string      `json:"note"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Paper.FileAssetID != "file-upload-1" {
		t.Fatalf("expected linked file asset id, got %#v", response.Paper)
	}
	if !strings.Contains(response.Note, "linked") {
		t.Fatalf("expected linked note, got %q", response.Note)
	}
}

func TestExamTemplateBindingUsesExistingLockedTemplateEndpoint(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	store := paper.NewMemoryStore()
	router := testRouter(authStore, store)
	token := login(t, router)
	paperVersion := createPaper(t, router, token, "exam-1")
	question := createQuestion(t, router, token, "exam-1", 10)
	payload, _ := json.Marshal(map[string]any{
		"exam_paper_id": paperVersion.ID, "name": "本场模板", "page_count": 1,
		"layout": map[string]any{"pages": []any{map[string]any{
			"page_no": 1, "width": 1000, "height": 1400, "registration_marks": []any{}, "identity_regions": []any{},
			"question_regions": []any{map[string]any{"id": "q1", "question_id": question.ID, "label": "Q1", "x": .1, "y": .1, "width": .8, "height": .2}},
		}}},
	})
	createReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBuffer(payload), token)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Template paper.AnswerSheetTemplate `json:"template"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	lockReq := authedRequest(http.MethodPost, "/api/v1/answer-sheet-templates/"+created.Template.ID+"/lock", nil, token)
	lockRec := httptest.NewRecorder()
	router.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("lock template: %d %s", lockRec.Code, lockRec.Body.String())
	}

	bindReq := authedRequest(http.MethodPut, "/api/v1/exams/exam-1/answer-sheet-template-binding", bytes.NewBufferString(`{"template_id":"`+created.Template.ID+`","mode":"locked_with_guard","expected_revision":0}`), token)
	bindRec := httptest.NewRecorder()
	router.ServeHTTP(bindRec, bindReq)
	if bindRec.Code != http.StatusOK {
		t.Fatalf("bind template: %d %s", bindRec.Code, bindRec.Body.String())
	}
	var bound struct {
		Binding paper.ExamTemplateBinding `json:"binding"`
	}
	if err := json.Unmarshal(bindRec.Body.Bytes(), &bound); err != nil {
		t.Fatal(err)
	}
	if bound.Binding.TemplateID != created.Template.ID || bound.Binding.Mode != "locked_with_guard" {
		t.Fatalf("unexpected binding: %#v", bound.Binding)
	}

	unbindReq := authedRequest(http.MethodDelete, "/api/v1/exams/exam-1/answer-sheet-template-binding", bytes.NewBufferString(`{"expected_revision":1,"reason":"测试解除"}`), token)
	unbindRec := httptest.NewRecorder()
	router.ServeHTTP(unbindRec, unbindReq)
	if unbindRec.Code != http.StatusOK {
		t.Fatalf("unbind template: %d %s", unbindRec.Code, unbindRec.Body.String())
	}
	getReq := authedRequest(http.MethodGet, "/api/v1/exams/exam-1/answer-sheet-template-binding", nil, token)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || !strings.Contains(getRec.Body.String(), `"binding":null`) {
		t.Fatalf("expected unbound response, got %d %s", getRec.Code, getRec.Body.String())
	}
}

func TestInvalidQuestionTypeRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), paper.NewMemoryStore())
	token := login(t, router)

	body := `{"question_no":"Q1","question_type":"magic","score":10}`
	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/questions", bytes.NewBufferString(body), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestReadyExamRejectsAllOfficialPaperMutations(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	store := paper.NewMemoryStore()
	router := testRouter(authStore, store)
	token := login(t, router)
	paperVersion := createPaper(t, router, token, "exam-1")
	qUpdate := createQuestion(t, router, token, "exam-1", 10)
	qDelete := createQuestionWithNumber(t, router, token, "exam-1", "Q2", 10)
	qRubric := createQuestionWithNumber(t, router, token, "exam-1", "Q3", 10)

	templatePayload, _ := json.Marshal(map[string]any{
		"exam_paper_id": paperVersion.ID, "name": "冻结测试模板", "page_count": 1,
		"layout": map[string]any{"pages": []any{map[string]any{
			"page_no": 1, "width": 1000, "height": 1400, "registration_marks": []any{}, "identity_regions": []any{},
			"question_regions": []any{map[string]any{"id": "q1", "question_id": qUpdate.ID, "label": "Q1", "x": .1, "y": .1, "width": .8, "height": .2}},
		}}},
	})
	createTemplateReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBuffer(templatePayload), token)
	createTemplateRec := httptest.NewRecorder()
	router.ServeHTTP(createTemplateRec, createTemplateReq)
	if createTemplateRec.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", createTemplateRec.Code, createTemplateRec.Body.String())
	}
	var templateResponse struct {
		Template paper.AnswerSheetTemplate `json:"template"`
	}
	if err := json.Unmarshal(createTemplateRec.Body.Bytes(), &templateResponse); err != nil {
		t.Fatal(err)
	}
	var updateTemplate map[string]any
	if err := json.Unmarshal(templatePayload, &updateTemplate); err != nil {
		t.Fatal(err)
	}
	delete(updateTemplate, "exam_paper_id")
	updateTemplate["expected_revision"] = 1
	updateTemplatePayload, _ := json.Marshal(updateTemplate)

	store.SetReadinessContext("exam-1", 30, 1, 1, "ready")
	requests := []struct{ name, method, path, body string }{
		{"create", http.MethodPost, "/api/v1/exams/exam-1/questions", `{"question_no":"Q4","question_type":"short_answer","score":10}`},
		{"update", http.MethodPatch, "/api/v1/questions/" + qUpdate.ID, `{"stem":"changed"}`},
		{"answer", http.MethodPatch, "/api/v1/questions/" + qUpdate.ID, `{"answer_key":{"standard_answer":"x","equivalent_answers":[],"tolerance":{}}}`},
		{"delete", http.MethodDelete, "/api/v1/questions/" + qDelete.ID, ""},
		{"rubric", http.MethodPost, "/api/v1/questions/" + qRubric.ID + "/rubric", validRubricJSON("draft", 10)},
		{"template-update", http.MethodPatch, "/api/v1/answer-sheet-templates/" + templateResponse.Template.ID, string(updateTemplatePayload)},
		{"template-lock", http.MethodPost, "/api/v1/answer-sheet-templates/" + templateResponse.Template.ID + "/lock", ""},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			req := authedRequest(tc.method, tc.path, bytes.NewBufferString(tc.body), token)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"exam_frozen"`) {
				t.Fatalf("expected frozen 409, got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestQuestionRejectsPaperFromDifferentExam(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	router := testRouter(authStore, paperStore)
	token := login(t, router)
	paperResp := createPaper(t, router, token, "exam-1")

	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-2/questions", bytes.NewBufferString(validQuestionJSONWithPaper(10, paperResp.ID)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected cross-exam paper reference to be rejected, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRubricVersioningAndLock(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), paper.NewMemoryStore())
	token := login(t, router)
	question := createQuestion(t, router, token, "exam-1", 10)

	r1 := createRubric(t, router, token, question.ID, "draft", 10)
	if r1.Version != "v1" {
		t.Fatalf("expected v1, got %s", r1.Version)
	}
	r2 := createRubric(t, router, token, question.ID, "locked", 10)
	if r2.Version != "v2" || r2.Status != "locked" {
		t.Fatalf("expected locked v2, got %#v", r2)
	}

	req := authedRequest(http.MethodPost, "/api/v1/questions/"+question.ID+"/rubric", bytes.NewBufferString(validRubricJSON("draft", 10)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected locked rubric conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRubricScoreMismatchRejected(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"exam:manage"}), paper.NewMemoryStore())
	token := login(t, router)
	question := createQuestion(t, router, token, "exam-1", 10)

	req := authedRequest(http.MethodPost, "/api/v1/questions/"+question.ID+"/rubric", bytes.NewBufferString(validRubricJSON("draft", 8)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected mismatch 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestValidatePaperConfigFindsScoreMismatch(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	paperStore.SetExamTotal("exam-1", 100)
	router := testRouter(authStore, paperStore)
	token := login(t, router)
	createQuestion(t, router, token, "exam-1", 10)

	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/validate-paper-config", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "total_score_mismatch") {
		t.Fatalf("expected score mismatch issue, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestPaperPermissionDenied(t *testing.T) {
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), paper.NewMemoryStore())
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/questions", bytes.NewBufferString(validQuestionJSON(10)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestAnswerTemplateReadinessGateAndInvalidation(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	paperStore.SetReadinessContext("exam-1", 10, 1, 30, "configured")
	router := testRouter(authStore, paperStore)
	token := login(t, router)
	paperVersion := createPaper(t, router, token, "exam-1")
	question := createQuestion(t, router, token, "exam-1", 10)
	createRubric(t, router, token, question.ID, "locked", 10)

	readinessReq := authedRequest(http.MethodGet, "/api/v1/exams/exam-1/readiness", nil, token)
	readinessRec := httptest.NewRecorder()
	router.ServeHTTP(readinessRec, readinessReq)
	if readinessRec.Code != http.StatusOK || !strings.Contains(readinessRec.Body.String(), `"ready":false`) || !strings.Contains(readinessRec.Body.String(), `"locked_template"`) {
		t.Fatalf("readiness must block without template, got %d %s", readinessRec.Code, readinessRec.Body.String())
	}

	payload := map[string]any{
		"exam_paper_id": paperVersion.ID,
		"name":          "A 卷答题模板",
		"page_count":    1,
		"layout": map[string]any{"pages": []any{map[string]any{
			"page_no": 1, "width": 2480, "height": 3508,
			"registration_marks": []any{}, "identity_regions": []any{},
			"question_regions": []any{map[string]any{"id": "q1", "question_id": question.ID, "label": "Q1", "x": 0.1, "y": 0.2, "width": 0.7, "height": 0.2}},
		}}},
	}
	raw, _ := json.Marshal(payload)
	createReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBuffer(raw), token)
	createRec := httptest.NewRecorder()
	router.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create template expected 201, got %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Template paper.AnswerSheetTemplate `json:"template"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode template: %v", err)
	}

	lockReq := authedRequest(http.MethodPost, "/api/v1/answer-sheet-templates/"+created.Template.ID+"/lock", nil, token)
	lockRec := httptest.NewRecorder()
	router.ServeHTTP(lockRec, lockReq)
	if lockRec.Code != http.StatusOK {
		t.Fatalf("lock template expected 200, got %d %s", lockRec.Code, lockRec.Body.String())
	}

	confirmReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/readiness/confirm", nil, token)
	confirmRec := httptest.NewRecorder()
	router.ServeHTTP(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK || !strings.Contains(confirmRec.Body.String(), `"confirmed":true`) || paperStore.ExamStatus("exam-1") != "ready" {
		t.Fatalf("confirm expected ready exam, got %d %s status=%s", confirmRec.Code, confirmRec.Body.String(), paperStore.ExamStatus("exam-1"))
	}

	cloneReq := authedRequest(http.MethodPost, "/api/v1/answer-sheet-templates/"+created.Template.ID+"/clone", nil, token)
	cloneRec := httptest.NewRecorder()
	router.ServeHTTP(cloneRec, cloneReq)
	if cloneRec.Code != http.StatusConflict || !strings.Contains(cloneRec.Body.String(), `"exam_frozen"`) || paperStore.ExamStatus("exam-1") != "ready" {
		t.Fatalf("ready exam must reject template clone, got %d %s status=%s", cloneRec.Code, cloneRec.Body.String(), paperStore.ExamStatus("exam-1"))
	}
}

func TestAnswerTemplateRejectsUnknownFieldsAndCrossExamPaper(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"exam:manage"})
	paperStore := paper.NewMemoryStore()
	router := testRouter(authStore, paperStore)
	token := login(t, router)
	otherPaper := createPaper(t, router, token, "exam-2")

	base := `{"exam_paper_id":"` + otherPaper.ID + `","name":"模板","page_count":1,"layout":{"pages":[{"page_no":1,"width":1000,"height":1400,"registration_marks":[],"identity_regions":[],"question_regions":[]}]}`
	unknownReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBufferString(base+`,"tenant_id":"tenant-other"}`), token)
	unknownRec := httptest.NewRecorder()
	router.ServeHTTP(unknownRec, unknownReq)
	if unknownRec.Code != http.StatusBadRequest {
		t.Fatalf("unknown tenant field expected 400, got %d %s", unknownRec.Code, unknownRec.Body.String())
	}

	crossReq := authedRequest(http.MethodPost, "/api/v1/exams/exam-1/answer-sheet-templates", bytes.NewBufferString(base+`}`), token)
	crossRec := httptest.NewRecorder()
	router.ServeHTTP(crossRec, crossReq)
	if crossRec.Code != http.StatusBadRequest || !strings.Contains(crossRec.Body.String(), `"invalid_configuration"`) {
		t.Fatalf("cross exam paper expected 400, got %d %s", crossRec.Code, crossRec.Body.String())
	}
}

// 使用真实路由与认证中间件、内存业务存储；这些用例不覆盖数据库锁、触发器和落库原子性。
func testRouter(authStore *auth.MemoryStore, paperStore *paper.MemoryStore) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewRouter(cfg, logger.New(io.Discard, "error"), nil, authStore, org.NewMemoryStore(), exam.NewMemoryStore(), paperStore)
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
			ID:          "u-paper",
			TenantID:    "tenant-paper",
			TenantCode:  "demo",
			Username:    "paper_admin",
			DisplayName: "Paper Admin",
			Status:      "active",
			Roles:       []string{"school_admin"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "paper_admin", "password": "ChangeMe123!"})
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

func createPaper(t *testing.T, router http.Handler, token string, examID string) paper.Paper {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/exams/"+examID+"/papers", bytes.NewBufferString(`{"file":{"original_name":"paper.pdf","content_type":"application/pdf","size_bytes":1024,"hash_sha256":"abc","storage_bucket":"papers","storage_key":"tenant/demo/paper.pdf"}}`), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create paper expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Paper paper.Paper `json:"paper"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Paper
}

func createQuestion(t *testing.T, router http.Handler, token string, examID string, score float64) paper.Question {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/exams/"+examID+"/questions", bytes.NewBufferString(validQuestionJSON(score)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create question expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Question paper.Question `json:"question"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Question
}

func createQuestionWithNumber(t *testing.T, router http.Handler, token, examID, number string, score float64) paper.Question {
	t.Helper()
	body := strings.Replace(validQuestionJSON(score), `"question_no":"Q1"`, `"question_no":"`+number+`"`, 1)
	req := authedRequest(http.MethodPost, "/api/v1/exams/"+examID+"/questions", bytes.NewBufferString(body), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create question %s expected 201, got %d %s", number, rec.Code, rec.Body.String())
	}
	var response struct {
		Question paper.Question `json:"question"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	return response.Question
}

func createRubric(t *testing.T, router http.Handler, token string, questionID string, status string, score float64) paper.Rubric {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/questions/"+questionID+"/rubric", bytes.NewBufferString(validRubricJSON(status, score)), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create rubric expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Rubric paper.Rubric `json:"rubric"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Rubric
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

func validQuestionJSON(score float64) string {
	return `{"question_no":"Q1","question_type":"short_answer","score":` + floatString(score) + `,"stem":"Explain Newton's second law","knowledge_points":["force"],"answer_area":{"page":1,"x":10,"y":20,"w":200,"h":80},"answer_key":{"standard_answer":"F=ma","equivalent_answers":["force equals mass times acceleration"],"tolerance":{}}}`
}

func validQuestionJSONWithPaper(score float64, paperID string) string {
	return `{"exam_paper_id":"` + paperID + `","question_no":"Q1","question_type":"short_answer","score":` + floatString(score) + `,"stem":"Explain Newton's second law","knowledge_points":["force"],"answer_area":{"page":1,"x":10,"y":20,"w":200,"h":80},"answer_key":{"standard_answer":"F=ma","equivalent_answers":["force equals mass times acceleration"],"tolerance":{}}}`
}

func validRubricJSON(status string, score float64) string {
	return `{"status":"` + status + `","max_score":` + floatString(score) + `,"points":[{"id":"p1","description":"main point","score":` + floatString(score) + `,"required":true}],"deductions":[],"examples":[]}`
}

func floatString(value float64) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

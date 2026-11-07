package subjective_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRouter(authStore *auth.MemoryStore, subjectiveStore subjective.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *server.ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Grading.Subjective = subjectiveStore
	})
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
			ID:          userID,
			TenantID:    tenantID,
			TenantCode:  "demo",
			Username:    "subjective_admin",
			DisplayName: "Subjective Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "subjective_admin", "password": "ChangeMe123!"})
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

func authedRequest(method string, path string, body *bytes.Buffer, token string) *http.Request {
	var reader io.Reader
	if body != nil {
		reader = body
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func callSubjectiveHandler(t *testing.T, handler *subjective.Handler, segmentID string, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/subjective-ai-grade", bytes.NewBufferString(body))
	req.SetPathValue("id", segmentID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID, TenantCode: "demo", Username: "subjective_admin", Status: "active", Permissions: []string{"grading:manage"}}))
	rec := httptest.NewRecorder()
	handler.Grade(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("subjective grade expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func callSubjectiveRequest(t *testing.T, handler *subjective.Handler, segmentID string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/answer-segments/"+segmentID+"/subjective-ai-grade", bytes.NewBufferString(body))
	req.SetPathValue("id", segmentID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID, TenantCode: "demo", Username: "subjective_admin", Status: "active", Permissions: []string{"grading:manage"}}))
	rec := httptest.NewRecorder()
	handler.Grade(rec, req)
	return rec
}

func subjectiveContext(kind string, score float64, answerText string, ocrConfidence *float64) subjective.Context {
	return subjective.Context{
		AnswerVersion: "answer-v1",
		Subject:       "chinese",
		GradeLevel:    "junior",
		Question: paper.Question{
			ID:           "question-" + kind,
			TenantID:     tenantID,
			ExamID:       "exam-1",
			QuestionNo:   "Q1",
			QuestionType: kind,
			Score:        score,
			Stem:         "synthetic question",
		},
		Rubric: paper.Rubric{
			ID:         "rubric-" + kind,
			QuestionID: "question-" + kind,
			Version:    "v1",
			Status:     "approved",
			MaxScore:   score,
			Points:     []paper.RubricPoint{{ID: "p1", Description: "synthetic rubric point", Score: score, Required: true}},
		},
		AnswerText:     answerText,
		AnswerImageRef: map[string]any{"answer_segment_id": "segment-1"},
		OCRConfidence:  ocrConfidence,
	}
}

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}

// 入队重放、真实租约执行和实时统计依次验证；领取任务后领域运行须等执行才进入处理中。
func assertSubjectiveBatchEnqueueAndLiveProgress(t *testing.T, handler *subjective.Handler, store *subjective.MemoryStore, runtime *workerruntime.MemoryStore, batchID string) {
	t.Helper()
	enqueueReq := httptest.NewRequest(http.MethodPost, "/api/v1/subjective-grading-batches/"+batchID+"/enqueue", nil)
	enqueueReq.SetPathValue("batchId", batchID)
	enqueueReq = enqueueReq.WithContext(auth.WithUser(enqueueReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"grading:manage"}}))
	enqueueRec := httptest.NewRecorder()
	handler.EnqueueBatch(enqueueRec, enqueueReq)
	if enqueueRec.Code != http.StatusOK || !strings.Contains(enqueueRec.Body.String(), `"queued_count":2`) {
		t.Fatalf("enqueue expected two queued tasks, got %d %s", enqueueRec.Code, enqueueRec.Body.String())
	}
	requeued := httptest.NewRecorder()
	handler.EnqueueBatch(requeued, enqueueReq)
	if requeued.Code != http.StatusOK || !strings.Contains(requeued.Body.String(), `"queued_count":2`) {
		t.Fatalf("repeated enqueue should be idempotent, got %d %s", requeued.Code, requeued.Body.String())
	}
	claimed, err := runtime.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "subjective-grading", WorkerService: "subjective-worker", WorkerInstanceID: "worker-b", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim one batch task: %v %#v", err, claimed)
	}
	run, err := store.GetRun(context.Background(), tenantID, claimed[0].SourceID)
	if err != nil || run.Status != subjective.RunQueued {
		t.Fatalf("batch run must remain queued until worker execution: %#v err=%v", run, err)
	}
	executeBody, _ := json.Marshal(map[string]string{"task_id": claimed[0].ID, "lease_token": claimed[0].LeaseToken})
	executeReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/subjective-grading/runs/"+run.ID+"/execute", bytes.NewReader(executeBody))
	executeReq.SetPathValue("runId", run.ID)
	executeReq = executeReq.WithContext(auth.WithUser(executeReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"orchestrator:manage"}}))
	executeRec := httptest.NewRecorder()
	handler.ExecuteWorker(executeRec, executeReq)
	if executeRec.Code != http.StatusOK {
		t.Fatalf("execute batch task expected 200, got %d %s", executeRec.Code, executeRec.Body.String())
	}
	getBatchReq := httptest.NewRequest(http.MethodGet, "/api/v1/subjective-grading-batches/"+batchID, nil)
	getBatchReq.SetPathValue("batchId", batchID)
	getBatchReq = getBatchReq.WithContext(auth.WithUser(getBatchReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"grading:manage"}}))
	getBatchRec := httptest.NewRecorder()
	handler.GetBatch(getBatchRec, getBatchReq)
	if getBatchRec.Code != http.StatusOK || !strings.Contains(getBatchRec.Body.String(), `"queued_count":1`) || !strings.Contains(getBatchRec.Body.String(), `"processing_count":1`) {
		t.Fatalf("batch refresh should expose live worker progress, got %d %s", getBatchRec.Code, getBatchRec.Body.String())
	}
}

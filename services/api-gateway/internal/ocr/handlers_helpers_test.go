package ocr_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRouter(authStore *auth.MemoryStore, submissionStore submission.Store, ocrStore ocrpkg.Store, queue *ocrpkg.MemoryQueue, runtimeStores ...workerruntime.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *server.ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Exam.Submissions = submissionStore
		stores.Capture.OCR = ocrStore
		stores.Capture.OCRQueue = queue
		if len(runtimeStores) > 0 {
			stores.Capture.WorkerRuntime = runtimeStores[0]
		}
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
			Username:    "ocr_admin",
			DisplayName: "OCR Admin",
			Status:      "active",
			Roles:       []string{"teacher"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	// 人类账号仍用于创建与管理；内部处理必须使用独立服务会话和实际领取的租约。
	store.AddUser(auth.UserWithPassword{User: auth.User{
		ID: "00000000-0000-0000-0000-000000000402", TenantID: tenantID, TenantCode: "demo",
		Username: "ocr_worker", DisplayName: "Synthetic OCR Worker", Status: "active",
		Roles: []string{"page_processing_worker"}, Permissions: []string{"ocr:manage"},
		DataScope: map[string]any{"scope": "service", "synthetic": true},
	}, PasswordHash: hash})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	return loginOCRAccount(t, router, "ocr_admin", "")
}

func loginOCRAccount(t *testing.T, router http.Handler, username, clientType string) string {
	t.Helper()
	input := map[string]string{"tenant_code": "demo", "username": username, "password": "ChangeMe123!"}
	if clientType != "" {
		input["client_type"] = clientType
	}
	raw, _ := json.Marshal(input)
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

func readySubmission(t *testing.T, store submission.Store) submission.SubmissionPage {
	t.Helper()
	ctx := context.Background()
	item, err := store.Create(ctx, tenantID, "exam-1", userID, submission.CreateSubmissionInput{SourceType: "scanner_upload", ExpectedPageCount: 1})
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	page, err := store.AddPage(ctx, tenantID, item.ID, userID, submission.AddPageInput{FileAssetID: "file-1", PageNo: 1})
	if err != nil {
		t.Fatalf("add page: %v", err)
	}
	if _, err := store.RunQualityCheck(ctx, tenantID, item.ID, userID); err != nil {
		t.Fatalf("collection integrity check: %v", err)
	}
	if _, err := store.ApplyPageQualityResult(ctx, tenantID, submission.ApplyPageQualityInput{
		SubmissionID:          item.ID,
		PageID:                page.ID,
		LatestQualityRunID:    "quality-run-1",
		NormalizedFileAssetID: "normalized-file-1",
		QualityStatus:         "passed",
	}); err != nil {
		t.Fatalf("page image quality result: %v", err)
	}
	if _, err := store.UpdateStatus(ctx, tenantID, item.ID, userID, "ready_for_ocr", item.Revision); err != nil {
		t.Fatalf("ready status: %v", err)
	}
	return page
}

func createTask(t *testing.T, router http.Handler, token string, submissionID string) ocrpkg.Task {
	t.Helper()
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+submissionID+"/ocr-tasks", bytes.NewBufferString(validTaskJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Task ocrpkg.Task `json:"task"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return response.Task
}

func validTaskJSON() string {
	return `{"engine":"paddleocr","engine_version":"pp-ocrv5","min_confidence":0.8}`
}

type ocrWorkerSession struct {
	token string
	task  workerruntime.Task
}

func ocrWorkerForTask(t *testing.T, router http.Handler, sourceTaskID string) ocrWorkerSession {
	t.Helper()
	worker := ocrWorkerSession{token: loginOCRAccount(t, router, "ocr_worker", "service")}
	body, _ := json.Marshal(workerruntime.ClaimInput{
		QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "ocr-test-1",
		Limit: 1, LeaseSeconds: 300,
	})
	req := authedRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", bytes.NewBuffer(body), worker.token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var response struct {
		Tasks []workerruntime.Task `json:"tasks"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Tasks) != 1 {
		t.Fatalf("claim OCR runtime task: %d %s", rec.Code, rec.Body.String())
	}
	worker.task = response.Tasks[0]
	if worker.task.SourceType != "ocr_task" || worker.task.SourceID != sourceTaskID {
		t.Fatalf("claimed runtime task does not belong to source %s: %#v", sourceTaskID, worker.task)
	}
	return worker
}

func workerRequest(method, path string, body *bytes.Buffer, worker ocrWorkerSession) *http.Request {
	req := authedRequest(method, path, body, worker.token)
	req.Header.Set(workerruntime.TaskIDHeader, worker.task.ID)
	req.Header.Set(workerruntime.TaskLeaseHeader, worker.task.LeaseToken)
	req.Header.Set(workerruntime.WorkerServiceHeader, worker.task.WorkerService)
	req.Header.Set(workerruntime.WorkerInstanceIDHeader, worker.task.WorkerInstanceID)
	return req
}

func withRuntime(body string, task workerruntime.Task) string {
	return strings.TrimSuffix(body, "}") + fmt.Sprintf(
		`,"runtime_task_id":%q,"runtime_lease_token":%q}`,
		task.ID, task.LeaseToken,
	)
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

func assertAuditAction(t *testing.T, store *auth.MemoryStore, action string) {
	t.Helper()
	for _, audit := range store.Audits() {
		if audit.Action == action {
			return
		}
	}
	t.Fatalf("missing audit action %s in %#v", action, store.Audits())
}

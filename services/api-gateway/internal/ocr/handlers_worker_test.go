package ocr_test

import (
	"bytes"
	"context"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCompleteOCRTaskIsIdempotentForSamePayload(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	runtimeStore := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue(), runtimeStore)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	runtimeTask := worker.task

	req := workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/start", nil, worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body := withRuntime(`{"model_version":"ppocr-v5-server","config_hash":"cfg","input_hash":"input","duration_ms":42,"worker_id":"worker-a","preprocess_profile":"default","results":[{"submission_page_id":"`+page.ID+`","text":"OCR text","bbox":[10,20,100,40],"confidence":0.91}]}`, runtimeTask)
	for attempt := 0; attempt < 2; attempt++ {
		req = workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result_count":1`) {
			t.Fatalf("completion attempt %d expected idempotent 200, got %d %s", attempt+1, rec.Code, rec.Body.String())
		}
	}

	changed := strings.Replace(body, `"OCR text"`, `"changed text"`, 1)
	req = workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(changed), worker)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "ocr_result_conflict") {
		t.Fatalf("changed duplicate expected 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestOCRWorkerRoutesRejectHumanEvenWithAValidWorkerLease(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissions := submission.NewMemoryStore()
	page := readySubmission(t, submissions)
	source := ocrpkg.NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissions, source, ocrpkg.NewMemoryQueue(), runtime)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/ocr-tasks/pending"},
		{http.MethodGet, "/api/v1/ocr-tasks/" + task.ID + "/input"},
		{http.MethodPost, "/api/v1/ocr-tasks/" + task.ID + "/start"},
		{http.MethodPost, "/api/v1/ocr-tasks/" + task.ID + "/results"},
		{http.MethodPost, "/api/v1/ocr-tasks/" + task.ID + "/fail"},
	} {
		req := workerRequest(route.method, route.path, nil, worker)
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "worker_identity_required") {
			t.Fatalf("human worker access %s: %d %s", route.path, response.Code, response.Body.String())
		}
	}
	current, err := source.GetTask(context.Background(), tenantID, task.ID)
	if err != nil || current.Status != "queued" || len(current.Results) != 0 {
		t.Fatalf("human request changed OCR source: %#v err=%v", current, err)
	}
}

type expiredOCRLeaseStore struct{ *workerruntime.MemoryStore }

func (s expiredOCRLeaseStore) AuthorizeLease(ctx context.Context, id, lease, service, instance string, now time.Time) (workerruntime.Task, error) {
	return s.MemoryStore.AuthorizeLease(ctx, id, lease, service, instance, now.Add(time.Hour))
}

func TestOCRCompletionRejectsExpiredLeaseWithoutWritingResults(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissions := submission.NewMemoryStore()
	page := readySubmission(t, submissions)
	source := ocrpkg.NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	queue := ocrpkg.NewMemoryQueue()
	router := testRouter(authStore, submissions, source, queue, runtime)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	started := httptest.NewRecorder()
	router.ServeHTTP(started, workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/start", nil, worker))
	if started.Code != http.StatusOK {
		t.Fatalf("start before expiry: %d %s", started.Code, started.Body.String())
	}
	// 仅把守卫传给真实租约校验器的时间前移，不等待三十秒或改写任务状态；回调必须停在业务写入之前。
	expiredRouter := testRouter(authStore, submissions, source, queue, expiredOCRLeaseStore{runtime})
	body := withRuntime(`{"results":[{"submission_page_id":"`+page.ID+`","text":"late","bbox":[1,2,3,4],"confidence":0.9}]}`, worker.task)
	response := httptest.NewRecorder()
	expiredRouter.ServeHTTP(response, workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker))
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "worker_scope_forbidden") {
		t.Fatalf("expired completion: %d %s", response.Code, response.Body.String())
	}
	current, err := source.GetTask(context.Background(), tenantID, task.ID)
	if err != nil || current.Status != "processing" || len(current.Results) != 0 {
		t.Fatalf("expired callback changed OCR source: %#v err=%v", current, err)
	}
	saved, err := runtime.Get(context.Background(), tenantID, worker.task.ID)
	if err != nil || saved.Status != workerruntime.StatusLeased || saved.LeaseToken != worker.task.LeaseToken {
		t.Fatalf("expired callback consumed runtime lease: %#v err=%v", saved, err)
	}
}

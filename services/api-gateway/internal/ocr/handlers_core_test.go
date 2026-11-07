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
)

type regionSubmissionStore struct {
	*submission.MemoryStore
	regions []submission.AnswerRegion
}

func (s *regionSubmissionStore) ListAnswerRegions(_ context.Context, tenant, submissionID string) ([]submission.AnswerRegion, error) {
	if tenant != tenantID {
		return nil, submission.ErrNotFound
	}
	return append([]submission.AnswerRegion{}, s.regions...), nil
}

func TestCreateStartCompleteOCRTaskWithLowConfidence(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	ocrStore := ocrpkg.NewMemoryStore()
	queue := ocrpkg.NewMemoryQueue()
	runtimeStore := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrStore, queue, runtimeStore)
	token := login(t, router)

	task := createTask(t, router, token, page.SubmissionID)
	if len(queue.Tasks()) != 0 {
		t.Fatalf("runtime-backed OCR must not use the legacy in-memory queue, got %d entries", len(queue.Tasks()))
	}
	runtimeTask, err := runtimeStore.GetBySource(context.Background(), tenantID, "ocr_task", task.ID)
	if err != nil || runtimeTask.Status != workerruntime.StatusQueued {
		t.Fatalf("ocr creation should enqueue runtime task: %v %#v", err, runtimeTask)
	}
	worker := ocrWorkerForTask(t, router, task.ID)
	runtimeTask = worker.task

	req := workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/start", nil, worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "processing") {
		t.Fatalf("start expected processing, got %d %s", rec.Code, rec.Body.String())
	}

	body := withRuntime(`{"results":[{"submission_page_id":"`+page.ID+`","text":"Synthetic OCR text","bbox":[10,20,100,40],"confidence":0.52}]}`, runtimeTask)
	req = workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"requires_human_review":true`) || !strings.Contains(rec.Body.String(), `"ocr_engine":"paddleocr"`) {
		t.Fatalf("complete expected low-confidence review flag, got %d %s", rec.Code, rec.Body.String())
	}

	assertAuditAction(t, authStore, "ocr.task_created")
	assertAuditAction(t, authStore, "ocr.task_started")
	assertAuditAction(t, authStore, "ocr.task_completed")
}

func TestOCRTaskRequiresReadySubmission(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	item, err := submissionStore.Create(context.Background(), tenantID, "exam-1", userID, submission.CreateSubmissionInput{SourceType: "scanner_upload", ExpectedPageCount: 1})
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue())
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/ocr-tasks", bytes.NewBufferString(validTaskJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "submission_not_ready_for_ocr") {
		t.Fatalf("expected submission not ready conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCompleteBeforeStartRejected(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	runtimeStore := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue(), runtimeStore)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	runtimeTask := worker.task

	body := withRuntime(`{"results":[{"submission_page_id":"`+page.ID+`","text":"Synthetic OCR text","bbox":[10,20,100,40],"confidence":0.9}]}`, runtimeTask)
	req := workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "invalid_ocr_status_transition") {
		t.Fatalf("expected transition conflict, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestOCRTaskFail(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	runtimeStore := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue(), runtimeStore)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	runtimeTask := worker.task

	req := workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/fail", bytes.NewBufferString(withRuntime(`{"error_message":"worker timeout"}`, runtimeTask)), worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"failed"`) {
		t.Fatalf("fail expected failed status, got %d %s", rec.Code, rec.Body.String())
	}
	assertAuditAction(t, authStore, "ocr.task_failed")
}

func TestOCRPermissionDenied(t *testing.T) {
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	router := testRouter(authStoreWithPermissions(t, []string{"system:read"}), submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue())
	token := login(t, router)
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+page.SubmissionID+"/ocr-tasks", bytes.NewBufferString(validTaskJSON()), token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

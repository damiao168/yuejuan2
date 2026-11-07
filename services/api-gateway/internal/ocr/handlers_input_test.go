package ocr_test

import (
	"bytes"
	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPendingOCRTasksReturnsOnlyQueuedTasks(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	pageA := readySubmission(t, submissionStore)
	pageB := readySubmission(t, submissionStore)
	ocrStore := ocrpkg.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrStore, ocrpkg.NewMemoryQueue())
	token := login(t, router)
	processingTask := createTask(t, router, token, pageA.SubmissionID)
	queuedTask := createTask(t, router, token, pageB.SubmissionID)
	worker := ocrWorkerForTask(t, router, processingTask.ID)

	req := workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+processingTask.ID+"/start", nil, worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	req = workerRequest(http.MethodGet, "/api/v1/ocr-tasks/pending?limit=10", nil, worker)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Tasks []ocrpkg.Task `json:"tasks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(response.Tasks) != 1 || response.Tasks[0].ID != queuedTask.ID {
		t.Fatalf("pending should return only queued task %s, got %#v", queuedTask.ID, response.Tasks)
	}
}

func TestOCRTaskInputOmitsStudentIdentityAndIncludesPages(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := submission.NewMemoryStore()
	page := readySubmission(t, submissionStore)
	runtimeStore := workerruntime.NewMemoryStore()
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue(), runtimeStore)
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)

	req := workerRequest(http.MethodGet, "/api/v1/ocr-tasks/"+task.ID+"/input", nil, worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("input expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "student_id") || strings.Contains(body, "candidate_no") {
		t.Fatalf("input must not expose student identity fields: %s", body)
	}
	if !strings.Contains(body, page.ID) || !strings.Contains(body, page.FileAssetID) {
		t.Fatalf("input should include submission page and file asset ids, got %s", body)
	}
}

func TestOCRTaskInputIncludesPrivacySafeAnswerRegions(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := &regionSubmissionStore{MemoryStore: submission.NewMemoryStore()}
	page := readySubmission(t, submissionStore.MemoryStore)
	submissionStore.regions = []submission.AnswerRegion{{
		ID: "segment-1", QuestionID: "question-1", QuestionNo: "Q1",
		QuestionType: "text", BBox: []float64{10, 20, 100, 40}, PageID: page.ID,
	}}
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue())
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)

	req := workerRequest(http.MethodGet, "/api/v1/ocr-tasks/"+task.ID+"/input", nil, worker)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	body := rec.Body.String()
	for _, want := range []string{`"answer_segment_id":"segment-1"`, `"question_no":"Q1"`, `"bbox":[10,20,100,40]`} {
		if !strings.Contains(body, want) {
			t.Fatalf("region input missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "candidate_no") || strings.Contains(body, "student_id") {
		t.Fatalf("region input exposed identity: %s", body)
	}
}

func TestOCRTaskInputFallsBackForMixedCoordinateSpaces(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"ocr:manage"})
	submissionStore := &regionSubmissionStore{MemoryStore: submission.NewMemoryStore()}
	page := readySubmission(t, submissionStore.MemoryStore)
	submissionStore.regions = []submission.AnswerRegion{
		{ID: "legacy", PageID: page.ID, BBox: []float64{10, 20, 30, 40}},
		{ID: "registered", PageID: page.ID, BBox: []float64{30, 40, 50, 60}, RequiresFullPage: true},
		{ID: "foreign-page", PageID: "another-page", BBox: []float64{1, 2, 3, 4}},
	}
	router := testRouter(authStore, submissionStore, ocrpkg.NewMemoryStore(), ocrpkg.NewMemoryQueue())
	token := login(t, router)
	task := createTask(t, router, token, page.SubmissionID)
	worker := ocrWorkerForTask(t, router, task.ID)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, workerRequest(http.MethodGet, "/api/v1/ocr-tasks/"+task.ID+"/input", nil, worker))
	if rec.Code != http.StatusOK {
		t.Fatalf("input expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"regions"`) || strings.Contains(rec.Body.String(), "foreign-page") {
		t.Fatalf("unsafe partial ROI input: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"download_url":"/api/v1/files/file-1/download"`) {
		t.Fatalf("full-page fallback changed image coordinates: %s", rec.Body.String())
	}
}

func TestCompleteOCRTaskRejectsInvalidBBoxSchema(t *testing.T) {
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

	body := withRuntime(`{"results":[{"submission_page_id":"`+page.ID+`","text":"bad bbox","bbox":[-1,20,100,40],"confidence":0.9}]}`, runtimeTask)
	req = workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid bbox expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestCompleteOCRTaskPersistsWorkerMetadata(t *testing.T) {
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

	body := withRuntime(`{"model_version":"ppocr-v5-server","config_hash":"cfg123","input_hash":"input123","duration_ms":42,"worker_id":"worker-a","preprocess_profile":"default","results":[{"submission_page_id":"`+page.ID+`","text":"Real OCR text","bbox":[10,20,100,40],"confidence":0.91}]}`, runtimeTask)
	req = workerRequest(http.MethodPost, "/api/v1/ocr-tasks/"+task.ID+"/results", bytes.NewBufferString(body), worker)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body = rec.Body.String()
	for _, want := range []string{`"model_version":"ppocr-v5-server"`, `"config_hash":"cfg123"`, `"input_hash":"input123"`, `"duration_ms":42`, `"worker_id":"worker-a"`, `"preprocess_profile":"default"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %s: %s", want, body)
		}
	}
}

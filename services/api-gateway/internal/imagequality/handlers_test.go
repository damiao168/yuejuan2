package imagequality_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/logger"
	"edugrade-enterprise/services/api-gateway/internal/server"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type transactionalStoreDecorator struct {
	imagequality.Store
}

func (s *transactionalStoreDecorator) CreateRunsWithTasks(ctx context.Context, tenantID, _ string, input imagequality.CreateRunsInput) ([]imagequality.Run, error) {
	return s.CreateRuns(ctx, tenantID, input)
}

func (s *transactionalStoreDecorator) SubmitResultCommand(ctx context.Context, tenantID, _ string, runID string, input imagequality.ResultInput) (imagequality.Run, error) {
	return s.CompleteRun(ctx, tenantID, runID, input)
}

func TestTransactionalHandlerRejectsMissingAndTypedNilCoordinator(t *testing.T) {
	dependencies := imagequality.TransactionalHandlerDependencies{
		Submissions: submission.NewMemoryStore(), Files: files.NewMemoryStore(), Audit: auth.NewMemoryStore(),
		Runtime: workerruntime.NewMemoryStore(), Captures: capture.NewMemoryStore(),
	}
	if _, err := imagequality.NewTransactionalHandler(dependencies); err == nil || !strings.Contains(err.Error(), "coordinator") {
		t.Fatalf("missing coordinator accepted: %v", err)
	}
	var typedNil *transactionalStoreDecorator
	dependencies.Coordinator = typedNil
	if _, err := imagequality.NewTransactionalHandler(dependencies); err == nil || !strings.Contains(err.Error(), "coordinator") {
		t.Fatalf("typed nil coordinator accepted: %v", err)
	}
}

func TestTransactionalHandlerAcceptsCapabilityDecorator(t *testing.T) {
	dependencies := imagequality.TransactionalHandlerDependencies{
		Coordinator: &transactionalStoreDecorator{Store: imagequality.NewMemoryStore()},
		Submissions: submission.NewMemoryStore(), Files: files.NewMemoryStore(), Audit: auth.NewMemoryStore(),
		Runtime: workerruntime.NewMemoryStore(), Captures: capture.NewMemoryStore(),
	}
	if _, err := imagequality.NewTransactionalHandler(dependencies); err != nil {
		t.Fatalf("capability decorator rejected: %v", err)
	}
}

const handlerTenantID = "00000000-0000-0000-0000-000000000002"
const handlerUserID = "00000000-0000-0000-0000-000000000401"

func TestRunQualityCheckCreatesRunsAndClaimOmitsStudentIdentity(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage", "ocr:manage"})
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	qualityStore := imagequality.NewMemoryStore()
	runtimeStore := workerruntime.NewMemoryStore()
	item, page := submissionWithOriginalFile(t, fileStore, submissionStore, "source-hash-1")
	router := testRouter(authStore, fileStore, submissionStore, qualityStore, runtimeStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/run-quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"processing_status":"pending"`) {
		t.Fatalf("run-quality-check expected 202 pending run, got %d %s", rec.Code, rec.Body.String())
	}

	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/jobs/claim", bytes.NewBufferString(`{"worker_instance_id":"worker-a","limit":1,"lease_seconds":300}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "student_id") || strings.Contains(body, "candidate_no") {
		t.Fatalf("claim must not expose student identity fields: %s", body)
	}
	for _, want := range []string{`"run_id"`, `"lease_token"`, page.ID, page.FileAssetID, `"source_sha256":"source-hash-1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("claim response missing %s: %s", want, body)
		}
	}
	if !strings.Contains(body, `"runtime_task_id"`) {
		t.Fatalf("claim response must expose runtime task id: %s", body)
	}
}

func TestListPageQualityRunsReturnsExplainableHistoryWithoutLeaseSecrets(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage", "ocr:manage"})
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	qualityStore := imagequality.NewMemoryStore()
	item, page := submissionWithOriginalFile(t, fileStore, submissionStore, "source-hash-history")
	router := testRouter(authStore, fileStore, submissionStore, qualityStore)
	token := login(t, router)

	for range 2 {
		req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/run-quality-check", nil, token)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("create quality run expected 202, got %d %s", rec.Code, rec.Body.String())
		}
	}

	req := authedRequest(http.MethodGet, "/api/v1/submission-pages/"+page.ID+"/quality-runs", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list quality runs expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Runs []imagequality.Run `json:"runs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode quality history: %v", err)
	}
	if len(response.Runs) != 2 || response.Runs[0].SubmissionPageID != page.ID {
		t.Fatalf("unexpected quality history: %#v", response.Runs)
	}
	if strings.Contains(rec.Body.String(), "lease_token") || strings.Contains(rec.Body.String(), "result_payload_hash") {
		t.Fatalf("quality history leaked internal secrets: %s", rec.Body.String())
	}
}

func TestRuntimeHeartbeatRenewsImageQualitySourceLease(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage", "ocr:manage", "worker:execute"})
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	qualityStore := imagequality.NewMemoryStore()
	runtimeStore := workerruntime.NewMemoryStore()
	item, _ := submissionWithOriginalFile(t, fileStore, submissionStore, "source-hash-heartbeat")
	router := testRouter(authStore, fileStore, submissionStore, qualityStore, runtimeStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/run-quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run-quality-check expected 202, got %d %s", rec.Code, rec.Body.String())
	}
	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/jobs/claim", bytes.NewBufferString(`{"worker_instance_id":"worker-a","limit":1,"lease_seconds":300}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var claimed struct {
		Jobs []imagequality.ClaimedJob `json:"jobs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&claimed); err != nil || len(claimed.Jobs) != 1 {
		t.Fatalf("decode claimed job: %v %#v", err, claimed)
	}
	job := claimed.Jobs[0]

	qualityStore.ForceExpireLeaseForTest(job.RunID)
	heartbeatBody := `{"lease_token":"` + job.LeaseToken + `","worker_service":"image-quality-worker","worker_instance_id":"worker-a","state":"running","lease_seconds":600}`
	req = authedRequest(http.MethodPost, "/api/v1/internal/worker/tasks/"+job.RuntimeTaskID+"/heartbeat", bytes.NewBufferString(heartbeatBody), token)
	qualityWorkerHeaders(req, job)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	run, err := qualityStore.GetRun(context.Background(), handlerTenantID, job.RunID)
	if err != nil {
		t.Fatalf("get renewed quality run: %v", err)
	}
	if run.LeaseExpiresAt == nil || !run.LeaseExpiresAt.After(time.Now().UTC().Add(9*time.Minute)) {
		t.Fatalf("heartbeat did not renew source lease: %#v", run.LeaseExpiresAt)
	}
}

func TestQualityResultRequiresLeaseAndActivatesPage(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage", "ocr:manage"})
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	qualityStore := imagequality.NewMemoryStore()
	runtimeStore := workerruntime.NewMemoryStore()
	item, page := submissionWithOriginalFile(t, fileStore, submissionStore, "source-hash-1")

	router := testRouter(authStore, fileStore, submissionStore, qualityStore, runtimeStore)
	token := login(t, router)

	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/run-quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run-quality-check expected 202, got %d %s", rec.Code, rec.Body.String())
	}
	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/jobs/claim", bytes.NewBufferString(`{"worker_instance_id":"worker-a","limit":1,"lease_seconds":300}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	var claimResponse struct {
		Jobs []imagequality.ClaimedJob `json:"jobs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&claimResponse); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if len(claimResponse.Jobs) != 1 {
		t.Fatalf("expected one job, got %#v", claimResponse.Jobs)
	}
	job := claimResponse.Jobs[0]
	normalized := createFile(t, fileStore, files.CreateAssetInput{
		TenantID:     handlerTenantID,
		ExamID:       item.ExamID,
		SubmissionID: item.ID,
		OwnerType:    "submission_page_normalized",
		OwnerID:      page.ID,
		OriginalName: "page-001-normalized.png",
		ContentType:  "image/png",
		SizeBytes:    128,
		HashSHA256:   "normalized-hash-1",
		UploadedBy:   handlerUserID,
	})

	resultBody := `{
	  "lease_token":"` + job.LeaseToken + `",
	  "attempt_no":` + intString(job.AttemptNo) + `,
	  "result_version":"result-v1",
	  "duration_ms":740,
	  "processing_status":"completed",
	  "quality_status":"passed",
	  "normalized_file_asset_id":"` + normalized.ID + `",
	  "quality_report":{"metrics":{"sharpness_score":0.91}},
	  "quality_issues":[],
	  "normalization_transform":{"source_to_normalized_matrix":[[1,0,0],[0,1,0],[0,0,1]]}
	}`
	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/runs/"+job.RunID+"/result", bytes.NewBufferString(resultBody), token)
	qualityWorkerHeaders(req, job)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"quality_status":"passed"`) {
		t.Fatalf("result expected 200 passed, got %d %s", rec.Code, rec.Body.String())
	}

	pages, err := submissionStore.ListPages(context.Background(), handlerTenantID, item.ID)
	if err != nil {
		t.Fatalf("list pages: %v", err)
	}
	if len(pages) != 1 {
		t.Fatalf("expected one page, got %#v", pages)
	}
	if pages[0].LatestQualityRunID != job.RunID || pages[0].NormalizedFileAssetID != normalized.ID || pages[0].QualityStatus != "passed" {
		t.Fatalf("quality result should activate page, got %#v", pages[0])
	}
	runtimeTask, err := runtimeStore.GetBySource(context.Background(), handlerTenantID, "image_quality_run", job.RunID)
	if err != nil || runtimeTask.Status != workerruntime.StatusSucceeded {
		t.Fatalf("quality result should complete runtime task: %v %#v", err, runtimeTask)
	}
	// 完成响应丢失后只可重放同一结果；成功租约不能用于改写已有质量结论。
	for _, retry := range []struct {
		body string
		want int
	}{
		{resultBody, http.StatusOK},
		{strings.Replace(resultBody, `"sharpness_score":0.91`, `"sharpness_score":0.21`, 1), http.StatusConflict},
	} {
		req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/runs/"+job.RunID+"/result", bytes.NewBufferString(retry.body), token)
		qualityWorkerHeaders(req, job)
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != retry.want {
			t.Fatalf("quality result retry expected %d, got %d %s", retry.want, rec.Code, rec.Body.String())
		}
	}
}

func TestQualityResultRejectsMissingLease(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"submission:manage", "ocr:manage"})
	fileStore := files.NewMemoryStore()
	submissionStore := submission.NewMemoryStore()
	qualityStore := imagequality.NewMemoryStore()
	item, _ := submissionWithOriginalFile(t, fileStore, submissionStore, "source-hash-1")
	router := testRouter(authStore, fileStore, submissionStore, qualityStore)
	token := login(t, router)
	req := authedRequest(http.MethodPost, "/api/v1/submissions/"+item.ID+"/run-quality-check", nil, token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("run-quality-check expected 202, got %d %s", rec.Code, rec.Body.String())
	}
	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/jobs/claim", bytes.NewBufferString(`{"worker_instance_id":"worker-a","limit":1,"lease_seconds":300}`), token)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var claimResponse struct {
		Jobs []imagequality.ClaimedJob `json:"jobs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&claimResponse); err != nil {
		t.Fatalf("decode claim: %v", err)
	}

	req = authedRequest(http.MethodPost, "/api/v1/internal/image-quality/runs/"+claimResponse.Jobs[0].RunID+"/result", bytes.NewBufferString(`{"attempt_no":1,"result_version":"result-v1","processing_status":"completed","quality_status":"passed","normalized_file_asset_id":"file-normalized"}`), token)
	qualityWorkerHeaders(req, claimResponse.Jobs[0])
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_image_quality_input") {
		t.Fatalf("missing lease expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func testRouter(authStore *auth.MemoryStore, fileStore files.Store, submissionStore submission.Store, qualityStore imagequality.Store, runtimeStores ...workerruntime.Store) http.Handler {
	cfg := config.Config{
		Service: config.ServiceConfig{Name: "test", Environment: "test", ReadinessTimeout: time.Millisecond},
		Auth:    config.AuthConfig{SessionTTL: time.Hour},
	}
	return server.NewMemoryRouter(cfg, logger.New(io.Discard, "error"), nil, files.NewMemoryObjectStorage(), func(stores *server.ApplicationStores) {
		stores.Identity.Auth = authStore
		stores.Exam.Files = fileStore
		stores.Exam.Submissions = submissionStore
		stores.Capture.ImageQuality = qualityStore
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
			ID:          handlerUserID,
			TenantID:    handlerTenantID,
			TenantCode:  "demo",
			Username:    "quality_admin",
			DisplayName: "Quality Admin",
			Status:      "active",
			Roles:       []string{"page_processing_worker"},
			Permissions: permissions,
			DataScope:   map[string]any{"scope": "school", "synthetic": true},
		},
		PasswordHash: hash,
	})
	return store
}

func login(t *testing.T, router http.Handler) string {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"tenant_code": "demo", "username": "quality_admin", "password": "ChangeMe123!", "client_type": "service"})
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

func submissionWithOriginalFile(t *testing.T, fileStore files.Store, submissionStore submission.Store, hash string) (submission.Submission, submission.SubmissionPage) {
	t.Helper()
	file := createFile(t, fileStore, files.CreateAssetInput{
		TenantID:     handlerTenantID,
		OwnerType:    "submission_page_original",
		OwnerID:      "page-original",
		OriginalName: "page-001.jpg",
		ContentType:  "image/jpeg",
		SizeBytes:    256,
		HashSHA256:   hash,
		UploadedBy:   handlerUserID,
	})
	item, err := submissionStore.Create(context.Background(), handlerTenantID, "exam-1", handlerUserID, submission.CreateSubmissionInput{
		StudentID:         "00000000-0000-0000-0000-000000000501",
		CandidateNo:       "A001",
		SourceType:        "scanner_upload",
		ExpectedPageCount: 1,
	})
	if err != nil {
		t.Fatalf("create submission: %v", err)
	}
	page, err := submissionStore.AddPage(context.Background(), handlerTenantID, item.ID, handlerUserID, submission.AddPageInput{FileAssetID: file.ID, PageNo: 1})
	if err != nil {
		t.Fatalf("add page: %v", err)
	}
	return item, page
}

func createFile(t *testing.T, store files.Store, input files.CreateAssetInput) files.FileAsset {
	t.Helper()
	input.StorageBucket = "test-bucket"
	input.StorageKey = input.HashSHA256 + "-" + input.OriginalName
	input.Visibility = "private"
	asset, err := store.Create(context.Background(), input)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	return asset
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

func intString(value int) string {
	return strconv.Itoa(value)
}

func qualityWorkerHeaders(req *http.Request, job imagequality.ClaimedJob) {
	req.Header.Set(workerruntime.TaskIDHeader, job.RuntimeTaskID)
	req.Header.Set(workerruntime.TaskLeaseHeader, job.LeaseToken)
	req.Header.Set(workerruntime.WorkerServiceHeader, "image-quality-worker")
	req.Header.Set(workerruntime.WorkerInstanceIDHeader, "worker-a")
}

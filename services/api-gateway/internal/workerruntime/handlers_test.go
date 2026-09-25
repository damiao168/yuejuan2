package workerruntime_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestHandlerClaimHeartbeatCompleteAndAudit(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	audit := auth.NewMemoryStore()
	handler := workerruntime.NewHandler(store, audit)
	input := imageQualityTaskInput()
	input.TaskType = "report_generate"
	input.QueueName = "report"
	input.SourceType = "report_job"
	input.IdempotencyKey = "report-job:1"
	task, err := store.CreateTask(t.Context(), runtimeTenantID, "actor-1", input)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	claim := requestWithUser(http.MethodPost, "/api/v1/internal/worker/tasks/claim", `{"queue_name":"report","worker_service":"report-worker","worker_instance_id":"worker-a","limit":1,"lease_seconds":300}`)
	claimRec := httptest.NewRecorder()
	handler.Claim(claimRec, claim)
	if claimRec.Code != http.StatusOK {
		t.Fatalf("claim expected 200, got %d %s", claimRec.Code, claimRec.Body.String())
	}
	var claimed struct {
		Tasks []workerruntime.Task `json:"tasks"`
	}
	if err := json.NewDecoder(claimRec.Body).Decode(&claimed); err != nil || len(claimed.Tasks) != 1 {
		t.Fatalf("decode claimed task: %v %#v", err, claimed)
	}

	heartbeat := requestWithUser(http.MethodPost, "/api/v1/internal/worker/tasks/"+task.ID+"/heartbeat", `{"lease_token":"`+claimed.Tasks[0].LeaseToken+`","worker_service":"report-worker","worker_instance_id":"worker-a","state":"running"}`)
	heartbeat.SetPathValue("taskId", task.ID)
	heartbeatRec := httptest.NewRecorder()
	handler.Heartbeat(heartbeatRec, heartbeat)
	if heartbeatRec.Code != http.StatusOK {
		t.Fatalf("heartbeat expected 200, got %d %s", heartbeatRec.Code, heartbeatRec.Body.String())
	}

	complete := requestWithUser(http.MethodPost, "/api/v1/internal/worker/tasks/"+task.ID+"/complete", `{"lease_token":"`+claimed.Tasks[0].LeaseToken+`","result_schema_version":"v1","result":{"quality_status":"passed"},"duration_ms":100}`)
	complete.SetPathValue("taskId", task.ID)
	completeRec := httptest.NewRecorder()
	handler.Complete(completeRec, complete)
	if completeRec.Code != http.StatusOK {
		t.Fatalf("complete expected 200, got %d %s", completeRec.Code, completeRec.Body.String())
	}

	audits := audit.Audits()
	if len(audits) != 2 || audits[0].Action != "worker.task_claimed" || audits[1].Action != "worker.task_completed" {
		t.Fatalf("unexpected runtime audits: %#v", audits)
	}
}

func TestPlatformWorkerClaimsAndHeartbeatsSchoolTask(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	audit := auth.NewMemoryStore()
	handler := workerruntime.NewHandler(store, audit)
	input := imageQualityTaskInput()
	input.TaskType = "ocr"
	input.QueueName = "ocr"
	input.SourceType = "ocr_task"
	input.IdempotencyKey = "ocr:school-task"
	task, err := store.CreateTask(t.Context(), runtimeTenantID, "school-actor", input)
	if err != nil {
		t.Fatalf("create school task: %v", err)
	}
	platformWorker := auth.User{
		ID: auth.PlatformTenantID, TenantID: auth.PlatformTenantID,
		Roles: []string{"page_processing_worker"}, Permissions: []string{"ocr:manage"},
	}
	claim := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", bytes.NewBufferString(
		`{"queue_name":"ocr","worker_service":"ocr-worker","worker_instance_id":"platform-ocr-1","limit":1,"lease_seconds":300}`,
	))
	claim = claim.WithContext(auth.WithUser(claim.Context(), platformWorker))
	claimRec := httptest.NewRecorder()

	handler.Claim(claimRec, claim)

	var claimed struct {
		Tasks []workerruntime.Task `json:"tasks"`
	}
	if claimRec.Code != http.StatusOK || json.NewDecoder(claimRec.Body).Decode(&claimed) != nil || len(claimed.Tasks) != 1 {
		t.Fatalf("global claim expected school task, got %d %s", claimRec.Code, claimRec.Body.String())
	}
	if claimed.Tasks[0].TenantID != runtimeTenantID {
		t.Fatalf("claimed wrong tenant task: %#v", claimed.Tasks[0])
	}

	heartbeat := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/"+task.ID+"/heartbeat", bytes.NewBufferString(
		`{"lease_token":"`+claimed.Tasks[0].LeaseToken+`","worker_service":"ocr-worker","worker_instance_id":"platform-ocr-1","state":"running"}`,
	))
	heartbeat.SetPathValue("taskId", task.ID)
	heartbeat.Header.Set(workerruntime.TaskIDHeader, task.ID)
	heartbeat.Header.Set(workerruntime.TaskLeaseHeader, claimed.Tasks[0].LeaseToken)
	heartbeat.Header.Set(workerruntime.WorkerServiceHeader, "ocr-worker")
	heartbeat.Header.Set(workerruntime.WorkerInstanceIDHeader, "platform-ocr-1")
	heartbeat = heartbeat.WithContext(auth.WithUser(heartbeat.Context(), platformWorker))
	heartbeatRec := httptest.NewRecorder()

	workerruntime.TaskScope(store)(http.HandlerFunc(handler.Heartbeat)).ServeHTTP(heartbeatRec, heartbeat)

	if heartbeatRec.Code != http.StatusOK {
		t.Fatalf("school task heartbeat expected 200, got %d %s", heartbeatRec.Code, heartbeatRec.Body.String())
	}
}

func TestPlatformPageWorkerCannotClaimSubjectiveGradingTask(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	handler := workerruntime.NewHandler(store, auth.NewMemoryStore())
	input := imageQualityTaskInput()
	input.TaskType = "ai_grade"
	input.QueueName = "subjective-grading"
	input.SourceType = "subjective_grading_run"
	input.IdempotencyKey = "subjective:school-task"
	_, err := store.CreateTask(t.Context(), runtimeTenantID, "school-actor", input)
	if err != nil {
		t.Fatal(err)
	}
	pageWorker := auth.User{ID: auth.PlatformTenantID, TenantID: auth.PlatformTenantID, Roles: []string{"page_processing_worker"}, Permissions: []string{"ocr:manage"}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", bytes.NewBufferString(`{"queue_name":"subjective-grading","worker_service":"subjective-grading-worker","worker_instance_id":"attacker","limit":1,"lease_seconds":300}`))
	req = req.WithContext(auth.WithUser(req.Context(), pageWorker))
	rec := httptest.NewRecorder()
	handler.Claim(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerRejectsSensitivePayload(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	handler := workerruntime.NewHandler(store, auth.NewMemoryStore())
	req := requestWithUser(http.MethodPost, "/api/v1/internal/worker/tasks", `{"task_type":"ocr","queue_name":"ocr","source_type":"ocr_task","source_id":"source-1","idempotency_key":"ocr:1","payload_schema_version":"v1","payload":{"student_answer":"secret"}}`)
	rec := httptest.NewRecorder()
	handler.CreateTask(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sensitive payload expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandlerRequiresSourceAdapterForManagedCompletion(t *testing.T) {
	for _, source := range []struct {
		name       string
		taskType   string
		queueName  string
		sourceType string
	}{
		{name: "ocr", taskType: "ocr", queueName: "ocr", sourceType: "ocr_task"},
		{name: "omr", taskType: "omr_extract", queueName: "page-processing", sourceType: "omr_run"},
		{name: "subjective", taskType: "ai_grade", queueName: "subjective-grading", sourceType: "subjective_grading_run"},
		{name: "math-verification", taskType: "evidence_verify", queueName: "math-verification", sourceType: "math_understanding_artifact"},
	} {
		t.Run(source.name, func(t *testing.T) {
			store := workerruntime.NewMemoryStore()
			handler := workerruntime.NewHandler(store, auth.NewMemoryStore())
			task, err := store.CreateTask(t.Context(), runtimeTenantID, "actor-1", workerruntime.CreateTaskInput{
				TaskType: source.taskType, QueueName: source.queueName, SourceType: source.sourceType, SourceID: source.name + "-task-1",
				IdempotencyKey: source.name + ":1", PayloadSchemaVersion: "v1", MaxAttempts: 3, RetryBackoffSeconds: 1,
			})
			if err != nil {
				t.Fatalf("create task: %v", err)
			}
			claimed, err := store.Claim(t.Context(), runtimeTenantID, workerruntime.ClaimInput{
				QueueName: source.queueName, WorkerService: source.name + "-worker", WorkerInstanceID: "worker-a", Limit: 1, LeaseSeconds: 300,
			})
			if err != nil || len(claimed) != 1 {
				t.Fatalf("claim task: %v %#v", err, claimed)
			}
			req := requestWithUser(http.MethodPost, "/api/v1/internal/worker/tasks/"+task.ID+"/complete", `{"lease_token":"`+claimed[0].LeaseToken+`","result_schema_version":"v1","result":{"ok":true},"duration_ms":1}`)
			req.SetPathValue("taskId", task.ID)
			rec := httptest.NewRecorder()
			handler.Complete(rec, req)
			if rec.Code != http.StatusConflict || !bytes.Contains(rec.Body.Bytes(), []byte("worker_source_activation_required")) {
				t.Fatalf("source task generic complete expected 409, got %d %s", rec.Code, rec.Body.String())
			}
			stored, err := store.Get(t.Context(), runtimeTenantID, task.ID)
			if err != nil || stored.Status != workerruntime.StatusLeased {
				t.Fatalf("generic completion must leave managed task leased: %v %#v", err, stored)
			}
		})
	}
}

func requestWithUser(method string, path string, body string) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	user := auth.User{ID: "actor-1", TenantID: runtimeTenantID}
	return req.WithContext(auth.WithUser(req.Context(), user))
}

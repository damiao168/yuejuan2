package subjective_test

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubjectiveWorkerResultValidatesVersionsAndCompletesDomainRun(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage", "orchestrator:manage"})
	store := subjective.NewMemoryStore()
	store.AddContext(tenantID, "segment-1", subjectiveContext("short_answer", 8, "synthetic answer", nil))
	runtime := workerruntime.NewMemoryStore()
	handler := subjective.NewHandler(store, fixedAdapter{}, authStore).WithWorkerRuntimeStore(runtime)
	run, err := store.GetOrCreateRun(context.Background(), tenantID, userID, subjective.CreateRunInput{AnswerSegmentID: "segment-1", AnswerVersion: "answer-v1", QuestionID: "question-short_answer", RubricVersion: "v1", ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: 0.8, RequestID: "worker-request-1"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	task, err := runtime.CreateTask(context.Background(), tenantID, userID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID, PayloadSchemaVersion: "subjective-grade-v1", Payload: map[string]any{"answer_segment_id": "segment-1", "answer_version": "answer-v1", "rubric_version": "v1", "model_version": "model-v1", "prompt_version": "prompt-v1"}, IdempotencyKey: "subjective-task-1", MaxAttempts: 2, RetryBackoffSeconds: 1})
	if err != nil {
		t.Fatalf("create worker task: %v", err)
	}
	claimed, err := runtime.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "subjective-grading", WorkerService: "subjective-worker", WorkerInstanceID: "worker-a", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim worker task: %v %#v", err, claimed)
	}
	badExecuteBody, _ := json.Marshal(map[string]string{"task_id": task.ID, "lease_token": "wrong-lease"})
	badExecuteReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/subjective-grading/runs/"+run.ID+"/execute", bytes.NewReader(badExecuteBody))
	badExecuteReq.SetPathValue("runId", run.ID)
	badExecuteReq = badExecuteReq.WithContext(auth.WithUser(badExecuteReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"orchestrator:manage"}}))
	badExecuteRec := httptest.NewRecorder()
	handler.ExecuteWorker(badExecuteRec, badExecuteReq)
	if badExecuteRec.Code != http.StatusConflict || !strings.Contains(badExecuteRec.Body.String(), "subjective_task_lease_mismatch") {
		t.Fatalf("worker execute with wrong lease expected 409, got %d %s", badExecuteRec.Code, badExecuteRec.Body.String())
	}
	validExecuteBody, _ := json.Marshal(map[string]string{"task_id": task.ID, "lease_token": claimed[0].LeaseToken})
	validExecuteReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/subjective-grading/runs/"+run.ID+"/execute", bytes.NewReader(validExecuteBody))
	validExecuteReq.SetPathValue("runId", run.ID)
	validExecuteReq = validExecuteReq.WithContext(auth.WithUser(validExecuteReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"orchestrator:manage"}}))
	validExecuteRec := httptest.NewRecorder()
	handler.ExecuteWorker(validExecuteRec, validExecuteReq)
	if validExecuteRec.Code != http.StatusOK {
		t.Fatalf("worker execute with active lease expected 200, got %d %s", validExecuteRec.Code, validExecuteRec.Body.String())
	}
	output, err := (fixedAdapter{}).Grade(context.Background(), subjective.AdapterInput{RequestID: run.RequestID, SegmentID: "segment-1", Question: paper.Question{ID: "question-short_answer", QuestionType: "short_answer", Score: 8}, Rubric: paper.Rubric{ID: "rubric-short_answer", Version: "v1"}, AnswerText: "synthetic answer", ModelPolicy: subjective.ModelPolicy{ModelVersion: run.ModelVersion, PromptVersion: run.PromptVersion, MinConfidence: run.MinConfidence}})
	if err != nil {
		t.Fatalf("build worker output: %v", err)
	}
	raw, _ := json.Marshal(subjective.WorkerResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultSchemaVersion: "subjective-grade-result-v1", DurationMS: 22, Output: output})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/subjective-grading/runs/"+run.ID+"/result", bytes.NewReader(raw))
	req.SetPathValue("runId", run.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"orchestrator:manage"}}))
	rec := httptest.NewRecorder()
	handler.CompleteWorker(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("worker result expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated, err := store.GetRun(context.Background(), tenantID, run.ID)
	if err != nil || updated.Status != subjective.RunSucceeded || updated.GradeID == "" {
		t.Fatalf("run should be succeeded and linked to grade: %#v err=%v", updated, err)
	}
	storedTask, err := runtime.Get(context.Background(), tenantID, task.ID)
	if err != nil || storedTask.Status != workerruntime.StatusSucceeded {
		t.Fatalf("worker task should be completed: %#v err=%v", storedTask, err)
	}
}

func TestSubjectiveBatchCreationValidatesSegmentsAndIsIdempotent(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	store := subjective.NewMemoryStore()
	store.AddContext(tenantID, "segment-1", subjectiveContext("short_answer", 8, "answer one", nil))
	store.AddContext(tenantID, "segment-2", subjectiveContext("essay", 20, "answer two", nil))
	for _, segment := range []string{"segment-1", "segment-2"} {
		authStore.AddResourceBoundary("answer_segment", segment, auth.ResourceBoundary{TenantID: tenantID, SchoolID: "school-1"})
	}
	runtime := workerruntime.NewMemoryStore()
	handler := subjective.NewHandler(store, fixedAdapter{}, authStore).WithWorkerRuntimeStore(runtime)
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/subjective-grading-batches", bytes.NewBufferString(body))
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"grading:manage"}}))
		req = req.WithContext(auth.WithAccessScope(req.Context(), auth.AccessScope{ActorID: userID, TenantID: tenantID, TenantWide: true}))
		rec := httptest.NewRecorder()
		handler.CreateBatch(rec, req)
		return rec
	}
	first := request(`{"idempotency_key":"batch-retry-1","segment_ids":["segment-1","segment-2"]}`)
	if first.Code != http.StatusCreated || !strings.Contains(first.Body.String(), `"total_count":2`) || !strings.Contains(first.Body.String(), `"status":"planned"`) {
		t.Fatalf("batch expected planned 201, got %d %s", first.Code, first.Body.String())
	}
	var created struct {
		Batch subjective.GradingBatch `json:"batch"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil || created.Batch.ID == "" {
		t.Fatalf("decode created batch: %v %s", err, first.Body.String())
	}
	assertSubjectiveBatchEnqueueAndLiveProgress(t, handler, store, runtime, created.Batch.ID)
	second := request(`{"idempotency_key":"batch-retry-1","segment_ids":["segment-1","segment-2"]}`)
	if second.Code != http.StatusCreated || !strings.Contains(second.Body.String(), `"subjective-batch-`) {
		t.Fatalf("batch replay should return same durable batch, got %d %s", second.Code, second.Body.String())
	}
	conflict := request(`{"idempotency_key":"batch-retry-1","segment_ids":["segment-2","segment-1"]}`)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), `subjective_grade_idempotency_conflict`) {
		t.Fatalf("changed batch request expected 409 conflict, got %d %s", conflict.Code, conflict.Body.String())
	}
	invalid := request(`{"idempotency_key":"batch-invalid","segment_ids":["segment-1","segment-1"]}`)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("duplicate segment IDs expected 400, got %d %s", invalid.Code, invalid.Body.String())
	}
}

func TestSubjectiveBatchEnqueueReportsPartialSuccessAndCanRetry(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	store := &countingBatchStore{MemoryStore: subjective.NewMemoryStore()}
	store.AddContext(tenantID, "segment-1", subjectiveContext("short_answer", 8, "answer one", nil))
	store.AddContext(tenantID, "segment-2", subjectiveContext("short_answer", 8, "answer two", nil))
	for _, segment := range []string{"segment-1", "segment-2"} {
		authStore.AddResourceBoundary("answer_segment", segment, auth.ResourceBoundary{TenantID: tenantID, SchoolID: "school-1"})
	}
	baseRuntime := workerruntime.NewMemoryStore()
	runtime := &selectiveFailureRuntime{Store: baseRuntime, failedSegmentID: "segment-2"}
	handler := subjective.NewHandler(store, fixedAdapter{}, authStore).WithWorkerRuntimeStore(runtime)

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/subjective-grading-batches", bytes.NewBufferString(`{"idempotency_key":"batch-partial-1","segment_ids":["segment-1","segment-2"]}`))
	createReq = createReq.WithContext(auth.WithUser(createReq.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"grading:manage"}}))
	createReq = createReq.WithContext(auth.WithAccessScope(createReq.Context(), auth.AccessScope{ActorID: userID, TenantID: tenantID, TenantWide: true}))
	createRec := httptest.NewRecorder()
	handler.CreateBatch(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create partial batch: %d %s", createRec.Code, createRec.Body.String())
	}
	var created struct {
		Batch subjective.GradingBatch `json:"batch"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	enqueue := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/subjective-grading-batches/"+created.Batch.ID+"/enqueue", nil)
		req.SetPathValue("batchId", created.Batch.ID)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID, Permissions: []string{"grading:manage"}}))
		rec := httptest.NewRecorder()
		handler.EnqueueBatch(rec, req)
		return rec
	}
	partial := enqueue()
	if partial.Code != http.StatusOK || !strings.Contains(partial.Body.String(), `"partial_success":true`) || !strings.Contains(partial.Body.String(), `"accepted_count":1`) || !strings.Contains(partial.Body.String(), `"code":"task_creation_failed"`) {
		t.Fatalf("partial enqueue must return an actionable summary: %d %s", partial.Code, partial.Body.String())
	}
	assertAuditAction(t, authStore, "subjective.batch_enqueue_partial")
	if store.bulkLoads != 2 || store.singleLoads != 0 {
		t.Fatalf("create and enqueue must use two bulk loads, got bulk=%d single=%d", store.bulkLoads, store.singleLoads)
	}

	runtime.failedSegmentID = ""
	beforePlan, planErr := store.GetEnqueuePlan(context.Background(), tenantID, userID, created.Batch.ID)
	if planErr != nil || beforePlan.Completed {
		t.Fatalf("partial plan: %+v %v", beforePlan, planErr)
	}
	changed := subjectiveContext("short_answer", 8, "changed answer", nil)
	changed.AnswerVersion = "changed-after-partial-enqueue"
	changed.Rubric.Version = "changed-rubric"
	store.AddContext(tenantID, "segment-2", changed)
	retried := enqueue()
	if retried.Code != http.StatusOK || !strings.Contains(retried.Body.String(), `"failed_count":0`) || !strings.Contains(retried.Body.String(), `"queued_count":2`) {
		t.Fatalf("safe retry must complete the missing task without duplication: %d %s", retried.Code, retried.Body.String())
	}
	afterPlan, planErr := store.GetEnqueuePlan(context.Background(), tenantID, userID, created.Batch.ID)
	if planErr != nil || !afterPlan.Completed || afterPlan.Runs[1] != beforePlan.Runs[1] || store.bulkLoads != 2 {
		t.Fatalf("retry changed the frozen plan: %+v %v loads=%d", afterPlan, planErr, store.bulkLoads)
	}
}

type countingBatchStore struct {
	*subjective.MemoryStore
	bulkLoads   int
	singleLoads int
}

func (s *countingBatchStore) LoadContext(ctx context.Context, tenantID string, segmentID string) (subjective.Context, error) {
	s.singleLoads++
	return s.MemoryStore.LoadContext(ctx, tenantID, segmentID)
}

func (s *countingBatchStore) LoadContexts(ctx context.Context, tenantID string, segmentIDs []string) ([]subjective.Context, error) {
	s.bulkLoads++
	return s.MemoryStore.LoadContexts(ctx, tenantID, segmentIDs)
}

type selectiveFailureRuntime struct {
	workerruntime.Store
	failedSegmentID string
}

func (s *selectiveFailureRuntime) CreateTask(ctx context.Context, tenantID string, actorID string, input workerruntime.CreateTaskInput) (workerruntime.Task, error) {
	if input.Payload["answer_segment_id"] == s.failedSegmentID {
		return workerruntime.Task{}, errors.New("synthetic task creation failure")
	}
	return s.Store.CreateTask(ctx, tenantID, actorID, input)
}

func TestSubjectiveGradeRejectsUnknownFieldsAndInvalidIdempotencyKey(t *testing.T) {
	authStore := authStoreWithPermissions(t, []string{"grading:manage"})
	store := subjective.NewMemoryStore()
	store.AddContext(tenantID, "segment-1", subjectiveContext("short_answer", 8, "synthetic answer", nil))
	handler := subjective.NewHandler(store, fixedAdapter{}, authStore)
	unknown := callSubjectiveRequest(t, handler, "segment-1", `{"unexpected":true}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field expected 400, got %d %s", unknown.Code, unknown.Body.String())
	}
	invalidKey := callSubjectiveRequest(t, handler, "segment-1", `{"idempotency_key":"bad key"}`)
	if invalidKey.Code != http.StatusBadRequest {
		t.Fatalf("invalid idempotency key expected 400, got %d %s", invalidKey.Code, invalidKey.Body.String())
	}
}

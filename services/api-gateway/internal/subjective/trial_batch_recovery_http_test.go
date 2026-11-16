package subjective

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestScoringTrialHTTPQueuesOnlySavedSamples(t *testing.T) {
	store, input, _ := trialFixture(t)
	runtime := workerruntime.NewMemoryStore()
	handler := NewHandler(store, NewMockLLMAdapter(), nil).WithWorkerRuntimeStore(runtime)
	teacher := auth.User{ID: "teacher-1", TenantID: trialTestTenant}
	path := map[string]string{"runId": "run-1"}

	create := func(input CreateTrialInput) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		handler.CreateScoringTrial(response, subjectiveTestRequest(t, http.MethodPost, teacher, input, path))
		return response
	}
	readTrials := func() []ScoringTrial {
		response := httptest.NewRecorder()
		handler.ListScoringTrials(response, subjectiveTestRequest(t, http.MethodGet, teacher, nil, path))
		var body struct {
			Trials []ScoringTrial `json:"trials"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
			t.Fatalf("list trials: %v, %d %s", err, response.Code, response.Body)
		}
		return body.Trials
	}

	invalid := input
	invalid.Samples = invalid.Samples[:4]
	response := create(invalid)
	if response.Code != http.StatusBadRequest || response.Header().Get(httpx.ErrorCodeHeader) != "invalid_subjective_grading_input" {
		t.Fatalf("expected invalid baseline response: %d %s", response.Code, response.Body)
	}
	if trials := readTrials(); len(trials) != 0 {
		t.Fatalf("invalid request saved %d trials", len(trials))
	}

	response = create(input)
	if response.Code != http.StatusCreated {
		t.Fatalf("create trial: %d %s", response.Code, response.Body)
	}
	trials := readTrials()
	if len(trials) != 1 || trials[0].Status != "awaiting_enqueue" || len(trials[0].Samples) != len(input.Samples) {
		t.Fatalf("unexpected trials: %#v", trials)
	}
	trial := trials[0]
	response = create(input)
	if response.Code != http.StatusCreated {
		t.Fatalf("replay create trial: %d %s", response.Code, response.Body)
	}
	if trials := readTrials(); len(trials) != 1 || trials[0].ID != trial.ID || trials[0].SampleBatchID != trial.SampleBatchID {
		t.Fatal("replayed request changed the trial or sample batch")
	}

	// 保存人工基线和入队是两个独立步骤。重复入队应返回同一批任务，并继续引用原样本。
	path["trialId"] = trial.ID
	sampleIDs := make([]string, 0, len(input.Samples))
	for _, sample := range input.Samples {
		sampleIDs = append(sampleIDs, sample.SegmentID)
	}
	var firstTasks []string
	for attempt := 0; attempt < 2; attempt++ {
		response = httptest.NewRecorder()
		handler.EnqueueScoringTrial(response, subjectiveTestRequest(t, http.MethodPost, teacher, nil, path))
		var body struct {
			Batch GradingBatch         `json:"batch"`
			Tasks []workerruntime.Task `json:"tasks"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK {
			t.Fatalf("enqueue trial: %v, %d %s", err, response.Code, response.Body)
		}
		if body.Batch.ID != trial.SampleBatchID || !slices.Equal(body.Batch.SegmentIDs, sampleIDs) || len(body.Tasks) != len(sampleIDs) {
			t.Fatalf("enqueue changed sample scope: %s", response.Body)
		}
		ids := make([]string, 0, len(body.Tasks))
		for index, task := range body.Tasks {
			if task.Payload["answer_segment_id"] != sampleIDs[index] {
				t.Fatal("task references another answer segment")
			}
			ids = append(ids, task.ID)
		}
		if attempt == 0 {
			firstTasks = ids
		} else if !slices.Equal(firstTasks, ids) {
			t.Fatal("retry produced new task IDs")
		}
	}
	if trials := readTrials(); len(trials) != 1 || trials[0].Status != "running" || trials[0].Revision != trial.Revision+1 {
		t.Fatalf("sample batch was not marked running exactly once: %#v", trials)
	}
}

func TestBatchRecoveryHTTPUsesOriginalActorAndFrozenRunIDs(t *testing.T) {
	store := NewMemoryStore()
	handler := NewHandler(store, NewMockLLMAdapter(), nil)
	teacher := auth.User{ID: "teacher-1", TenantID: "tenant-1"}
	otherTeacher := auth.User{ID: "teacher-2", TenantID: teacher.TenantID}
	value := serviceTestContext()
	store.AddContext(teacher.TenantID, value.SegmentID, value)
	const commandID = "synthetic-batch-recovery"
	batch, err := store.CreateBatch(t.Context(), teacher.TenantID, teacher.ID, CreateBatchInput{
		IdempotencyKey: commandID,
		SegmentIDs:     []string{value.SegmentID},
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		actor auth.User
		want  string
	}{{teacher, "succeeded"}, {otherTeacher, "not_accepted"}} {
		response := httptest.NewRecorder()
		handler.RecoverBatchCommand(response, subjectiveTestRequest(t, http.MethodGet, tc.actor, nil, map[string]string{"commandId": commandID}))
		var body BatchCommandRecovery
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK || body.CommandID != commandID || body.Status != tc.want {
			t.Fatalf("recover batch: %v, %d %s", err, response.Code, response.Body)
		}
		if tc.want == "succeeded" && (body.Batch == nil || body.Batch.ID != batch.ID) {
			t.Fatal("creator did not recover the original batch")
		}
		if tc.want == "not_accepted" && body.Batch != nil {
			t.Fatal("another actor received a batch response")
		}
	}

	readEnqueue := func(actor auth.User, status string, ids []string) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.RecoverEnqueueCommand(response, subjectiveTestRequest(t, http.MethodGet, actor, nil, map[string]string{"batchId": batch.ID}))
		var body struct {
			CommandID     string   `json:"command_id"`
			BatchID       string   `json:"batch_id"`
			Status        string   `json:"status"`
			RunRequestIDs []string `json:"run_request_ids"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK ||
			body.CommandID != "enqueue:"+batch.ID || body.BatchID != batch.ID || body.Status != status || !slices.Equal(body.RunRequestIDs, ids) {
			t.Fatalf("recover enqueue expected %s: %v, %d %s", status, err, response.Code, response.Body)
		}
	}
	readEnqueue(teacher, "not_accepted", nil)
	input := serviceTestRunInput(value)
	plan := BatchEnqueuePlan{BatchID: batch.ID, CommandID: "enqueue:" + batch.ID, Runs: []CreateRunInput{input}}
	if _, err := store.SaveEnqueuePlan(t.Context(), teacher.TenantID, teacher.ID, plan); err != nil {
		t.Fatal(err)
	}
	readEnqueue(teacher, "processing", []string{input.RequestID})
	readEnqueue(otherTeacher, "not_accepted", nil)

	// 重试时即使调用方用新策略重算了请求身份，已保存的计划仍决定客户端看到的运行。
	plan.Runs = []CreateRunInput{input}
	plan.Runs[0].RequestID = "recomputed-request"
	replayed, err := store.SaveEnqueuePlan(t.Context(), teacher.TenantID, teacher.ID, plan)
	if err != nil || len(replayed.Runs) != 1 || replayed.Runs[0].RequestID != input.RequestID {
		t.Fatalf("existing enqueue plan was not reused: %#v, %v", replayed, err)
	}
	if err := store.CompleteEnqueuePlan(t.Context(), teacher.TenantID, teacher.ID, batch.ID); err != nil {
		t.Fatal(err)
	}
	readEnqueue(teacher, "succeeded", []string{input.RequestID})
}

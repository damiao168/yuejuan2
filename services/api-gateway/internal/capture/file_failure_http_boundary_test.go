package capture

import (
	"net/http"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestCaptureFileFailureHTTPPreservesLeaseUntilValidFailureAndReportsRetryState(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		retryable   bool
		maxAttempts int
		wantTask    string
		wantFile    string
		wantBatch   string
	}{
		{"retryable outage", true, 2, workerruntime.StatusQueued, "queued", "processing"},
		{"retry budget exhausted", true, 1, workerruntime.StatusDeadLetter, "failed", "needs_review"},
		{"permanent decode failure", false, 2, workerruntime.StatusFailed, "failed", "needs_review"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, runtime := NewMemoryStore(), workerruntime.NewMemoryStore()
			batch, err := store.CreateBatch(t.Context(), "tenant", "exam", "operator", CreateBatchInput{Name: "failure recovery", SourceType: "web_upload"})
			if err != nil {
				t.Fatal(err)
			}
			file, err := store.RegisterFile(t.Context(), "tenant", batch.ID, "operator", RegisterFileInput{FileAssetID: "source", IdempotencyKey: "source"}, FileAssetSnapshot{ID: "source", ExamID: "exam", SHA256: "synthetic-hash", SizeBytes: 10})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := store.QueueBatch(t.Context(), "tenant", batch.ID, "operator")
			if err != nil {
				t.Fatal(err)
			}
			_, err = runtime.CreateTask(t.Context(), "tenant", "operator", workerruntime.CreateTaskInput{
				TaskType: "preprocess", QueueName: "page-processing", SourceType: "capture_file", SourceID: file.ID,
				PayloadSchemaVersion: "fixture-v1", IdempotencyKey: "decode-source", MaxAttempts: scenario.maxAttempts,
			})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := runtime.Claim(t.Context(), "tenant", workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
			if err != nil || len(claimed) != 1 {
				t.Fatalf("could not claim decode task: %v", err)
			}
			task := claimed[0]
			audit := &collectionAuditRecorder{}
			mux := http.NewServeMux()
			mux.HandleFunc("POST /api/v1/internal/capture/files/{fileId}/fail", NewHandler(store, nil, nil, runtime, audit).FailFile)
			const prefix = "/api/v1/internal/capture/files/"
			input := FileFailureInput{TaskID: task.ID, LeaseToken: task.LeaseToken, Retryable: scenario.retryable, ErrorCode: "decode_failed", DurationMS: 12}
			requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", prefix+"different-file/fail", captureWorkflowBody(t, input), nil, nil), http.StatusConflict, "capture_task_mismatch")
			invalid := input
			invalid.LeaseToken = "obsolete-lease"
			requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", prefix+file.ID+"/fail", captureWorkflowBody(t, invalid), nil, nil), http.StatusConflict, "capture_worker_lease_invalid")
			before, err := runtime.Get(t.Context(), "tenant", task.ID)
			if err != nil || !reflect.DeepEqual(before, task) || store.files[file.ID].Status != "queued" || store.batches[batch.ID].Revision != queued.Revision || len(audit.events) != 0 {
				t.Fatalf("rejected failure changed worker, file, batch or audit: task=%+v err=%v", before, err)
			}

			// 同一份错误回调能否重试由任务预算决定，HTTP 返回和采集投影必须与运行时终态一致。
			type failureResponse struct {
				File       File
				TaskStatus string `json:"task_status"`
			}
			response := captureWorkflowJSON[failureResponse](t, captureWorkflowRequest(t, mux, "POST", prefix+file.ID+"/fail", captureWorkflowBody(t, input), nil, nil), http.StatusOK)
			failed, err := runtime.Get(t.Context(), "tenant", task.ID)
			if err != nil || response.TaskStatus != scenario.wantTask || failed.Status != scenario.wantTask || response.File.Status != scenario.wantFile ||
				response.File.ErrorCode != input.ErrorCode || len(failed.Attempts) != 1 || failed.Attempts[0].Status != workerruntime.StatusFailed {
				t.Fatalf("worker and file disagree on failure outcome: response=%+v task=%+v err=%v", response, failed, err)
			}
			updated := store.batches[batch.ID]
			wantFailed := 1
			if scenario.wantFile == "queued" {
				wantFailed = 0
			}
			if updated.Status != scenario.wantBatch || updated.Revision != queued.Revision+1 || updated.FailedCount != wantFailed ||
				len(audit.events) != 1 || audit.events[0].Action != "capture_file.processing_failed" || audit.events[0].TargetID != file.ID {
				t.Fatalf("failure did not update batch and audit once: batch=%+v audit=%+v", updated, audit.events)
			}
		})
	}
}

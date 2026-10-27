package capture

import (
	"context"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type registrationRetryProjection struct{ *processing.MemoryStore }

func (s registrationRetryProjection) RetryTarget(context.Context, string, string) (processing.RetryTarget, error) {
	return processing.RetryTarget{SourceType: "page_registration_run", SourceID: "run"}, nil
}

func remediationMemoryRuntimeTask(t *testing.T, runtime *workerruntime.MemoryStore, source, id string) workerruntime.Task {
	t.Helper()
	ctx := context.Background()
	_, err := runtime.CreateTask(ctx, "tenant", "actor", workerruntime.CreateTaskInput{TaskType: "preprocess", QueueName: "page-processing", SourceType: source, SourceID: id, PayloadSchemaVersion: "fixture-v1", IdempotencyKey: id, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant", workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	return claimed[0]
}

func TestFileFailureCommandKeepsSourceAndLeaseTogether(t *testing.T) {
	ctx := context.Background()
	runtime := workerruntime.NewMemoryStore()
	domain := NewMemoryStore()
	domain.batches["batch"] = Batch{ID: "batch", TenantID: "tenant", Status: "processing"}
	domain.files["file"] = File{ID: "file", TenantID: "tenant", CaptureBatchID: "batch", Status: "queued"}
	task := remediationMemoryRuntimeTask(t, runtime, "capture_file", "file")
	input := FileFailureInput{TaskID: task.ID, LeaseToken: "wrong-lease", ErrorCode: "decode_failed"}
	if _, _, err := domain.SubmitFileFailureCommand(ctx, "tenant", "file", input, runtime); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("wrong lease: %v", err)
	}
	if domain.files["file"].Status != "queued" {
		t.Fatal("invalid lease changed source")
	}
	input.LeaseToken = task.LeaseToken
	file, status, err := domain.SubmitFileFailureCommand(ctx, "tenant", "file", input, runtime)
	if err != nil || status != workerruntime.StatusFailed || file.Status != "failed" || domain.batches["batch"].FailedCount != 1 {
		t.Fatalf("failure not projected: %#v %s %v", file, status, err)
	}
	if _, err = domain.QueueBatch(ctx, "tenant", "batch", "actor"); err != nil {
		t.Fatalf("normal batch retry cannot select failed file: %v", err)
	}
}

func TestWorkbenchRegistrationRetryRestoresSourceAndAcceptsResult(t *testing.T) {
	ctx := context.Background()
	domain := NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	task := remediationMemoryRuntimeTask(t, runtime, "page_registration_run", "run")
	if _, err := runtime.Fail(ctx, "tenant", task.ID, workerruntime.FailInput{LeaseToken: task.LeaseToken, ErrorCode: "terminal"}); err != nil {
		t.Fatal(err)
	}
	domain.batches["batch"] = Batch{ID: "batch", TenantID: "tenant", Status: "processing"}
	domain.pages["page"] = Page{ID: "page", TenantID: "tenant", CaptureBatchID: "batch"}
	domain.registrations["run"] = RegistrationRun{ID: "run", CapturePageID: "page", RuntimeTaskID: task.ID, ProcessingStatus: "terminal_error", ErrorCode: "terminal"}
	service := processing.NewService(registrationRetryProjection{processing.NewMemoryStore()}, runtime).WithRegistrationRetryStore(domain)
	queued, err := service.Retry(ctx, "tenant", "exception")
	if err != nil || queued.Status != workerruntime.StatusQueued || domain.registrations["run"].ProcessingStatus != "processing" {
		t.Fatalf("retry: %#v %v", queued, err)
	}
	// 重复点击及旧版本遗留的 queued/terminal_error 都通过同一原子恢复入口。
	current := domain.registrations["run"]
	current.ProcessingStatus = "terminal_error"
	domain.registrations["run"] = current
	if _, err = service.Retry(ctx, "tenant", "exception"); err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant", workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatal(err)
	}
	result, err := domain.SubmitRegistrationResultCommand(ctx, "tenant", "run", RegistrationResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultVersion: "fixture-v1", Confidence: .99, Method: "fixture"}, runtime)
	if err != nil || result.ProcessingStatus != "completed" {
		t.Fatalf("successful recomputation rejected: %#v %v", result, err)
	}
	if _, err = domain.RetryRegistrationTask(ctx, "tenant", "run", task.ID, runtime); !errors.Is(err, workerruntime.ErrInvalidTransition) {
		t.Fatalf("completed source retried: %v", err)
	}
	if _, err = domain.RetryRegistrationTask(ctx, "other", "run", task.ID, runtime); !errors.Is(err, workerruntime.ErrNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
}

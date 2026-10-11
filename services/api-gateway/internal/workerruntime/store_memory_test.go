package workerruntime_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

const runtimeTenantID = "tenant-1"

func TestCreateTaskIsIdempotent(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	input := imageQualityTaskInput()

	first, err := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", input)
	if err != nil {
		t.Fatalf("create first task: %v", err)
	}
	second, err := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", input)
	if err != nil {
		t.Fatalf("create duplicate task: %v", err)
	}
	if first.ID != second.ID || first.Revision != 1 || second.Revision != first.Revision {
		t.Fatalf("idempotent create returned different tasks: %s != %s", first.ID, second.ID)
	}
}

func TestClaimHeartbeatAndComplete(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	task, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())

	claimed, err := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-a", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim task: %v %#v", err, claimed)
	}
	if claimed[0].ID != task.ID || claimed[0].Status != workerruntime.StatusLeased || claimed[0].LeaseToken == "" || claimed[0].AttemptCount != 1 {
		t.Fatalf("unexpected claimed task: %#v", claimed[0])
	}
	if claimed[0].Revision <= task.Revision {
		t.Fatalf("claim must advance task revision: created=%d claimed=%d", task.Revision, claimed[0].Revision)
	}

	running, err := store.Heartbeat(context.Background(), runtimeTenantID, task.ID, workerruntime.HeartbeatInput{
		LeaseToken: claimed[0].LeaseToken, WorkerService: "image-quality-worker", WorkerInstanceID: "worker-a", State: workerruntime.StatusRunning, LeaseSeconds: 600,
		Progress: map[string]any{"stage": "quality", "completed": 2, "total": 5},
	})
	if err != nil || running.Status != workerruntime.StatusRunning {
		t.Fatalf("heartbeat task: %v %#v", err, running)
	}
	if running.LeaseExpiresAt == nil || !running.LeaseExpiresAt.After(*claimed[0].LeaseExpiresAt) {
		t.Fatalf("heartbeat should extend lease: before=%v after=%v", claimed[0].LeaseExpiresAt, running.LeaseExpiresAt)
	}
	if running.Revision <= claimed[0].Revision {
		t.Fatalf("heartbeat must advance task revision: claimed=%d running=%d", claimed[0].Revision, running.Revision)
	}
	if running.Progress["stage"] != "quality" || running.Progress["completed"] != float64(2) || running.Progress["total"] != float64(5) {
		t.Fatalf("heartbeat progress was not persisted: %#v", running.Progress)
	}

	assertWorkerCompletionAndDuplicateLease(t, store, task, claimed[0], running)
}

func TestRetryAndDeadLetter(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	input := imageQualityTaskInput()
	input.MaxAttempts = 2
	task, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", input)

	first := mustClaim(t, store)
	retried, err := store.Fail(context.Background(), runtimeTenantID, task.ID, workerruntime.FailInput{
		LeaseToken: first.LeaseToken, Retryable: true, ErrorCode: "storage_timeout",
	})
	if err != nil || retried.Status != workerruntime.StatusQueued || retried.NotBefore == nil {
		t.Fatalf("retry task: %v %#v", err, retried)
	}
	store.MakeRunnableForTest(task.ID)

	second := mustClaim(t, store)
	dead, err := store.Fail(context.Background(), runtimeTenantID, task.ID, workerruntime.FailInput{
		LeaseToken: second.LeaseToken, Retryable: true, ErrorCode: "storage_timeout",
	})
	if err != nil || dead.Status != workerruntime.StatusDeadLetter {
		t.Fatalf("dead-letter task: %v %#v", err, dead)
	}

	requeued, err := store.Requeue(context.Background(), runtimeTenantID, task.ID)
	if err != nil || requeued.Status != workerruntime.StatusQueued {
		t.Fatalf("requeue dead letter: %v %#v", err, requeued)
	}
	if len(requeued.Attempts) != 2 {
		t.Fatalf("attempt history was lost: %#v", requeued.Attempts)
	}
	if requeued.MaxAttempts <= requeued.AttemptCount {
		t.Fatalf("manual requeue must grant another attempt: %#v", requeued)
	}
}

func TestExpiredLeaseCanBeReclaimedAndRejectsLateComplete(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	task, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())
	first := mustClaim(t, store)
	store.ForceExpireLeaseForTest(task.ID)

	second := mustClaim(t, store)
	if second.LeaseToken == first.LeaseToken || second.AttemptCount != 2 {
		t.Fatalf("expected a new lease and attempt: first=%#v second=%#v", first, second)
	}
	_, err := store.Complete(context.Background(), runtimeTenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: first.LeaseToken, ResultSchemaVersion: "v1", Result: map[string]any{"ok": true},
	})
	if !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("late complete should fail with lease mismatch, got %v", err)
	}
}

func TestExpiredLeaseAtAttemptLimitIsAtomicallyDeadLettered(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	input := imageQualityTaskInput()
	input.MaxAttempts = 1
	task, err := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", input)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	_ = mustClaim(t, store)
	store.ForceExpireLeaseForTest(task.ID)

	var claimedCount atomic.Int32
	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			claimed, claimErr := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
				QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-limit", Limit: 1, LeaseSeconds: 300,
			})
			if claimErr != nil {
				t.Errorf("claim %d: %v", worker, claimErr)
				return
			}
			claimedCount.Add(int32(len(claimed)))
		}(worker)
	}
	wg.Wait()
	if claimedCount.Load() != 0 {
		t.Fatalf("expired task at max attempts must not be re-leased, got %d claims", claimedCount.Load())
	}

	stored, err := store.Get(context.Background(), runtimeTenantID, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Status != workerruntime.StatusDeadLetter || stored.AttemptCount != 1 || stored.AttemptCount > stored.MaxAttempts {
		t.Fatalf("unexpected exhausted task: %#v", stored)
	}
	if stored.CompletedAt == nil || stored.LeaseToken != "" || stored.LeaseExpiresAt != nil {
		t.Fatalf("dead-lettered task must be terminal without an active lease: %#v", stored)
	}
	if len(stored.Attempts) != 1 || stored.Attempts[0].Status != "lease_expired" || stored.Attempts[0].CompletedAt == nil {
		t.Fatalf("expired attempt must be closed exactly once: %#v", stored.Attempts)
	}
}

func TestExhaustedTaskDoesNotBlockNextClaimableTask(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	firstInput := imageQualityTaskInput()
	firstInput.MaxAttempts = 1
	first, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", firstInput)
	secondInput := imageQualityTaskInput()
	secondInput.SourceID = "quality-run-2"
	secondInput.IdempotencyKey = "image-quality-run:quality-run-2"
	second, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", secondInput)
	_ = mustClaim(t, store)
	store.ForceExpireLeaseForTest(first.ID)

	claimed, err := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-b", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 || claimed[0].ID != second.ID {
		t.Fatalf("claim behind exhausted task: %v %#v", err, claimed)
	}
	dead, _ := store.Get(context.Background(), runtimeTenantID, first.ID)
	if dead.Status != workerruntime.StatusDeadLetter {
		t.Fatalf("first task should be dead-lettered: %#v", dead)
	}
}

func TestRunAtomicRollsBackRuntimeMutations(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	wantErr := errors.New("source mutation failed")
	err := store.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		if _, createErr := tx.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput()); createErr != nil {
			return createErr
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("atomic callback error: %v", err)
	}
	if _, err := store.GetBySource(context.Background(), runtimeTenantID, "image_quality_run", "quality-run-1"); !errors.Is(err, workerruntime.ErrNotFound) {
		t.Fatalf("rolled-back task must not remain visible, got %v", err)
	}

	var created workerruntime.Task
	if err := store.RunAtomic(func(tx *workerruntime.MemoryTx) error {
		var createErr error
		created, createErr = tx.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())
		return createErr
	}); err != nil {
		t.Fatalf("commit atomic create: %v", err)
	}
	if created.ID != "worker-task-1" {
		t.Fatalf("rollback must restore deterministic memory identifiers, got %s", created.ID)
	}
}

func TestCreateSucceededTaskIsIdempotentAndHashChecked(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	complete := workerruntime.CompleteInput{ResultSchemaVersion: "import.v1", Result: map[string]any{"b": 2, "a": 1}, DurationMS: 12}
	first, err := store.CreateSucceededTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput(), complete)
	if err != nil || first.Status != workerruntime.StatusSucceeded || first.AttemptCount != 0 {
		t.Fatalf("seed succeeded task: %v %#v", err, first)
	}
	second, err := store.CreateSucceededTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput(), workerruntime.CompleteInput{
		ResultSchemaVersion: "import.v1", Result: map[string]any{"a": 1, "b": 2}, DurationMS: 99,
	})
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent seed: %v %#v", err, second)
	}
	_, err = store.CreateSucceededTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput(), workerruntime.CompleteInput{
		ResultSchemaVersion: "import.v1", Result: map[string]any{"a": 3},
	})
	if !errors.Is(err, workerruntime.ErrConflict) {
		t.Fatalf("changed trusted result must conflict, got %v", err)
	}
}

func TestCanonicalResultHashIgnoresMapInsertionOrder(t *testing.T) {
	first, err := workerruntime.ResultPayloadHash("v1", map[string]any{"z": 2, "a": map[string]any{"y": true, "x": 1}})
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	second, err := workerruntime.ResultPayloadHash("v1", map[string]any{"a": map[string]any{"x": 1, "y": true}, "z": 2})
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if first != second {
		t.Fatalf("canonical result hash changed with map order: %s != %s", first, second)
	}
}

func TestCancelAndMetrics(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	first, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())
	secondInput := imageQualityTaskInput()
	secondInput.SourceID = "quality-run-2"
	secondInput.IdempotencyKey = "image-quality-run:quality-run-2"
	_, _ = store.CreateTask(context.Background(), runtimeTenantID, "actor-1", secondInput)

	cancelled, err := store.Cancel(context.Background(), runtimeTenantID, first.ID)
	if err != nil || cancelled.Status != workerruntime.StatusCancelled {
		t.Fatalf("cancel task: %v %#v", err, cancelled)
	}
	metrics, err := store.Metrics(context.Background(), runtimeTenantID)
	if err != nil || len(metrics.Queues) != 1 || metrics.Queues[0].Queued != 1 || metrics.Queues[0].Cancelled != 1 {
		t.Fatalf("unexpected metrics: %v %#v", err, metrics)
	}
}

func TestEmptyClaimReportsIdleWorkerPresence(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	claimed, err := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
		QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "ocr-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 0 {
		t.Fatalf("empty claim: %v %#v", err, claimed)
	}

	metrics, err := store.Metrics(context.Background(), runtimeTenantID)
	if err != nil || len(metrics.Workers) != 1 {
		t.Fatalf("idle worker presence missing: %v %#v", err, metrics.Workers)
	}
	worker := metrics.Workers[0]
	if worker.WorkerService != "ocr-worker" || worker.WorkerInstanceID != "ocr-1" || worker.QueueName != "ocr" || worker.Metadata["state"] != "polling" {
		t.Fatalf("unexpected idle worker presence: %#v", worker)
	}
}

func TestConcurrentClaimsReturnTaskOnlyOnce(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	_, _ = store.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())

	var claimedCount atomic.Int32
	var wg sync.WaitGroup
	for index := 0; index < 16; index++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			claimed, err := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
				QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-concurrent", Limit: 1, LeaseSeconds: 300,
			})
			if err != nil {
				t.Errorf("claim %d: %v", worker, err)
				return
			}
			claimedCount.Add(int32(len(claimed)))
		}(index)
	}
	wg.Wait()
	if claimedCount.Load() != 1 {
		t.Fatalf("one queued task must be claimed exactly once, got %d claims", claimedCount.Load())
	}
}

func TestConcurrentCompleteAndCancelProduceOneTerminalResult(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	task, _ := store.CreateTask(context.Background(), runtimeTenantID, "actor-1", imageQualityTaskInput())
	claimed := mustClaim(t, store)

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, err := store.Complete(context.Background(), runtimeTenantID, task.ID, workerruntime.CompleteInput{
			LeaseToken: claimed.LeaseToken, ResultSchemaVersion: "v1", Result: map[string]any{"ok": true},
		})
		results <- err
	}()
	go func() {
		<-start
		_, err := store.Cancel(context.Background(), runtimeTenantID, task.ID)
		results <- err
	}()
	close(start)

	first, second := <-results, <-results
	successes := 0
	for _, err := range []error{first, second} {
		if err == nil {
			successes++
		} else if !errors.Is(err, workerruntime.ErrInvalidTransition) {
			t.Fatalf("losing terminal transition should be rejected, got %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("exactly one terminal transition must succeed, got %d", successes)
	}
	final, err := store.Get(context.Background(), runtimeTenantID, task.ID)
	if err != nil || (final.Status != workerruntime.StatusSucceeded && final.Status != workerruntime.StatusCancelled) {
		t.Fatalf("unexpected terminal task: %v %#v", err, final)
	}
}

func mustClaim(t *testing.T, store *workerruntime.MemoryStore) workerruntime.Task {
	t.Helper()
	claimed, err := store.Claim(context.Background(), runtimeTenantID, workerruntime.ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-a", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	return claimed[0]
}

func imageQualityTaskInput() workerruntime.CreateTaskInput {
	return workerruntime.CreateTaskInput{
		TaskType: "image_quality", QueueName: "image-quality", SourceType: "image_quality_run", SourceID: "quality-run-1",
		IdempotencyKey: "image-quality-run:quality-run-1", PayloadSchemaVersion: "image-quality.v1",
		Payload: map[string]any{"submission_page_id": "page-1"}, MaxAttempts: 3, RetryBackoffSeconds: 1,
	}
}

var _ = time.Second

// 完成后原租约可幂等重放；过期令牌必须失败且不得推进版本。
func assertWorkerCompletionAndDuplicateLease(t *testing.T, store *workerruntime.MemoryStore, task workerruntime.Task, claimed workerruntime.Task, running workerruntime.Task) {
	t.Helper()
	completed, err := store.Complete(context.Background(), runtimeTenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: claimed.LeaseToken, ResultSchemaVersion: "image-quality-result.v1", Result: map[string]any{"quality_status": "passed"}, DurationMS: 1200,
	})
	if err != nil || completed.Status != workerruntime.StatusSucceeded || completed.CompletedAt == nil {
		t.Fatalf("complete task: %v %#v", err, completed)
	}
	if completed.Revision <= running.Revision {
		t.Fatalf("completion must advance task revision: running=%d completed=%d", running.Revision, completed.Revision)
	}

	duplicate, err := store.Complete(context.Background(), runtimeTenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: claimed.LeaseToken, ResultSchemaVersion: "image-quality-result.v1", Result: map[string]any{"quality_status": "passed"}, DurationMS: 1200,
	})
	if err != nil || duplicate.Status != workerruntime.StatusSucceeded {
		t.Fatalf("duplicate complete should be idempotent: %v %#v", err, duplicate)
	}
	if duplicate.Revision != completed.Revision {
		t.Fatalf("idempotent completion must not advance revision: completed=%d duplicate=%d", completed.Revision, duplicate.Revision)
	}
	_, err = store.Complete(context.Background(), runtimeTenantID, task.ID, workerruntime.CompleteInput{
		LeaseToken: "stale-token", ResultSchemaVersion: "image-quality-result.v1", Result: map[string]any{"quality_status": "passed"}, DurationMS: 1200,
	})
	if !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("duplicate complete with a stale token must be rejected, got %v", err)
	}
}

package paper

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestParseFinalizationContextSurvivesExecutionCancellation(t *testing.T) {
	executionCtx, cancelExecution := context.WithCancel(context.Background())
	cancelExecution()
	finalizeCtx, cancelFinalize := finalizationContext(executionCtx)
	defer cancelFinalize()
	if err := finalizeCtx.Err(); err != nil {
		t.Fatalf("finalization inherited canceled execution context: %v", err)
	}
	deadline, ok := finalizeCtx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > parseFinalizationTimeout {
		t.Fatalf("finalization context is not independently bounded: deadline=%v ok=%v", deadline, ok)
	}
}

func TestParseTaskExecutorLeaseAlwaysExceedsConfiguredTimeout(t *testing.T) {
	service := NewDocumentImportService(NewPostgresStore(nil), nil, nil, "", "", 0)
	runtime := workerruntime.NewMemoryStore()
	for _, timeout := range []time.Duration{0, 30 * time.Second, 58 * time.Minute} {
		executor, err := NewParseTaskExecutor(service, runtime, timeout)
		if err != nil {
			t.Fatalf("timeout %s rejected: %v", timeout, err)
		}
		if executor.executionTimeout < service.parserTimeout || executor.executionTimeout < timeout {
			t.Fatalf("executor timeout %s does not cover parser timeout %s and requested timeout %s", executor.executionTimeout, service.parserTimeout, timeout)
		}
		if time.Duration(executor.leaseSeconds)*time.Second < executor.executionTimeout+time.Minute {
			t.Fatalf("timeout %s has unsafe lease %s", timeout, time.Duration(executor.leaseSeconds)*time.Second)
		}
	}
}

func TestParseExecutionErrorCarriesCorrelationWithoutLeaseToken(t *testing.T) {
	task := workerruntime.Task{ID: "task-1", AttemptCount: 3, LeaseToken: "must-not-leak"}
	binding := PaperImportRunBinding{ImportID: "import-1", RunID: "run-2", Generation: 2}
	err := executionError(workerruntime.ErrLeaseMismatch, task, binding, "paper_import_lease_mismatch", "rejected")
	var detail *ParseExecutionError
	if !errors.As(err, &detail) || !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("correlated error must preserve its cause: %v", err)
	}
	if detail.ImportID != "import-1" || detail.RunID != "run-2" || detail.Generation != 2 || detail.TaskID != "task-1" || detail.Attempt != 3 {
		t.Fatalf("missing correlation fields: %#v", detail)
	}
	if strings.Contains(err.Error(), task.LeaseToken) || detail.LeaseValidation != "rejected" {
		t.Fatalf("lease diagnostic must report only the validation conclusion: %#v", detail)
	}
}

func TestParseTaskExecutorRejectsTimeoutWithoutLeaseSafetyMargin(t *testing.T) {
	service := NewDocumentImportService(NewPostgresStore(nil), nil, nil, "", "", 0)
	_, err := NewParseTaskExecutor(service, workerruntime.NewMemoryStore(), 60*time.Minute)
	if err == nil || !strings.Contains(err.Error(), "safety margin") {
		t.Fatalf("unsafe timeout accepted: %v", err)
	}
}

func TestParseHeartbeatDoesNotAdvanceEventSequenceWithoutNewProgress(t *testing.T) {
	ctx := context.Background()
	runtime := workerruntime.NewMemoryStore()
	created, err := runtime.CreateTask(ctx, "tenant-1", "actor-1", workerruntime.CreateTaskInput{
		TaskType: "paper_parse", QueueName: "paper-parse", SourceType: "paper_import_parse", SourceID: "input-1",
		IdempotencyKey: "paper-parse:input-1", PayloadSchemaVersion: "paper-parse.v1", MaxAttempts: 1, RetryBackoffSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, "tenant-1", workerruntime.ClaimInput{
		QueueName: "paper-parse", WorkerService: "api-gateway-paper-parser", WorkerInstanceID: "parser-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim parse task: %v %#v", err, claimed)
	}
	executionCtx, cancelExecution := context.WithCancel(ctx)
	heartbeat := newParseTaskHeartbeat(runtime, claimed[0], "parser-1", 300, 5*time.Millisecond, cancelExecution)
	if err := heartbeat.setProgress(executionCtx, "model_request", 0, 2, "compact_model", "正在处理"); err != nil {
		t.Fatal(err)
	}
	first, _ := runtime.Get(ctx, "tenant-1", created.ID)
	heartbeat.start(executionCtx)
	time.Sleep(18 * time.Millisecond)
	if err := heartbeat.close(); err != nil {
		t.Fatal(err)
	}
	last, _ := runtime.Get(ctx, "tenant-1", created.ID)
	cancelExecution()
	if last.Progress["event_seq"] != first.Progress["event_seq"] {
		t.Fatalf("liveness heartbeat changed factual event sequence: first=%#v last=%#v", first.Progress, last.Progress)
	}
	if !last.UpdatedAt.After(first.UpdatedAt) {
		t.Fatalf("periodic heartbeat did not update liveness timestamp: first=%v last=%v", first.UpdatedAt, last.UpdatedAt)
	}
}

func TestNormalizedParseProgressRejectsInventedOrFractionalCounts(t *testing.T) {
	phase, completed, total, route := normalizedParseProgress(map[string]any{
		"phase": "invented", "route": "invented", "completed": 1.5, "total": 2.0,
	})
	if phase != "structuring" || completed != 0 || total != 0 || route != "" {
		t.Fatalf("untrusted progress was accepted: %q %d/%d %q", phase, completed, total, route)
	}
}

package workerruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCompletionScopeDoesNotRestoreFinishedTaskExecution(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateTask(context.Background(), "tenant-school", "actor", CreateTaskInput{TaskType: "ocr", QueueName: "ocr", SourceType: "ocr_task", SourceID: "ocr-1",
		Payload: map[string]any{"exam_id": "exam-1"}, PayloadSchemaVersion: "ocr-v1", IdempotencyKey: "complete-scope", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), "tenant-school", ClaimInput{QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	task := claimed[0]
	if _, err := store.Complete(context.Background(), task.TenantID, task.ID, CompleteInput{LeaseToken: task.LeaseToken, ResultSchemaVersion: "ocr-result-v1", Result: map[string]any{"text": "accepted"}}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	completed := store.tasks[task.ID]
	expired := time.Now().Add(-time.Hour)
	completed.LeaseExpiresAt = &expired
	store.tasks[task.ID] = completed
	store.mu.Unlock()
	next := RequireTaskSource("ocr_task", "id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, scenario := range []struct {
		name                    string
		completion              bool
		instance, lease, source string
		want                    int
	}{
		{name: "same completion", completion: true, instance: "worker-1", lease: task.LeaseToken, source: "ocr-1", want: http.StatusNoContent},
		{name: "execution", instance: "worker-1", lease: task.LeaseToken, source: "ocr-1", want: http.StatusForbidden},
		{name: "other worker", completion: true, instance: "worker-2", lease: task.LeaseToken, source: "ocr-1", want: http.StatusForbidden},
		{name: "other lease", completion: true, instance: "worker-1", lease: "other", source: "ocr-1", want: http.StatusForbidden},
		{name: "other source", completion: true, instance: "worker-1", lease: task.LeaseToken, source: "ocr-2", want: http.StatusForbidden},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			req := taskRequest(task, scenario.source)
			req.Header.Set(WorkerInstanceIDHeader, scenario.instance)
			req.Header.Set(TaskLeaseHeader, scenario.lease)
			handler := TaskScope(store)(next)
			if scenario.completion {
				handler = TaskCompletionScope(store)(next)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != scenario.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, scenario.want, rec.Body.String())
			}
		})
	}
}

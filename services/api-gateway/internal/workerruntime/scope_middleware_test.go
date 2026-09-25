package workerruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestTaskScopeDerivesTenantAndRejectsCallerSelectedResource(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateTask(context.Background(), "tenant-school", "actor-1", CreateTaskInput{
		TaskType: "ocr", QueueName: "ocr", SourceType: "ocr_task", SourceID: "ocr-1",
		Payload:              map[string]any{"exam_id": "exam-1", "source_file_asset_id": "file-1"},
		PayloadSchemaVersion: "ocr-v1", IdempotencyKey: "ocr-1", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), "tenant-school", ClaimInput{
		QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	task := claimed[0]

	handler := TaskScope(store)(RequireTaskSource("ocr_task", "id")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserFromContext(r.Context())
		scope, _ := auth.AccessScopeFromContext(r.Context())
		if user.TenantID != "tenant-school" || user.ID != "" || !scope.AllowsExam("exam-1") {
			t.Fatalf("unexpected derived worker scope: user=%#v scope=%#v", user, scope)
		}
		w.WriteHeader(http.StatusNoContent)
	})))

	req := taskRequest(task, "ocr-1")
	// The legacy caller-selected tenant header must have no authority.
	req.Header.Set("X-EduGrade-Tenant-ID", "attacker-tenant")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected valid task capability, got %d %s", rec.Code, rec.Body.String())
	}

	forged := taskRequest(task, "ocr-2")
	forgedRec := httptest.NewRecorder()
	handler.ServeHTTP(forgedRec, forged)
	if forgedRec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-resource request to be forbidden, got %d %s", forgedRec.Code, forgedRec.Body.String())
	}
}

func TestTaskScopeRequiresExactLeaseCapability(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateTask(context.Background(), "tenant-school", "actor-1", CreateTaskInput{
		TaskType: "image_quality", QueueName: "image-quality", SourceType: "image_quality_run", SourceID: "run-1",
		Payload:              map[string]any{"source_download_url": "/api/v1/files/file-1/download"},
		PayloadSchemaVersion: "image-quality-v1", IdempotencyKey: "quality-1", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), "tenant-school", ClaimInput{
		QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	task := claimed[0]
	handler := TaskScope(store)(RequireTaskFile("id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))

	req := taskRequest(task, "file-1")
	req.Header.Set(TaskLeaseHeader, "forged-lease")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected forged lease to be forbidden, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestTaskScopeRejectsPageWorkerWithSubjectiveLease(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.CreateTask(context.Background(), "tenant-school", "actor-1", CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: "item-1",
		Payload: map[string]any{"answer_file_asset_id": "file-1"}, PayloadSchemaVersion: "v1", IdempotencyKey: "subjective-1", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(context.Background(), "tenant-school", ClaimInput{QueueName: "subjective-grading", WorkerService: "subjective-grading-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %v %#v", err, claimed)
	}
	handler := TaskScope(store)(RequireTaskFile("id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })))
	req := taskRequest(claimed[0], "file-1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("page worker with subjective lease expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}

func taskRequest(task Task, resourceID string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/resource/"+resourceID, nil)
	req.SetPathValue("id", resourceID)
	req.Header.Set(TaskIDHeader, task.ID)
	req.Header.Set(TaskLeaseHeader, task.LeaseToken)
	req.Header.Set(WorkerServiceHeader, task.WorkerService)
	req.Header.Set(WorkerInstanceIDHeader, task.WorkerInstanceID)
	return req.WithContext(auth.WithUser(req.Context(), auth.User{
		ID: auth.PlatformTenantID, TenantID: auth.PlatformTenantID, Roles: []string{"page_processing_worker"},
	}))
}

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

type mathWorkerIdentityCase struct {
	name    string
	roles   []string
	service string
	allowed bool
}

type mathWorkerLeaseBoundary struct {
	name, lease, resource string
	allowed               bool
}

func mathIdentityTaskInput(key string) workerruntime.CreateTaskInput {
	return workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: "math-understanding", SourceType: "answer_segment", SourceID: "math-segment",
		Payload: map[string]any{"exam_id": "math-exam"}, PayloadSchemaVersion: "math-understanding-input-v1", IdempotencyKey: key}
}

func mathIdentityWorker(tc mathWorkerIdentityCase) auth.User {
	return auth.User{ID: auth.PlatformTenantID, TenantID: auth.PlatformTenantID, Roles: tc.roles, Permissions: []string{"ocr:manage"}}
}

func assertMathWorkerClaimIdentity(t *testing.T, tc mathWorkerIdentityCase) {
	t.Helper()
	store := workerruntime.NewMemoryStore()
	handler := workerruntime.NewHandler(store, auth.NewMemoryStore())
	if _, err := store.CreateTask(t.Context(), runtimeTenantID, "school-actor", mathIdentityTaskInput("math-understanding:1")); err != nil {
		t.Fatal(err)
	}
	body := `{"queue_name":"math-understanding","worker_service":"` + tc.service + `","worker_instance_id":"math-worker","limit":1,"lease_seconds":300}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", bytes.NewBufferString(body))
	req = req.WithContext(auth.WithUser(req.Context(), mathIdentityWorker(tc)))
	rec := httptest.NewRecorder()
	handler.Claim(rec, req)
	if !tc.allowed {
		if rec.Code != http.StatusForbidden || !bytes.Contains(rec.Body.Bytes(), []byte("worker_service_forbidden")) {
			t.Fatalf("claim expected identity rejection, got %d %s", rec.Code, rec.Body.String())
		}
		return
	}
	var response struct {
		Tasks []workerruntime.Task `json:"tasks"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil || len(response.Tasks) != 1 || response.Tasks[0].TenantID != runtimeTenantID {
		t.Fatalf("claim expected school math task, got %d %s", rec.Code, rec.Body.String())
	}
}

func assertMathWorkerLeaseBoundaries(t *testing.T, tc mathWorkerIdentityCase) {
	t.Helper()
	// 直接建立租约，保留错误服务/角色持有完整租约仍应被白名单拒绝的负例。
	store := workerruntime.NewMemoryStore()
	if _, err := store.CreateTask(t.Context(), runtimeTenantID, "school-actor", mathIdentityTaskInput("math-understanding:scope")); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Claim(t.Context(), runtimeTenantID, workerruntime.ClaimInput{QueueName: "math-understanding", WorkerService: tc.service, WorkerInstanceID: "math-worker", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("seed lease: %v %#v", err, claimed)
	}
	task := claimed[0]
	for _, boundary := range []mathWorkerLeaseBoundary{
		{name: "current lease", lease: task.LeaseToken, resource: task.SourceID, allowed: tc.allowed},
		{name: "forged lease", lease: "forged-lease", resource: task.SourceID},
		{name: "different resource", lease: task.LeaseToken, resource: "other-segment"},
	} {
		t.Run(boundary.name, func(t *testing.T) { assertMathWorkerScopedRequest(t, store, task, tc, boundary) })
	}
}

func assertMathWorkerScopedRequest(t *testing.T, store *workerruntime.MemoryStore, task workerruntime.Task, tc mathWorkerIdentityCase, boundary mathWorkerLeaseBoundary) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/resource/"+boundary.resource, nil)
	req.SetPathValue("id", boundary.resource)
	req.Header.Set(workerruntime.TaskIDHeader, task.ID)
	req.Header.Set(workerruntime.TaskLeaseHeader, boundary.lease)
	req.Header.Set(workerruntime.WorkerServiceHeader, tc.service)
	req.Header.Set(workerruntime.WorkerInstanceIDHeader, "math-worker")
	req.Header.Set("X-EduGrade-Tenant-ID", "caller-selected-tenant")
	req = req.WithContext(auth.WithUser(req.Context(), mathIdentityWorker(tc)))
	rec := httptest.NewRecorder()
	workerruntime.TaskScope(store)(workerruntime.RequireTaskSource("answer_segment", "id")(http.HandlerFunc(assertMathWorkerDerivedScope(t)))).ServeHTTP(rec, req)
	want := http.StatusForbidden
	if boundary.allowed {
		want = http.StatusNoContent
	}
	if rec.Code != want {
		t.Fatalf("scope expected %d, got %d %s", want, rec.Code, rec.Body.String())
	}
}

func assertMathWorkerDerivedScope(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserFromContext(r.Context())
		scope, _ := auth.AccessScopeFromContext(r.Context())
		if user.TenantID != runtimeTenantID || user.ID != "" || !scope.AllowsExam("math-exam") {
			t.Fatal("scope must derive school and exam from the persisted task")
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

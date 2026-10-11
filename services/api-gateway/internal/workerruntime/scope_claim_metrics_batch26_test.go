package workerruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

type batch26CapabilityStore struct {
	Store
	task  Task
	err   error
	calls int
	args  []string
	now   time.Time
}

type batch26RequestContextKey struct{}

func (s *batch26CapabilityStore) AuthorizeLease(_ context.Context, taskID, token, service, instance string, now time.Time) (Task, error) {
	s.calls++
	s.args = []string{taskID, token, service, instance}
	s.now = now
	return s.task, s.err
}

func TestBatch26TaskScopeKeepsCapabilityCheckOrder(t *testing.T) {
	task := batch26ScopedTask()
	worker := auth.User{ID: "worker-user", TenantID: auth.PlatformTenantID, Roles: []string{"page_processing_worker"}}
	human := auth.User{ID: "teacher-user", TenantID: "tenant-a", Roles: []string{"teacher"}}
	wrongWorker := worker
	wrongWorker.Roles = []string{"subjective_grading_worker"}
	for _, tc := range []taskScopeBoundaryCase{
		{name: "authentication first", wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated", wantMessage: "authentication required"},
		{name: "human bypass", user: &human, missing: TaskLeaseHeader, pathTaskID: "other-task", wantStatus: http.StatusNoContent},
		{name: "missing header before path", user: &worker, missing: TaskLeaseHeader, pathTaskID: "other-task", wantStatus: http.StatusForbidden, wantCode: "worker_scope_forbidden", wantMessage: "a current worker task capability is required"},
		{name: "path before lease", user: &worker, pathTaskID: "other-task", leaseError: ErrLeaseMismatch, wantStatus: http.StatusForbidden, wantCode: "worker_scope_forbidden", wantMessage: "worker task capability does not match the requested task"},
		{name: "lease before worker whitelist", user: &wrongWorker, leaseError: ErrLeaseMismatch, wantCalls: 1, wantStatus: http.StatusForbidden, wantCode: "worker_scope_forbidden", wantMessage: "worker task capability is invalid or expired"},
		{name: "authenticated queue whitelist", user: &wrongWorker, wantCalls: 1, wantStatus: http.StatusForbidden, wantCode: "worker_scope_forbidden", wantMessage: "authenticated worker service does not own this task queue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTaskScopeCapabilityBoundary(t, task, tc)
		})
	}
}

func TestBatch26TaskScopeRewritesOnlyCrossTenantIdentity(t *testing.T) {
	task := batch26ScopedTask()
	for _, tenantID := range []string{task.TenantID, auth.PlatformTenantID} {
		t.Run(tenantID, func(t *testing.T) {
			user := auth.User{ID: "worker-user", TenantID: tenantID, TenantCode: "worker-tenant", Roles: []string{"page_processing_worker"}, Permissions: []string{"files:read"}}
			store := &batch26CapabilityStore{task: task}
			req := taskRequest(task, "resource-1")
			req = req.WithContext(auth.WithUser(req.Context(), user))
			req = req.WithContext(context.WithValue(req.Context(), batch26RequestContextKey{}, "kept"))
			for header, value := range map[string]string{TaskIDHeader: task.ID, TaskLeaseHeader: task.LeaseToken, WorkerServiceHeader: task.WorkerService, WorkerInstanceIDHeader: task.WorkerInstanceID} {
				req.Header.Set(header, " "+value+" ")
			}
			req.Header.Set("X-EduGrade-Tenant-ID", "caller-selected-tenant")
			req.SetPathValue("taskId", " "+task.ID+" ")
			var gotUser auth.User
			var gotScope auth.AccessScope
			var gotTask Task
			var gotContextValue any
			rec := httptest.NewRecorder()
			started := time.Now().UTC()
			TaskScope(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotUser, _ = auth.UserFromContext(r.Context())
				gotScope, _ = auth.AccessScopeFromContext(r.Context())
				gotTask, _ = TaskCapabilityFromContext(r.Context())
				gotContextValue = r.Context().Value(batch26RequestContextKey{})
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(rec, req)
			wantUser := user
			wantUser.TenantID = task.TenantID
			if tenantID != task.TenantID {
				wantUser.ID, wantUser.TenantCode = "", ""
			}
			wantScope := auth.AccessScope{TenantID: task.TenantID, AssignedOnly: true, ExamIDs: []string{"exam-1"}, SubmissionIDs: []string{"submission-1"}}
			if rec.Code != http.StatusNoContent || !reflect.DeepEqual(gotUser, wantUser) || !reflect.DeepEqual(gotScope, wantScope) || !reflect.DeepEqual(gotTask, task) || gotContextValue != "kept" {
				t.Fatalf("status=%d user=%#v scope=%#v task=%#v", rec.Code, gotUser, gotScope, gotTask)
			}
			if store.calls != 1 || !reflect.DeepEqual(store.args, []string{task.ID, task.LeaseToken, task.WorkerService, task.WorkerInstanceID}) || store.now.Before(started) || store.now.After(time.Now().UTC()) || store.now.Location() != time.UTC {
				t.Fatalf("unexpected lease authorization: calls=%d args=%v now=%v", store.calls, store.args, store.now)
			}
		})
	}
}

func batch26ScopedTask() Task {
	return Task{ID: "task-1", TenantID: "tenant-a", QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", LeaseToken: "current-lease", Payload: map[string]any{"exam_id": "exam-1", "submission_id": "submission-1"}}
}

func TestBatch26PayloadGuardPreservesNestedExactMatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload map[string]any
		allowed bool
	}{
		{name: "trimmed case insensitive key", payload: map[string]any{" ANSWER_ID ": " answer-1 "}, allowed: true},
		{name: "nested map and array", payload: map[string]any{"records": []any{nil, map[string]any{"nested": map[string]any{"answer_id": "answer-1"}}}}, allowed: true},
		{name: "value remains case sensitive", payload: map[string]any{"answer_id": "ANSWER-1"}},
		{name: "key must match exactly", payload: map[string]any{"answer_id_extra": "answer-1"}},
		{name: "non string ID", payload: map[string]any{"answer_id": 1}},
		{name: "typed string slice is not payload array", payload: map[string]any{"answer_id": []string{"answer-1"}}},
		{name: "nil payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := batch26ScopedTask()
			task.Payload = tc.payload
			req := taskRequest(task, "answer-1")
			req.SetPathValue("id", " answer-1 ")
			req = req.WithContext(WithTaskCapability(req.Context(), task))
			rec := httptest.NewRecorder()
			RequireTaskPayloadValue("id", "answer_id")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(rec, req)
			want := http.StatusForbidden
			if tc.allowed {
				want = http.StatusNoContent
			}
			if rec.Code != want {
				t.Fatalf("status=%d want=%d: %s", rec.Code, want, rec.Body.String())
			}
		})
	}
}

func TestBatch26ClaimKeepsOrderScopeAndExhaustion(t *testing.T) {
	for _, across := range []bool{false, true} {
		name := "tenant only"
		if across {
			name = "across tenants"
		}
		t.Run(name, func(t *testing.T) {
			assertWorkerClaimOrderScopeAndExhaustion(t, across)
		})
	}
}

// 只构造候选排序和耗尽边界；现有测试已覆盖过期重领及并发只领取一次。
func batch26ClaimStore(now time.Time) *MemoryStore {
	store := NewMemoryStore()
	created := now.Add(-2 * time.Hour)
	for _, id := range []string{"exhausted", "expired", "a", "b", "later", "low-priority", "other-tenant", "other-queue", "future", "after-limit-exhausted"} {
		store.tasks[id] = Task{ID: id, TenantID: "tenant-a", QueueName: "ocr", Status: StatusQueued, Priority: 1, MaxAttempts: 3, CreatedAt: created, UpdatedAt: created, Revision: 1}
	}
	task := store.tasks["exhausted"]
	task.Priority, task.AttemptCount = -1, 4
	store.tasks[task.ID] = task
	task = store.tasks["expired"]
	past := now.Add(-time.Minute)
	task.Status, task.Priority, task.AttemptCount, task.MaxAttempts = StatusRunning, -1, 1, 1
	task.LeaseToken, task.LeasedBy, task.LeaseExpiresAt = "old-lease", "old-worker", &past
	task.Attempts = []Attempt{{Status: StatusRunning, AttemptNo: 1, StartedAt: created}}
	store.tasks[task.ID] = task
	task = store.tasks["later"]
	task.CreatedAt = created.Add(time.Minute)
	store.tasks[task.ID] = task
	task = store.tasks["low-priority"]
	task.Priority, task.CreatedAt = 2, created.Add(-time.Minute)
	store.tasks[task.ID] = task
	task = store.tasks["other-tenant"]
	task.TenantID, task.Priority = "tenant-b", 0
	store.tasks[task.ID] = task
	task = store.tasks["other-queue"]
	task.QueueName, task.Priority = "other-queue", -2
	store.tasks[task.ID] = task
	task = store.tasks["future"]
	future := now.Add(time.Hour)
	task.NotBefore, task.Priority = &future, -2
	store.tasks[task.ID] = task
	// 达到成功领取上限后必须停止，排在后面的耗尽任务也不能提前转入死信。
	task = store.tasks["after-limit-exhausted"]
	task.Priority, task.AttemptCount = 3, task.MaxAttempts
	store.tasks[task.ID] = task
	return store
}

func TestBatch26MetricsKeepsTaskAndAttemptCountingRules(t *testing.T) {
	store := NewMemoryStore()
	now := time.Now().UTC()
	recent, old := now.Add(-10*time.Minute), now.Add(-2*time.Hour)
	for index, status := range []string{StatusQueued, StatusLeased, StatusRunning, StatusSucceeded, StatusFailed, StatusDeadLetter, StatusCancelled} {
		task := Task{ID: status, TenantID: "tenant-a", QueueName: "b", Status: status, DurationMS: (index + 1) * 10, AttemptCount: 1, MaxAttempts: 3, UpdatedAt: recent}
		store.tasks[task.ID] = task
	}
	task := store.tasks[StatusQueued]
	// retry 统计的是失败尝试，不要求任务仍处于 failed；空时间、旧失败和成功尝试不计。
	task.Attempts = []Attempt{{Status: StatusFailed, CompletedAt: &recent}, {Status: StatusFailed, CompletedAt: &recent}, {Status: StatusFailed}, {Status: StatusFailed, CompletedAt: &old}, {Status: StatusSucceeded, CompletedAt: &recent}}
	store.tasks[task.ID] = task
	task = store.tasks[StatusFailed]
	task.AttemptCount = task.MaxAttempts
	task.Attempts = []Attempt{{Status: StatusFailed, CompletedAt: &recent}}
	store.tasks[task.ID] = task
	task = store.tasks[StatusDeadLetter]
	task.UpdatedAt = old
	store.tasks[task.ID] = task
	store.tasks["nonpositive-duration"] = Task{ID: "nonpositive-duration", TenantID: "tenant-a", QueueName: "a", Status: StatusQueued, DurationMS: -100}
	store.tasks["other-tenant"] = Task{ID: "other-tenant", TenantID: "tenant-b", QueueName: "c", Status: StatusFailed, DurationMS: 999, UpdatedAt: recent, AttemptCount: 1, MaxAttempts: 3, Attempts: []Attempt{{Status: StatusFailed, CompletedAt: &recent}}}
	store.heartbeats["older"] = WorkerHeartbeat{TenantID: "tenant-a", WorkerInstanceID: "older", LastSeenAt: old}
	store.heartbeats["newer"] = WorkerHeartbeat{TenantID: "tenant-a", WorkerInstanceID: "newer", LastSeenAt: now}
	store.heartbeats["middle"] = WorkerHeartbeat{TenantID: "tenant-b", WorkerInstanceID: "middle", LastSeenAt: recent}
	for _, across := range []bool{false, true} {
		name := "tenant only"
		if across {
			name = "across tenants"
		}
		t.Run(name, func(t *testing.T) {
			wantQueues := []QueueMetrics{{QueueName: "a", Queued: 1}, {QueueName: "b", Queued: 1, Leased: 1, Running: 1, Succeeded: 1, Failed: 1, DeadLetter: 1, Cancelled: 1, FailedLastHour: 1, RetryLastHour: 2, P95DurationMS: 70}}
			wantWorkers := []string{"newer", "older"}
			var got Metrics
			var err error
			if across {
				wantQueues = append(wantQueues, QueueMetrics{QueueName: "c", Failed: 1, FailedLastHour: 1, RetryLastHour: 1, P95DurationMS: 999})
				wantWorkers = []string{"newer", "middle", "older"}
				got, err = store.MetricsAcrossTenants(context.Background(), auth.PlatformTenantID)
			} else {
				got, err = store.Metrics(context.Background(), "tenant-a")
			}
			workerIDs := make([]string, 0, len(got.Workers))
			for _, worker := range got.Workers {
				workerIDs = append(workerIDs, worker.WorkerInstanceID)
			}
			if err != nil || !reflect.DeepEqual(got.Queues, wantQueues) || !reflect.DeepEqual(workerIDs, wantWorkers) {
				t.Fatalf("metrics=%#v workers=%v want queues=%#v workers=%v err=%v", got.Queues, workerIDs, wantQueues, wantWorkers, err)
			}
		})
	}
}

type taskScopeBoundaryCase struct {
	name        string
	user        *auth.User
	missing     string
	pathTaskID  string
	leaseError  error
	wantCalls   int
	wantStatus  int
	wantCode    string
	wantMessage string
}

func assertTaskScopeCapabilityBoundary(t *testing.T, task Task, tc taskScopeBoundaryCase) {
	t.Helper()
	store := &batch26CapabilityStore{task: task, err: tc.leaseError}
	req := taskRequest(task, "resource-1").WithContext(context.Background())
	if tc.user != nil {
		req = req.WithContext(auth.WithUser(req.Context(), *tc.user))
	}
	req.Header.Del(tc.missing)
	req.SetPathValue("taskId", tc.pathTaskID)
	rec := httptest.NewRecorder()
	TaskScope(store)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(rec, req)
	if rec.Code != tc.wantStatus || store.calls != tc.wantCalls {
		t.Fatalf("status=%d lease calls=%d, want %d/%d: %s", rec.Code, store.calls, tc.wantStatus, tc.wantCalls, rec.Body.String())
	}
	if tc.wantCode != "" {
		var body httpx.ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != tc.wantCode || body.Error.Message != tc.wantMessage || rec.Header().Get(httpx.ErrorCodeHeader) != tc.wantCode {
			t.Fatalf("unexpected public error: %#v headers=%v", body.Error, rec.Header())
		}
	}
}

func assertWorkerClaimOrderScopeAndExhaustion(t *testing.T, across bool) {
	t.Helper()
	now := time.Now().UTC()
	store := batch26ClaimStore(now)
	input := ClaimInput{QueueName: " ocr ", WorkerService: " ocr-worker ", WorkerInstanceID: " worker-1 ", Limit: 3, LeaseSeconds: 300}
	var got []Task
	var err error
	heartbeatTenant := "tenant-a"
	wantIDs := []string{"a", "b", "later"}
	if across {
		heartbeatTenant = auth.PlatformTenantID
		wantIDs = []string{"other-tenant", "a", "b"}
		got, err = store.ClaimAcrossTenants(context.Background(), heartbeatTenant, input)
	} else {
		got, err = store.Claim(context.Background(), heartbeatTenant, input)
	}
	if err != nil {
		t.Fatal(err)
	}
	ids := assertClaimedTasksHaveCurrentLease(t, got, now)
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("claim IDs=%v want=%v", ids, wantIDs)
	}
	for _, id := range []string{"exhausted", "expired"} {
		task, err := store.Get(context.Background(), "tenant-a", id)
		if err != nil || task.Status != StatusDeadLetter || task.AttemptCount != task.MaxAttempts || task.CompletedAt == nil || task.Revision != 2 || task.LeaseToken != "" || task.LeaseExpiresAt != nil || task.LeasedBy != "" {
			t.Fatalf("exhausted task %s: %#v %v", id, task, err)
		}
	}
	expired := store.tasks["expired"]
	if expired.ErrorCode != "lease_expired" || expired.Attempts[0].Status != "lease_expired" || expired.Attempts[0].CompletedAt == nil || store.tasks["exhausted"].ErrorCode != "max_attempts_exhausted" || store.tasks["exhausted"].MaxAttempts != 4 {
		t.Fatalf("lost exhausted history: expired=%#v exhausted=%#v", expired, store.tasks["exhausted"])
	}
	for _, id := range []string{"future", "other-queue", "low-priority", "after-limit-exhausted"} {
		if store.tasks[id].Status != StatusQueued {
			t.Fatalf("unexpectedly claimed %s", id)
		}
	}
	metrics, _ := store.MetricsAcrossTenants(context.Background(), auth.PlatformTenantID)
	if len(metrics.Workers) != 1 || metrics.Workers[0].TenantID != heartbeatTenant || metrics.Workers[0].Metadata["state"] != "polling" {
		t.Fatalf("unexpected polling heartbeat: %#v", metrics.Workers)
	}
}

func assertClaimedTasksHaveCurrentLease(t *testing.T, got []Task, now time.Time) []string {
	t.Helper()
	ids := make([]string, 0, len(got))
	for _, task := range got {
		ids = append(ids, task.ID)
		if task.Status != StatusLeased || task.AttemptCount != 1 || task.Revision != 2 || task.LeaseToken == "" || task.LeaseExpiresAt == nil || !task.LeaseExpiresAt.After(now) || task.WorkerService != "ocr-worker" || task.WorkerInstanceID != "worker-1" || len(task.Attempts) != 1 {
			t.Fatalf("unexpected claimed task: %#v", task)
		}
	}
	return ids
}

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

func subjectiveClaimRequest(user auth.User, queue, service string) *http.Request {
	body, _ := json.Marshal(map[string]any{"queue_name": queue, "worker_service": service,
		"worker_instance_id": "platform-subjective-1", "limit": 1, "lease_seconds": 300})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/worker/tasks/claim", bytes.NewReader(body))
	return req.WithContext(auth.WithUser(req.Context(), user))
}

func createSchoolSubjectiveTask(t *testing.T, store *workerruntime.MemoryStore) workerruntime.Task {
	t.Helper()
	task, err := store.CreateTask(t.Context(), runtimeTenantID, "school-actor", workerruntime.CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: "school-run-17",
		Payload:              map[string]any{"exam_id": "school-exam", "submission_id": "school-submission"},
		PayloadSchemaVersion: "v1", IdempotencyKey: "school-run-17", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestPlatformSubjectiveWorkerClaimsSchoolTaskAndKeepsExecuteResultLeaseScope(t *testing.T) {
	store := workerruntime.NewMemoryStore()
	task := createSchoolSubjectiveTask(t, store)
	audit := auth.NewMemoryStore()
	handler := workerruntime.NewHandler(store, audit)
	worker := auth.User{ID: "platform-subjective-user", TenantID: auth.PlatformTenantID, TenantCode: "platform", Roles: []string{"subjective_grading_worker"}}
	rec := httptest.NewRecorder()
	handler.Claim(rec, subjectiveClaimRequest(worker, "subjective-grading", "subjective-grading-worker"))
	var response struct {
		Tasks []workerruntime.Task `json:"tasks"`
	}
	if rec.Code != http.StatusOK || json.NewDecoder(rec.Body).Decode(&response) != nil || len(response.Tasks) != 1 || response.Tasks[0].ID != task.ID {
		t.Fatalf("platform subjective claim must receive the school task: %d %s", rec.Code, rec.Body.String())
	}
	claimed := response.Tasks[0]
	if audits := audit.Audits(); len(audits) != 1 || audits[0].TenantID != runtimeTenantID || audits[0].ActorID != "" {
		t.Fatalf("cross-tenant claim audit must not reuse the platform actor: %#v", audits)
	}

	// 使用与生产 execute/result 相同的租约及来源中间件；请求头不能选择租户或换到其他评分 run。
	for _, action := range []string{"execute", "result"} {
		for _, boundary := range []string{"valid", "wrong run", "wrong lease"} {
			t.Run(action+"/"+boundary, func(t *testing.T) {
				runID, lease := task.SourceID, claimed.LeaseToken
				if boundary == "wrong run" {
					runID = "other-school-run"
				}
				if boundary == "wrong lease" {
					lease = "forged-lease"
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/subjective-grading/runs/"+runID+"/"+action, nil)
				req.SetPathValue("runId", runID)
				req.Header.Set(workerruntime.TaskIDHeader, task.ID)
				req.Header.Set(workerruntime.TaskLeaseHeader, lease)
				req.Header.Set(workerruntime.WorkerServiceHeader, "subjective-grading-worker")
				req.Header.Set(workerruntime.WorkerInstanceIDHeader, "platform-subjective-1")
				req.Header.Set("X-EduGrade-Tenant-ID", "caller-selected-tenant")
				req = req.WithContext(auth.WithUser(req.Context(), worker))
				target := workerruntime.RequireTaskSource("subjective_grading_run", "runId")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					user, _ := auth.UserFromContext(r.Context())
					scope, _ := auth.AccessScopeFromContext(r.Context())
					if user.TenantID != runtimeTenantID || user.ID != "" || user.TenantCode != "" || scope.TenantID != runtimeTenantID ||
						!scope.AssignedOnly || scope.TenantWide || !scope.AllowsExam("school-exam") || scope.AllowsExam("other-exam") {
						t.Fatalf("execute/result must keep the leased school scope: user=%#v scope=%#v", user, scope)
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				scoped := workerruntime.TaskScope(store)(target)
				if action == "result" {
					scoped = workerruntime.TaskCompletionScope(store)(target)
				}
				rec := httptest.NewRecorder()
				scoped.ServeHTTP(rec, req)
				want := http.StatusForbidden
				if boundary == "valid" {
					want = http.StatusNoContent
				}
				if rec.Code != want {
					t.Fatalf("status=%d, want %d: %s", rec.Code, want, rec.Body.String())
				}
			})
		}
	}
}

func TestSubjectiveCrossTenantClaimDoesNotExpandOtherQueuesOrSchoolUsers(t *testing.T) {
	for _, tc := range []struct {
		name, tenant, role, queue, service string
		wantStatus                         int
	}{
		{"platform subjective cannot OCR", auth.PlatformTenantID, "subjective_grading_worker", "ocr", "ocr-worker", http.StatusForbidden},
		{"platform subjective cannot spoof page service", auth.PlatformTenantID, "subjective_grading_worker", "subjective-grading", "page-processing", http.StatusForbidden},
		{"school subjective stays in own tenant", "other-school", "subjective_grading_worker", "subjective-grading", "subjective-grading-worker", http.StatusOK},
		{"school human stays in own tenant", "other-school", "school_admin", "subjective-grading", "subjective-grading-worker", http.StatusOK},
		{"platform admin receives no worker privilege", auth.PlatformTenantID, "platform_admin", "subjective-grading", "subjective-grading-worker", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := workerruntime.NewMemoryStore()
			task := createSchoolSubjectiveTask(t, store)
			handler := workerruntime.NewHandler(store, auth.NewMemoryStore())
			rec := httptest.NewRecorder()
			handler.Claim(rec, subjectiveClaimRequest(auth.User{ID: "user", TenantID: tc.tenant, Roles: []string{tc.role}}, tc.queue, tc.service))
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			current, err := store.Get(t.Context(), runtimeTenantID, task.ID)
			if err != nil || current.Status != workerruntime.StatusQueued || current.AttemptCount != 0 {
				t.Fatalf("unauthorized cross-tenant task must remain untouched: %#v %v", current, err)
			}
		})
	}
}

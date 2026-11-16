package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func subjectiveTestRequest(t *testing.T, method string, user auth.User, body any, path map[string]string) *http.Request {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, "/", bytes.NewReader(raw))
	for name, value := range path {
		request.SetPathValue(name, value)
	}
	return request.WithContext(auth.WithUser(request.Context(), user))
}

type failingSubjectiveAdapter struct {
	err   error
	calls int
}

func (*failingSubjectiveAdapter) Name() string { return "synthetic_failure" }

func (a *failingSubjectiveAdapter) Grade(context.Context, AdapterInput) (AdapterOutput, error) {
	a.calls++
	return AdapterOutput{}, a.err
}

func TestFailWorkerHTTPPreservesTaskAndRunOnRejectedCallback(t *testing.T) {
	t.Run("unavailable runtime", func(t *testing.T) {
		response := httptest.NewRecorder()
		request := subjectiveTestRequest(t, http.MethodPost, auth.User{}, nil, nil)
		NewHandler(NewMemoryStore(), NewMockLLMAdapter(), nil).FailWorker(response, request)
		if response.Code != http.StatusServiceUnavailable || response.Header().Get(httpx.ErrorCodeHeader) != "subjective_worker_unavailable" {
			t.Fatalf("unexpected missing-runtime response: %d %s", response.Code, response.Body)
		}
	})

	for _, tc := range []struct {
		name     string
		reject   func(*WorkerFailureInput)
		rejected string
	}{
		{"different task", func(input *WorkerFailureInput) { input.TaskID = "another-task" }, "subjective_task_mismatch"},
		{"stale lease", func(input *WorkerFailureInput) { input.LeaseToken = "previous-lease" }, "subjective_task_failure_rejected"},
		{"current lease", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
			handler := NewHandler(fixture.store, NewMockLLMAdapter(), nil).WithWorkerRuntimeStore(fixture.runtime)
			input := WorkerFailureInput{TaskID: fixture.task.ID, LeaseToken: fixture.task.LeaseToken, ErrorCode: "provider_timeout", Retryable: true}
			if tc.reject != nil {
				tc.reject(&input)
			}
			request := subjectiveTestRequest(t, http.MethodPost, auth.User{ID: "worker-1", TenantID: workerServiceTenant}, input, map[string]string{"runId": fixture.run.ID})
			response := httptest.NewRecorder()
			handler.FailWorker(response, request)
			task, taskErr := fixture.runtime.Get(t.Context(), workerServiceTenant, fixture.task.ID)
			run, runErr := fixture.store.GetRun(t.Context(), workerServiceTenant, fixture.run.ID)
			if taskErr != nil || runErr != nil {
				t.Fatalf("read task and run: %v, %v", taskErr, runErr)
			}
			if tc.rejected != "" {
				if response.Code != http.StatusConflict || response.Header().Get(httpx.ErrorCodeHeader) != tc.rejected {
					t.Fatalf("unexpected conflict response: %d %s", response.Code, response.Body)
				}
				if task.Status != fixture.task.Status || task.LeaseToken != fixture.task.LeaseToken || run.Status != fixture.run.Status || run.ErrorCode != fixture.run.ErrorCode {
					t.Fatal("rejected callback changed the active attempt or its run")
				}
				return
			}
			if response.Code != http.StatusOK || task.Status != workerruntime.StatusQueued || task.NotBefore == nil || run.Status != RunQueued ||
				task.ErrorCode != input.ErrorCode || run.ErrorCode != input.ErrorCode || run.AttemptCount != task.AttemptCount {
				t.Fatalf("accepted retryable failure did not requeue task and run together: %d %s", response.Code, response.Body)
			}
		})
	}
}

func TestExecuteWorkerHTTPRecordsProviderFailureBeforeReply(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		code       string
		taskStatus string
		runStatus  string
		retryable  bool
	}{
		{"unconfigured", &GradingAgentError{Code: "adapter_not_configured"}, http.StatusServiceUnavailable, "ai_service_not_configured", workerruntime.StatusFailed, RunFailed, false},
		{"temporarily unavailable", &GradingAgentError{Code: "provider_unavailable", Retryable: true}, http.StatusServiceUnavailable, "ai_service_unavailable", workerruntime.StatusQueued, RunQueued, true},
		{"truncated", &GradingAgentError{Code: "synthetic-private-provider-code", Retryable: true, ExecutionDiagnostics: truncatedDiagnostics()}, http.StatusBadGateway, "ai_output_incomplete", workerruntime.StatusFailed, RunFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newWorkerServiceFixture(t, CreateRunInput{}, "")
			adapter := &failingSubjectiveAdapter{err: tc.err}
			handler := NewHandler(fixture.store, adapter, nil).WithWorkerRuntimeStore(fixture.runtime)
			request := subjectiveTestRequest(t, http.MethodPost, auth.User{ID: "worker-1", TenantID: workerServiceTenant},
				map[string]string{"task_id": fixture.task.ID, "lease_token": fixture.task.LeaseToken}, map[string]string{"runId": fixture.run.ID})
			response := httptest.NewRecorder()
			handler.ExecuteWorker(response, request)
			var payload struct {
				TaskFailureRecorded bool                  `json:"task_failure_recorded"`
				Retryable           bool                  `json:"retryable"`
				Diagnostics         *ExecutionDiagnostics `json:"execution_diagnostics"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if response.Code != tc.status || response.Header().Get(httpx.ErrorCodeHeader) != tc.code ||
				!payload.TaskFailureRecorded || payload.Retryable != tc.retryable || adapter.calls != 1 {
				t.Fatalf("unexpected provider failure response: %d %s", response.Code, response.Body)
			}
			if tc.code == "ai_output_incomplete" && (payload.Diagnostics == nil || payload.Diagnostics.FailureKind != "output_truncated" ||
				strings.Contains(response.Body.String(), "synthetic-private-provider-code")) {
				t.Fatalf("incomplete result lost public diagnostics or exposed the provider code: %s", response.Body)
			}
			task, taskErr := fixture.runtime.Get(t.Context(), workerServiceTenant, fixture.task.ID)
			run, runErr := fixture.store.GetRun(t.Context(), workerServiceTenant, fixture.run.ID)
			if taskErr != nil || runErr != nil || task.Status != tc.taskStatus || run.Status != tc.runStatus ||
				task.ErrorCode != tc.code || run.ErrorCode != tc.code {
				t.Fatalf("provider failure was not persisted before reply: task=%s/%s run=%s/%s errors=%v/%v",
					task.Status, task.ErrorCode, run.Status, run.ErrorCode, taskErr, runErr)
			}
			if _, err := fixture.store.GetGradeByAdapterRequestID(t.Context(), workerServiceTenant, fixture.run.RequestID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("provider failure left a grade: %v", err)
			}
		})
	}
}

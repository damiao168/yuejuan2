package capture

import (
	"context"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type templateCallbackStore struct {
	*MemoryStore
	resultInput TemplateMatchResultInput
	failInput   TemplateMatchFailureInput
	resultErr   error
	failErr     error
}

func (s *templateCallbackStore) SubmitTemplateMatchResultCommand(_ context.Context, tenantID, runID string, input TemplateMatchResultInput) (TemplateMatchRun, []RegistrationRun, error) {
	if tenantID != "tenant" || runID != "run" {
		return TemplateMatchRun{}, nil, ErrNotFound
	}
	s.resultInput = input
	if s.resultErr != nil {
		return TemplateMatchRun{}, nil, s.resultErr
	}
	return TemplateMatchRun{ID: runID, Decision: input.Decision, SelectedTemplateID: input.SelectedTemplateID}, []RegistrationRun{{ID: "registration"}}, nil
}

func (s *templateCallbackStore) SubmitTemplateMatchFailureCommand(_ context.Context, tenantID, runID string, input TemplateMatchFailureInput) (TemplateMatchRun, string, error) {
	if tenantID != "tenant" || runID != "run" {
		return TemplateMatchRun{}, "", ErrNotFound
	}
	s.failInput = input
	if s.failErr != nil {
		return TemplateMatchRun{}, "", s.failErr
	}
	return TemplateMatchRun{ID: runID, ProcessingStatus: "retryable_error", ErrorCode: input.ErrorCode}, workerruntime.StatusQueued, nil
}

func TestTemplateMatchHTTPDelegatesResultAndFailureToTransactionalStore(t *testing.T) {
	runtime := workerruntime.NewMemoryStore()
	activeTask := remediationMemoryRuntimeTask(t, runtime, "page_template_match_run", "run")
	unrelatedTask := remediationMemoryRuntimeTask(t, runtime, "page_template_match_run", "other-run")
	store := &templateCallbackStore{MemoryStore: NewMemoryStore()}
	store.templateMatches["run"] = TemplateMatchRun{ID: "run", RuntimeTaskID: activeTask.ID, ProcessingStatus: "processing"}
	audit := &collectionAuditRecorder{}
	handler := NewHandler(store, nil, nil, runtime, audit)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/internal/page-template-match-runs/{runId}/result", handler.CompleteTemplateMatch)
	mux.HandleFunc("POST /api/v1/internal/page-template-match-runs/{runId}/fail", handler.FailTemplateMatch)
	const path = "/api/v1/internal/page-template-match-runs/run"
	result := TemplateMatchResultInput{TaskID: activeTask.ID, LeaseToken: activeTask.LeaseToken, ResultVersion: "v1", Decision: "matched", SelectedTemplateID: "template", Score: .95}
	wrongRun := result
	wrongRun.TaskID, wrongRun.LeaseToken = unrelatedTask.ID, unrelatedTask.LeaseToken
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/result", captureWorkflowBody(t, wrongRun), nil, nil), http.StatusConflict, "template_match_task_mismatch")
	if store.resultInput.TaskID != "" {
		t.Fatalf("callback for different run reached the command: %+v", store.resultInput)
	}

	// 生产命令在事务中重新检查租约与页面版本。HTTP 层应返回冲突并且不追加成功审计。
	store.resultErr = workerruntime.ErrLeaseExpired
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/result", captureWorkflowBody(t, result), nil, nil), http.StatusConflict, "capture_worker_lease_invalid")
	store.resultErr = ErrConflict
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/result", captureWorkflowBody(t, result), nil, nil), http.StatusConflict, "capture_revision_conflict")
	failure := TemplateMatchFailureInput{TaskID: activeTask.ID, LeaseToken: activeTask.LeaseToken, ErrorCode: "template_unreadable", Retryable: true}
	store.failErr = workerruntime.ErrConflict
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/fail", captureWorkflowBody(t, failure), nil, nil), http.StatusConflict, "capture_worker_result_conflict")
	if len(audit.events) != 0 {
		t.Fatalf("rejected callbacks wrote success audit: %+v", audit.events)
	}

	// 事务替身只返回回执。真实内存租约仍保持原样，用来识别处理器是否又执行了独立的 Complete 或 Fail。
	store.resultErr, store.failErr = nil, nil
	type resultResponse struct {
		Run              TemplateMatchRun
		RegistrationRuns []RegistrationRun `json:"registration_runs"`
	}
	completed := captureWorkflowJSON[resultResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/result", captureWorkflowBody(t, result), nil, nil), http.StatusOK)
	if completed.Run.SelectedTemplateID != "template" || len(completed.RegistrationRuns) != 1 || completed.RegistrationRuns[0].ID != "registration" || store.resultInput.LeaseToken != activeTask.LeaseToken {
		t.Fatalf("result lost the selected template, registration or lease: %+v input=%+v", completed, store.resultInput)
	}
	type failureResponse struct {
		Run        TemplateMatchRun
		TaskStatus string `json:"task_status"`
	}
	retried := captureWorkflowJSON[failureResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/fail", captureWorkflowBody(t, failure), nil, nil), http.StatusOK)
	if retried.Run.ProcessingStatus != "retryable_error" || retried.TaskStatus != workerruntime.StatusQueued || store.failInput.ErrorCode != "template_unreadable" || !store.failInput.Retryable {
		t.Fatalf("failure did not preserve the queued retry or error: %+v input=%+v", retried, store.failInput)
	}
	current, err := runtime.Get(t.Context(), "tenant", activeTask.ID)
	if err != nil || current.Status != workerruntime.StatusLeased || current.Revision != activeTask.Revision {
		t.Fatalf("handler wrote worker state outside the transaction: status=%s revision=%d err=%v", current.Status, current.Revision, err)
	}
	if len(audit.events) != 2 || audit.events[0].Action != "page.template_matched" || audit.events[1].Action != "page.template_match_failed" {
		t.Fatalf("expected success audit for the two commands: %+v", audit.events)
	}
}

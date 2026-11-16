package capture

import (
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestCapturePageReviewHTTPRejectsStaleEditsAndReturnsRestoredPageForReview(t *testing.T) {
	store := NewMemoryStore()
	store.batches["batch"] = Batch{ID: "batch", TenantID: "tenant", Status: "processing"}
	store.pages["page"] = Page{ID: "page", TenantID: "tenant", CaptureBatchID: "batch", SubmissionID: "submission", Revision: 1, Status: "ready", AssignedPageNo: 1, SequenceNo: 1}
	handler := NewHandler(store, nil, nil, nil, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH /api/v1/capture-pages/{id}", handler.UpdatePage)
	mux.HandleFunc("POST /api/v1/capture-pages/{id}/page-match/confirm", handler.ConfirmPageMatch)
	mux.HandleFunc("POST /api/v1/capture-pages/{id}/delete", handler.DeletePage)
	mux.HandleFunc("POST /api/v1/capture-pages/{id}/restore", handler.RestorePage)
	mux.HandleFunc("GET /api/v1/submissions/{id}/processing-summary", handler.GetProcessingSummary)
	const path = "/api/v1/capture-pages/page"
	const summaryPath = "/api/v1/submissions/submission/processing-summary"

	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "PATCH", path, `{"revision":1,"rotation_degrees":45}`, nil, nil), http.StatusBadRequest, "capture_invalid_input")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "PATCH", path, `{"revision":1,"rotation_degrees":90,"sequence_no":0}`, nil, nil), http.StatusBadRequest, "capture_invalid_input")
	type pageResponse struct{ Page Page }
	page := captureWorkflowJSON[pageResponse](t, captureWorkflowRequest(t, mux, "PATCH", path, `{"revision":1,"rotation_degrees":90,"sequence_no":2}`, nil, nil), http.StatusOK).Page
	if page.Revision != 2 || page.RotationDegrees != 90 || page.SequenceNo != 2 || page.Status != "needs_review" {
		t.Fatalf("page correction did not invalidate previous review: %+v", page)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/page-match/confirm", `{"revision":1,"page_no":3,"reason":"manual correction"}`, nil, nil), http.StatusConflict, "capture_revision_conflict")
	page = captureWorkflowJSON[pageResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/page-match/confirm", `{"revision":2,"page_no":3,"reason":"manual correction"}`, nil, nil), http.StatusOK).Page
	if page.Revision != 3 || page.AssignedPageNo != 3 || page.ManualOverride["reason"] != "manual correction" {
		t.Fatalf("confirmed page is incorrect: %+v", page)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/delete", `{"revision":2,"reason":"remove wrong page"}`, nil, nil), http.StatusConflict, "capture_revision_conflict")
	page = captureWorkflowJSON[pageResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/delete", `{"revision":3,"reason":"remove wrong page"}`, nil, nil), http.StatusOK).Page
	if page.Revision != 4 || page.Status != "deleted" {
		t.Fatalf("page was not removed: %+v", page)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "GET", summaryPath, "", nil, nil), http.StatusNotFound, "capture_not_found")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/restore", `{"revision":3,"reason":"restore reviewed scan"}`, nil, nil), http.StatusConflict, "capture_revision_conflict")
	page = captureWorkflowJSON[pageResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/restore", `{"revision":4,"reason":"restore reviewed scan"}`, nil, nil), http.StatusOK).Page
	if page.Revision != 5 || page.Status != "needs_review" || page.AssignedPageNo != 3 {
		t.Fatalf("restored page lost its correction or skipped review: %+v", page)
	}
	summary := captureWorkflowJSON[ProcessingSummary](t, captureWorkflowRequest(t, mux, "GET", summaryPath, "", nil, nil), http.StatusOK)
	if summary.CanComplete || summary.BlockedPages != 1 || len(summary.Blockers) != 1 || summary.Blockers[0].Action != "review_quality" {
		t.Fatalf("restored page must block completion until reviewed: %+v", summary)
	}
}

func TestCaptureRegistrationHTTPRecoversFailedRunAndRejectsUnverifiedCrop(t *testing.T) {
	store, assets, runtime := NewMemoryStore(), files.NewMemoryStore(), workerruntime.NewMemoryStore()
	task := remediationMemoryRuntimeTask(t, runtime, "page_registration_run", "run")
	source := createCaptureWorkflowAsset(t, assets, "exam", "source")
	registered := createCaptureWorkflowAsset(t, assets, "exam", "registered")
	crop := createCaptureWorkflowAsset(t, assets, "exam", "crop")
	store.batches["batch"] = Batch{ID: "batch", TenantID: "tenant", Status: "processing"}
	store.pages["page"] = Page{ID: "page", TenantID: "tenant", CaptureBatchID: "batch", SubmissionID: "submission", SubmissionPageID: "page-result", Status: "normalized"}
	store.registrations["run"] = RegistrationRun{ID: "run", CapturePageID: "page", SubmissionPageID: "page-result", RuntimeTaskID: task.ID, SourceFileAssetID: source.ID, ProcessingStatus: "processing"}
	handler := NewHandler(store, assets, nil, runtime, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/internal/page-registration-runs/{runId}/fail", handler.FailRegistration)
	mux.HandleFunc("POST /api/v1/internal/page-registration-runs/{runId}/result", handler.CompleteRegistration)
	mux.HandleFunc("POST /api/v1/page-registration-runs/{id}/retry", handler.RetryRegistration)
	mux.HandleFunc("GET /api/v1/submissions/{id}/processing-summary", handler.GetProcessingSummary)
	mux.HandleFunc("GET /api/v1/submission-pages/{id}/registration-runs", handler.ListRegistrationRuns)
	const internal = "/api/v1/internal/page-registration-runs/run"
	const retry = "/api/v1/page-registration-runs/run/retry"
	type registrationResponse struct {
		Run        RegistrationRun
		TaskStatus string `json:"task_status"`
	}
	failureInput := RegistrationFailureInput{TaskID: task.ID, LeaseToken: task.LeaseToken, ErrorCode: "alignment_failed"}
	failure := captureWorkflowJSON[registrationResponse](t, captureWorkflowRequest(t, mux, "POST", internal+"/fail", captureWorkflowBody(t, failureInput), nil, nil), http.StatusOK)
	if failure.Run.ProcessingStatus != "terminal_error" || failure.TaskStatus != workerruntime.StatusFailed {
		t.Fatalf("registration failure was not projected: %+v", failure)
	}
	summary := captureWorkflowJSON[ProcessingSummary](t, captureWorkflowRequest(t, mux, "GET", "/api/v1/submissions/submission/processing-summary", "", nil, nil), http.StatusOK)
	if summary.CanComplete || summary.BlockedPages != 1 || len(summary.Blockers) != 1 || summary.Blockers[0].Action != "retry_registration" {
		t.Fatalf("failed run did not expose a retry blocker: %+v", summary)
	}
	queued := captureWorkflowJSON[registrationResponse](t, captureWorkflowRequest(t, mux, "POST", retry, "", nil, nil), http.StatusAccepted).Run
	if queued.ProcessingStatus != "processing" || queued.ErrorCode != "" || queued.RuntimeTaskID != task.ID {
		t.Fatalf("retry did not clear the failure on the original run: %+v", queued)
	}
	claimed, err := runtime.Claim(t.Context(), "tenant", workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "retry", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].ID != task.ID || claimed[0].LeaseToken == task.LeaseToken {
		t.Fatalf("failed run could not be claimed with a new lease: %v", err)
	}
	valid := RegistrationResultInput{TaskID: task.ID, LeaseToken: claimed[0].LeaseToken, ResultVersion: "v1", Confidence: .99, Method: "fixture", RegisteredFileAssetID: registered.ID, RegisteredSHA256: registered.HashSHA256, Segments: []SegmentCropInput{{QuestionID: "question", FileAssetID: crop.ID, SHA256: crop.HashSHA256}}}
	invalid := valid
	invalid.Segments = []SegmentCropInput{{QuestionID: "question", FileAssetID: crop.ID, SHA256: "wrong-crop-hash"}}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", internal+"/result", captureWorkflowBody(t, invalid), nil, nil), http.StatusBadRequest, "segment_crop_asset_invalid")
	invalid = valid
	invalid.LeaseToken = task.LeaseToken
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", internal+"/result", captureWorkflowBody(t, invalid), nil, nil), http.StatusConflict, "capture_worker_lease_invalid")
	completed := captureWorkflowJSON[registrationResponse](t, captureWorkflowRequest(t, mux, "POST", internal+"/result", captureWorkflowBody(t, valid), nil, nil), http.StatusOK).Run
	if completed.ProcessingStatus != "completed" || completed.RegisteredFileAssetID != registered.ID {
		t.Fatalf("corrected registration result not retained: %+v", completed)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", retry, "", nil, nil), http.StatusConflict, "capture_worker_result_rejected")
	listed := captureWorkflowJSON[map[string][]RegistrationRun](t, captureWorkflowRequest(t, mux, "GET", "/api/v1/submission-pages/page-result/registration-runs", "", nil, nil), http.StatusOK)["runs"]
	if len(listed) != 1 || listed[0].ID != completed.ID || listed[0].ProcessingStatus != "completed" {
		t.Fatalf("retry replaced or duplicated registration history: %+v", listed)
	}
}

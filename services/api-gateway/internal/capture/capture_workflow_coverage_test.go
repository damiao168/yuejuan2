package capture

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type captureWorkflowExam struct {
	exam.Store
	status string
}

func (s *captureWorkflowExam) GetExam(_ context.Context, scope auth.AccessScope, id string) (exam.Exam, error) {
	if scope.TenantID != "tenant" || id != "exam" {
		return exam.Exam{}, exam.ErrNotFound
	}
	return exam.Exam{ID: id, TenantID: scope.TenantID, Status: s.status}, nil
}

func captureWorkflowRequest(t *testing.T, mux *http.ServeMux, method, path, body string, scope *auth.AccessScope, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	ctx := auth.WithUser(t.Context(), auth.User{ID: "operator", TenantID: "tenant"})
	if scope != nil {
		ctx = auth.WithAccessScope(ctx, *scope)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request.WithContext(ctx))
	return response
}

func captureWorkflowJSON[T any](t *testing.T, response *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if response.Code != status {
		t.Fatalf("got HTTP %d, want %d: %s", response.Code, status, response.Body.String())
	}
	var out T
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func captureWorkflowBody(t *testing.T, input any) string {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func requireCaptureWorkflowError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	type errorResponse struct {
		Error struct{ Code string } `json:"error"`
	}
	if got := captureWorkflowJSON[errorResponse](t, response, status).Error.Code; got != code {
		t.Fatalf("got error %q, want %q: %s", got, code, response.Body.String())
	}
}

func createCaptureWorkflowAsset(t *testing.T, store *files.MemoryStore, examID, hash string) files.FileAsset {
	t.Helper()
	asset, err := store.Create(t.Context(), files.CreateAssetInput{TenantID: "tenant", ExamID: examID, HashSHA256: hash, SizeBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	return asset
}

func TestCaptureBatchHTTPRecoversAcceptedCommandAfterCollectionCloses(t *testing.T) {
	store := NewMemoryStore()
	examReader := &captureWorkflowExam{status: "collecting"}
	handler := NewHandler(store, nil, examReader, nil, nil)
	const batches = "/api/v1/exams/exam/capture-batches"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/exams/{examId}/capture-batches", handler.CreateBatch)
	mux.HandleFunc("GET /api/v1/exams/{examId}/capture-batches", handler.ListBatches)
	mux.HandleFunc("GET /api/v1/exams/{examId}/capture-batches/commands/{commandId}", handler.RecoverBatchCommand)
	scope := auth.AccessScope{TenantID: "tenant", ActorID: "operator", ExamIDs: []string{"exam"}}
	const input = `{"name":"First import","source_type":"web_upload"}`
	command := map[string]string{"Idempotency-Key": "first-import"}

	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", batches, input, nil, command), http.StatusForbidden, "access_scope_missing")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", batches, input+` {}`, &scope, command), http.StatusBadRequest, "invalid_request")
	type createResponse struct{ Batch Batch }
	first := captureWorkflowJSON[createResponse](t, captureWorkflowRequest(t, mux, "POST", batches, input, &scope, command), http.StatusCreated).Batch
	if first.ID == "" || first.Status != "draft" {
		t.Fatalf("invalid first batch: %+v", first)
	}

	// 操作员断线后可重放已受理的命令。考试进入阅卷后，新的普通批次仍应被拒绝。
	examReader.status = "grading"
	replayed := captureWorkflowJSON[createResponse](t, captureWorkflowRequest(t, mux, "POST", batches, input, &scope, command), http.StatusCreated).Batch
	if replayed.ID != first.ID {
		t.Fatalf("replay created a different batch: %s != %s", replayed.ID, first.ID)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", batches, `{"name":"Changed import","source_type":"web_upload"}`, &scope, command), http.StatusConflict, "capture_revision_conflict")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", batches, input, &scope, map[string]string{"Idempotency-Key": "new-import"}), http.StatusConflict, "exam_not_collecting")
	type recoveryResponse struct{ Command BatchCommandRecovery }
	recovery := captureWorkflowJSON[recoveryResponse](t, captureWorkflowRequest(t, mux, "GET", batches+"/commands/first-import", "", &scope, nil), http.StatusOK).Command
	if recovery.Status != "succeeded" || recovery.Batch == nil || recovery.Batch.ID != first.ID {
		t.Fatalf("existing command not recoverable: %+v", recovery)
	}
	recovery = captureWorkflowJSON[recoveryResponse](t, captureWorkflowRequest(t, mux, "GET", batches+"/commands/new-import", "", &scope, nil), http.StatusOK).Command
	if recovery.Status != "not_accepted" || recovery.Batch != nil {
		t.Fatalf("rejected command left a batch: %+v", recovery)
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", batches, `{"name":"Late page","source_type":"web_upload","purpose":"supplement"}`, &scope, nil), http.StatusBadRequest, "capture_invalid_input")
	supplement := captureWorkflowJSON[createResponse](t, captureWorkflowRequest(t, mux, "POST", batches, `{"name":"Late page","source_type":"web_upload","purpose":"supplement","reason":"replacement scan"}`, &scope, nil), http.StatusCreated).Batch
	if supplement.ID == first.ID || supplement.Purpose != "supplement" {
		t.Fatalf("supplemental batch was not created separately: %+v", supplement)
	}

	type batchPage struct {
		Batches    []Batch
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "GET", batches+"?limit=0", "", &scope, nil), http.StatusBadRequest, "invalid_pagination")
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "GET", batches+"?cursor=bad-token", "", &scope, nil), http.StatusBadRequest, "invalid_pagination")
	page := captureWorkflowJSON[batchPage](t, captureWorkflowRequest(t, mux, "GET", batches+"?limit=1", "", &scope, nil), http.StatusOK)
	if len(page.Batches) != 1 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("invalid first page: %+v", page)
	}
	next := captureWorkflowJSON[batchPage](t, captureWorkflowRequest(t, mux, "GET", batches+"?limit=1&cursor="+url.QueryEscape(page.NextCursor), "", &scope, nil), http.StatusOK)
	if len(next.Batches) != 1 || next.HasMore || next.NextCursor != "" || next.Batches[0].ID == page.Batches[0].ID {
		t.Fatalf("cursor repeated or dropped a batch: first=%+v next=%+v", page, next)
	}
}

func TestCaptureFileHTTPCompletionRejectsInvalidResultsWithoutConsumingLease(t *testing.T) {
	store, assets, runtime := NewMemoryStore(), files.NewMemoryStore(), workerruntime.NewMemoryStore()
	handler := NewHandler(store, assets, nil, runtime, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/capture-batches/{id}/files", handler.RegisterFile)
	mux.HandleFunc("POST /api/v1/capture-batches/{id}/process", handler.ProcessBatch)
	mux.HandleFunc("GET /api/v1/capture-batches/{id}", handler.GetBatch)
	mux.HandleFunc("POST /api/v1/internal/capture/files/{fileId}/result", handler.CompleteFile)
	batch, err := store.CreateBatch(t.Context(), "tenant", "exam", "operator", CreateBatchInput{Name: "Scan recovery", SourceType: "web_upload"})
	if err != nil {
		t.Fatal(err)
	}
	source := createCaptureWorkflowAsset(t, assets, "exam", "source-pdf")
	page := createCaptureWorkflowAsset(t, assets, "exam", "decoded-page")
	wrongExamPage := createCaptureWorkflowAsset(t, assets, "another-exam", "wrong-exam-page")
	secondPage := createCaptureWorkflowAsset(t, assets, "exam", "other-decoded-page")
	path := "/api/v1/capture-batches/" + batch.ID
	requireCaptureWorkflowError(t, captureWorkflowRequest(t, mux, "POST", path+"/files", `{"file_asset_id":"missing","idempotency_key":"first"}`, nil, nil), http.StatusBadRequest, "capture_file_asset_invalid")
	type registrationResponse struct{ File File }
	file := captureWorkflowJSON[registrationResponse](t, captureWorkflowRequest(t, mux, "POST", path+"/files", `{"file_asset_id":"`+source.ID+`","idempotency_key":"first"}`, nil, nil), http.StatusCreated).File
	if file.ID == "" || file.Status != "uploaded" {
		t.Fatalf("invalid file registration: %+v", file)
	}
	captureWorkflowJSON[map[string]Batch](t, captureWorkflowRequest(t, mux, "POST", path+"/process", "", nil, nil), http.StatusOK)
	task := remediationMemoryRuntimeTask(t, runtime, "capture_file", file.ID)
	otherTask := remediationMemoryRuntimeTask(t, runtime, "capture_file", "another-file")
	resultPath := "/api/v1/internal/capture/files/" + file.ID + "/result"
	valid := FileResultInput{TaskID: task.ID, LeaseToken: task.LeaseToken, ResultVersion: "v1", OriginalPageCount: 1, Pages: []DecodedPageInput{{SourceIndex: 1, FileAssetID: page.ID, SHA256: page.HashSHA256, Width: 100, Height: 200}}}
	complete := func(t *testing.T, input FileResultInput) *httptest.ResponseRecorder {
		t.Helper()
		return captureWorkflowRequest(t, mux, "POST", resultPath, captureWorkflowBody(t, input), nil, nil)
	}
	for _, scenario := range []struct {
		name   string
		change func(*FileResultInput)
		status int
		code   string
	}{
		{"different task", func(input *FileResultInput) { input.TaskID, input.LeaseToken = otherTask.ID, otherTask.LeaseToken }, http.StatusConflict, "capture_task_mismatch"},
		{"obsolete lease", func(input *FileResultInput) { input.LeaseToken = "obsolete-token" }, http.StatusConflict, "capture_worker_lease_invalid"},
		{"hash mismatch", func(input *FileResultInput) { input.Pages[0].SHA256 = "wrong-hash" }, http.StatusBadRequest, "decoded_page_asset_invalid"},
		{"page from another exam", func(input *FileResultInput) {
			input.Pages[0].FileAssetID, input.Pages[0].SHA256 = wrongExamPage.ID, wrongExamPage.HashSHA256
		}, http.StatusBadRequest, "decoded_page_asset_invalid"},
		{"duplicate source index", func(input *FileResultInput) { input.Pages = append(input.Pages, input.Pages[0]) }, http.StatusBadRequest, "capture_invalid_input"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := valid
			input.Pages = append([]DecodedPageInput(nil), valid.Pages...)
			scenario.change(&input)
			requireCaptureWorkflowError(t, complete(t, input), scenario.status, scenario.code)
			lease, err := runtime.Get(t.Context(), "tenant", task.ID)
			if err != nil || lease.Status != workerruntime.StatusLeased || lease.LeaseToken != task.LeaseToken {
				t.Fatalf("rejected result changed the valid lease: status=%s err=%v", lease.Status, err)
			}
			detail := captureWorkflowJSON[BatchDetail](t, captureWorkflowRequest(t, mux, "GET", path, "", nil, nil), http.StatusOK)
			if len(detail.Files) != 1 || detail.Files[0].Status != "queued" || len(detail.Pages) != 0 {
				t.Fatalf("rejected result left partial capture state: %+v", detail)
			}
		})
	}

	// 相同回执重放应保留原页面。仅页数相同、实际产物有变化时必须拒绝。
	for range 2 {
		out := captureWorkflowJSON[registrationResponse](t, complete(t, valid), http.StatusOK).File
		if out.ID != file.ID || out.Status != "completed" || out.PageCount != 1 {
			t.Fatalf("invalid accepted result: %+v", out)
		}
	}
	changed := valid
	changed.Pages = append([]DecodedPageInput(nil), valid.Pages...)
	changed.Pages[0].FileAssetID, changed.Pages[0].SHA256 = secondPage.ID, secondPage.HashSHA256
	requireCaptureWorkflowError(t, complete(t, changed), http.StatusConflict, "capture_worker_result_conflict")
	detail := captureWorkflowJSON[BatchDetail](t, captureWorkflowRequest(t, mux, "GET", path, "", nil, nil), http.StatusOK)
	if len(detail.Pages) != 1 || detail.Pages[0].DecodedFileAssetID != page.ID || len(detail.ProcessingSummaries) != 1 || detail.ProcessingSummaries[0].CanComplete {
		t.Fatalf("completion or replay changed stored page or bypassed processing: %+v", detail)
	}
}

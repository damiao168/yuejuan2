package apicontract

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestCaptureBatchCommandHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	document := readOpenAPIDocument(t)
	ctx := context.Background()
	exams := exam.NewMemoryStore()
	created, err := exams.CreateExam(ctx, auth.AccessScope{TenantID: "tenant-1", TenantWide: true}, "actor-1", exam.CreateInput{Name: "Capture contract", SchoolID: "school-1", Subject: "math", TotalScore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err = exams.SetStatusForTest("tenant-1", created.ID, "collecting"); err != nil {
		t.Fatal(err)
	}
	store := capture.NewMemoryStore()
	handler := capture.NewHandler(store, nil, exams, workerruntime.NewMemoryStore(), auth.NewMemoryStore())
	request := scopedContractRequest(httptest.NewRequest("POST", "/create", strings.NewReader(`{"name":"Batch","source_type":"web_upload","idempotency_key":"capture-command-1"}`)))
	request.SetPathValue("examId", created.ID)
	response := httptest.NewRecorder()
	handler.CreateBatch(response, request)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	validateResponseBody(t, document, "/api/v1/exams/{examId}/capture-batches", "post", "201", response.Body.Bytes())
	for _, commandID := range []string{"capture-command-1", "unaccepted-command"} {
		request := scopedContractRequest(httptest.NewRequest("GET", "/recover", nil))
		request.SetPathValue("examId", created.ID)
		request.SetPathValue("commandId", commandID)
		response := httptest.NewRecorder()
		handler.RecoverBatchCommand(response, request)
		if response.Code != 200 {
			t.Fatalf("recover: %d %s", response.Code, response.Body.String())
		}
		validateResponseBody(t, document, "/api/v1/exams/{examId}/capture-batches/commands/{commandId}", "get", "200", response.Body.Bytes())
	}
}

// 契约夹具记录已接受的命令，不运行工作器 I/O；数据库原子性由 PostgreSQL 工作流测试单独验证。
type contractDispatchStore struct{ *paper.MemoryStore }

func (*contractDispatchStore) ReconcileExpiredPaperImportTask(context.Context) (bool, error) {
	panic("HTTP must not reconcile workers")
}
func (*contractDispatchStore) ClaimPendingPaperImportDispatch(context.Context, string, time.Duration) (paper.PaperImportJob, bool, error) {
	panic("HTTP must not claim workers")
}
func (*contractDispatchStore) FailPendingPaperImportDispatch(context.Context, paper.PaperImportJob, string, string) error {
	panic("HTTP must not fail workers")
}

func TestImportAcceptedCommandHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	document := readOpenAPIDocument(t)
	store := &contractDispatchStore{paper.NewMemoryStore()}
	handler := paper.NewHandler(store, auth.NewMemoryStore()).WithDocumentImport(paper.NewDocumentImportService(store, nil, nil, "", "", time.Second))
	submit := func(command importAcceptedContractCommand) paper.PaperImportJob {
		t.Helper()
		raw, err := json.Marshal(command.body)
		if err != nil {
			t.Fatal(err)
		}
		request := scopedContractRequest(httptest.NewRequest(command.method, "/contract-command", bytes.NewReader(raw)))
		request.SetPathValue(command.idKey, command.id)
		request.Header.Set("Idempotency-Key", "contract-"+command.method+"-"+command.status)
		response := httptest.NewRecorder()
		command.handle(response, request)
		if response.Code != command.code {
			t.Fatalf("%s: status=%d body=%s", command.route, response.Code, response.Body.String())
		}
		validateResponseBody(t, document, command.route, strings.ToLower(command.method), command.status, response.Body.Bytes())
		var result struct {
			Import paper.PaperImportJob `json:"import"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Import
	}
	job := submit(importAcceptedContractCommand{"/api/v1/exams/{examId}/paper-imports", "POST", "examId", "exam-1", paper.CreatePaperImportInput{Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "file-1", RoleHint: "question"}}}, "201", 201, handler.CreatePaperImport})
	job = submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/sources", "POST", "id", job.ID, paper.AddPaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "file-2", DocumentIndex: 1, RoleHint: "answer"}}}, "202", 202, handler.AddPaperImportSources})
	job = submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/sources", "PUT", "id", job.ID, paper.ReplacePaperImportSourcesInput{ExpectedGeneration: job.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, RoleHint: "question"}}}, "202", 202, handler.ReplacePaperImportSources})
	if job.Generation != 3 || job.Status != "processing" {
		t.Fatalf("accepted response lost operation identity: %#v", job)
	}
	submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/review", "PUT", "id", job.ID, map[string]any{}, "400", 400, handler.SavePaperImportReview})
	submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/apply", "POST", "id", job.ID, map[string]any{}, "409", 409, handler.ApplyPaperImport})
	score := 3.0
	processed, err := store.CompletePaperImportCandidates(context.Background(), "tenant-1", job.ID, paper.PaperImportParseResult{
		QuestionCandidates: []paper.QuestionCandidate{{CandidateID: "q1", QuestionNoRaw: "1", QuestionType: "single_choice", Score: &score, Stem: "Choose", Confidence: .95, SourceRefs: []paper.PaperImportSourceRef{}}},
		AnswerCandidates:   []paper.AnswerCandidate{{CandidateID: "a1", QuestionNoHint: "1", StandardAnswer: "A", Confidence: .95, SourceRefs: []paper.PaperImportSourceRef{}}},
		SolutionCandidates: []paper.SolutionCandidate{}, RubricCandidates: []paper.RubricCandidate{}, Issues: []paper.PaperImportIssue{},
	})
	if err != nil {
		t.Fatal(err)
	}
	processed.Questions[0].HumanConfirmedFields = []string{"question_no", "question_type", "score", "stem", "answer", "rubric"}
	job = submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/review", "PUT", "id", job.ID, paper.ReviewPaperImportInput{ExpectedGeneration: job.Generation, ExpectedUpdatedAt: processed.UpdatedAt.Format(time.RFC3339Nano), Questions: processed.Questions}, "200", 200, handler.SavePaperImportReview})
	job = submit(importAcceptedContractCommand{"/api/v1/paper-imports/{id}/apply", "POST", "id", job.ID, map[string]any{}, "200", 200, handler.ApplyPaperImport})
	if job.Status != "applied" {
		t.Fatalf("apply response lost terminal state: %#v", job)
	}
}

func TestReliabilitySharedClientContract(t *testing.T) {
	if os.Getenv("EDUGRADE_CLIENT_CONTRACT") != "1" {
		t.Skip("shared client contract runs in the Node-enabled CI job")
	}
	document := readOpenAPIDocument(t)
	store := processing.NewMemoryStore()
	store.PutState("tenant-1", processing.PageState{PageID: "page-1", SubmissionID: "submission-1", ExamID: "exam-1", CurrentStage: processing.StageReceived, Blocking: true, IssueCode: processing.IssueMissingIdentity})
	handler := processing.NewHandler(processing.NewService(store, workerruntime.NewMemoryStore()), auth.NewMemoryStore())
	summaryRequest := scopedContractRequest(httptest.NewRequest(http.MethodGet, "/api/v1/exams/exam-1/processing/summary", nil))
	summaryRequest.SetPathValue("examId", "exam-1")
	summary := httptest.NewRecorder()
	handler.Summary(summary, summaryRequest)
	validateResponseBody(t, document, "/api/v1/exams/{examId}/processing/summary", "get", "200", summary.Body.Bytes())
	invalid := httptest.NewRecorder()
	handler.ListExceptions(invalid, scopedContractRequest(httptest.NewRequest(http.MethodGet, "/api/v1/processing/exceptions?status=invalid", nil)))
	validateResponseBody(t, document, "/api/v1/processing/exceptions", "get", "400", invalid.Body.Bytes())
	// 两个客户端接收上面真实处理器生成、已通过 OpenAPI 校验的相同字节，其中包含关联元数据。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := summary
		if r.URL.Path == "/api/v1/processing/exceptions" {
			response = invalid
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.Code)
		_, _ = w.Write(response.Body.Bytes())
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/ci/shared-client-contract.mjs", server.URL)
	command.Dir = filepath.Join("..", "..", "..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("shared client contract: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func TestImportAndProjectionHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	document := readOpenAPIDocument(t)
	imports := paper.NewMemoryStore()
	job, err := imports.CreatePaperImport(context.Background(), "tenant-1", "exam-1", "actor-1", paper.CreatePaperImportInput{
		Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "file-1", RoleHint: "question"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	importHandler := paper.NewHandler(imports, auth.NewMemoryStore())
	projection := processing.NewMemoryStore()
	projection.PutState("tenant-1", processing.PageState{PageID: "page-1", SubmissionID: "submission-1", ExamID: "exam-1", CurrentStage: processing.StageReceived, Blocking: true, IssueCode: processing.IssueMissingIdentity})
	projectionHandler := processing.NewHandler(processing.NewService(projection, workerruntime.NewMemoryStore()), auth.NewMemoryStore())
	for _, scenario := range []struct {
		name, route, url, pathKey, pathValue, status string
		code                                         int
		handler                                      http.HandlerFunc
	}{
		{"import processing", "/api/v1/paper-imports/{id}", "/api/v1/paper-imports/" + job.ID, "id", job.ID, "200", 200, importHandler.GetPaperImport},
		{"import missing", "/api/v1/paper-imports/{id}", "/api/v1/paper-imports/missing", "id", "missing", "404", 404, importHandler.GetPaperImport},
		{"projection summary", "/api/v1/exams/{examId}/processing/summary", "/api/v1/exams/exam-1/processing/summary", "examId", "exam-1", "200", 200, projectionHandler.Summary},
		{"projection exception", "/api/v1/processing/exceptions", "/api/v1/processing/exceptions?exam_id=exam-1", "", "", "200", 200, projectionHandler.ListExceptions},
		{"invalid exception filter", "/api/v1/processing/exceptions", "/api/v1/processing/exceptions?status=not-a-state", "", "", "400", 400, projectionHandler.ListExceptions},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := scopedContractRequest(httptest.NewRequest(http.MethodGet, scenario.url, nil))
			if scenario.pathKey != "" {
				request.SetPathValue(scenario.pathKey, scenario.pathValue)
			}
			response := httptest.NewRecorder()
			scenario.handler(response, request)
			if response.Code != scenario.code {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			validateResponseBody(t, document, scenario.route, "get", scenario.status, response.Body.Bytes())
		})
	}
}

func TestReliabilityWriteHandlerResponsesValidateAgainstOpenAPI(t *testing.T) {
	document := readOpenAPIDocument(t)
	ctx := context.Background()
	imports := paper.NewMemoryStore()
	job, err := imports.CreatePaperImport(ctx, "tenant-1", "exam-1", "actor-1", paper.CreatePaperImportInput{Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: "file-1", RoleHint: "question"}}})
	if err != nil {
		t.Fatal(err)
	}
	importHandler := paper.NewHandler(imports, auth.NewMemoryStore())
	projection := processing.NewMemoryStore()
	runtime := workerruntime.NewMemoryStore()
	_, err = runtime.CreateTask(ctx, "tenant-1", "actor-1", workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: "ocr", SourceType: "submission_page", SourceID: "page-1", IdempotencyKey: "contract-retry", PayloadSchemaVersion: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	projection.PutState("tenant-1", processing.PageState{PageID: "page-1", SubmissionID: "submission-1", ExamID: "exam-1", CurrentStage: processing.StageReceived, Blocking: true, IssueCode: processing.IssueOCRLowConfidence, Retryable: true, RetrySourceType: "submission_page", RetrySourceID: "page-1"})
	listed, err := projection.ListExceptions(ctx, "tenant-1", processing.ExceptionFilter{ExamID: "exam-1"})
	if err != nil || len(listed.Exceptions) != 1 {
		t.Fatalf("exception fixture: %#v %v", listed, err)
	}
	exceptionID := listed.Exceptions[0].ID
	handler := processing.NewHandler(processing.NewService(projection, runtime), auth.NewMemoryStore())
	for _, scenario := range []struct {
		name, route, id, query, body, status string
		code                                 int
		handler                              http.HandlerFunc
	}{
		{"cancel", "/api/v1/paper-imports/{id}/cancel", job.ID, "?expected_generation=1", "", "200", 200, importHandler.CancelPaperImport},
		{"cancel missing generation", "/api/v1/paper-imports/{id}/cancel", job.ID, "", "", "400", 400, importHandler.CancelPaperImport},
		{"retry already queued", "/api/v1/processing/exceptions/{id}/retry", exceptionID, "", "", "202", 202, handler.Retry},
		{"assign", "/api/v1/processing/exceptions/{id}/assign", exceptionID, "", `{"assignee_id":"actor-2"}`, "200", 200, handler.Assign},
		{"resolve", "/api/v1/processing/exceptions/{id}/resolve", exceptionID, "", `{"resolution":"operator_confirmed"}`, "200", 200, handler.Resolve},
		{"invalid assignment", "/api/v1/processing/exceptions/{id}/assign", exceptionID, "", `{}`, "400", 400, handler.Assign},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := scopedContractRequest(httptest.NewRequest(http.MethodPost, strings.ReplaceAll(scenario.route, "{id}", scenario.id)+scenario.query, strings.NewReader(scenario.body)))
			request.SetPathValue("id", scenario.id)
			response := httptest.NewRecorder()
			scenario.handler(response, request)
			if response.Code != scenario.code {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			validateResponseBody(t, document, scenario.route, "post", scenario.status, response.Body.Bytes())
		})
	}
}

// 同一命令的路由、路径绑定、请求与期望状态放在一起，避免复用调用中交换参数；请求时序仍由测试顺序决定。
type importAcceptedContractCommand struct {
	route, method, idKey, id string
	body                     any
	status                   string
	code                     int
	handle                   http.HandlerFunc
}

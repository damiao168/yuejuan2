package server

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 先创建一份只允许一次尝试的运行，领取后过期并由运行时关闭，后续协调才处理导入终态。
func e2eCreateExhaustedImportAttempt(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context) (paper.PaperImportJob, string) {
	t.Helper()
	db, tenantID, userID, first, paperStore, createImport, runtimeStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.Paper, scenario.ImportInput, scenario.Runtime
	input := createImport
	input.CommandID = "abandoned-final-" + uuid.NewString()
	job, err := paperStore.CreatePaperImport(ctx, tenantID, first.Exams[0].ID, userID, input)
	if err != nil {
		t.Fatal(err)
	}
	assets := []paper.PaperImportOCRAsset{{SourceID: job.Sources[0].ID, DocumentIndex: job.Sources[0].DocumentIndex, RoleHint: job.Sources[0].RoleHint, FileAssetID: job.Sources[0].FileAssetID, ContentType: "application/pdf"}}
	if err := paperStore.QueuePaperImportOCR(ctx, tenantID, job, userID, assets); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.QueryRow(`UPDATE agent_worker_task SET max_attempts=1,queue_name='audit-expired-import' WHERE paper_import_run_id=$1::uuid RETURNING id::text`, job.RunID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	tasks, err := runtimeStore.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: "audit-expired-import", WorkerService: "audit", WorkerInstanceID: "crashed", Limit: 1, LeaseSeconds: 60})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim: %#v %v", tasks, err)
	}
	if _, err := db.Exec(`UPDATE agent_worker_task SET lease_expires_at=now()-interval '1 second' WHERE id=$1::uuid`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeStore.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: "audit-expired-import", WorkerService: "audit", WorkerInstanceID: "new", Limit: 1, LeaseSeconds: 60}); err != nil {
		t.Fatal(err)
	}
	return job, taskID
}

// 生产采集页删除和恢复按有效页集合更新投影，原嵌套命令恢复子测试先执行。
func e2eAssertCaptureProjectionLifecycle(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, separate := scenario.DB, scenario.Separate
	ctx := context.Background()
	examID := separate.Exams[0].ID
	if _, err := db.Exec(`UPDATE exam SET status='collecting' WHERE id=$1::uuid`, examID); err != nil {
		t.Fatal(err)
	}
	t.Run("capture batch durable command recovery", func(t *testing.T) {
		e2eAssertCaptureCommandRecovery(t, scenario, ctx, examID)
	})
	before, captureStore, cpID, spID := e2eCreateCaptureProjectionPage(t, scenario, ctx, examID)
	e2eDeleteAndRestoreCaptureProjection(t, scenario, ctx, examID, captureProjectionPage{Before: before, Capture: captureStore, CapturePageID: cpID, SubmissionPageID: spID})
}

// 采集命令并发、删除后重放和传输收据丢失均使用同一生产存储及中间件。
func e2eAssertCaptureCommandRecovery(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, examID string) {
	t.Helper()
	db, tenantID, userID := scenario.DB, scenario.TenantID, scenario.UserID
	store := capture.NewPostgresStore(db)
	input := capture.CreateBatchInput{Name: "Command batch", SourceType: "web_upload", IdempotencyKey: uuid.NewString()}
	type result struct {
		batch capture.Batch
		err   error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			batch, err := store.CreateBatch(ctx, tenantID, examID, userID, input)
			results <- result{batch, err}
		}()
	}
	close(start)
	left, right := <-results, <-results
	if left.err != nil || right.err != nil || left.batch.ID != right.batch.ID {
		t.Fatalf("concurrent command: %#v %#v", left, right)
	}
	changed := input
	changed.Name = "Changed request"
	if _, err := store.CreateBatch(ctx, tenantID, examID, userID, changed); !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("changed request: %v", err)
	}
	if _, err := db.Exec(`UPDATE capture_batch SET deleted_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, left.batch.ID); err != nil {
		t.Fatal(err)
	}
	fresh := capture.NewPostgresStore(db)
	replayed, err := fresh.CreateBatch(ctx, tenantID, examID, userID, input)
	if err != nil || replayed.ID != left.batch.ID {
		t.Fatalf("deleted batch command duplicated: %#v %v", replayed, err)
	}
	recovery, err := fresh.RecoverBatchCommand(ctx, tenantID, examID, userID, input.IdempotencyKey)
	if err != nil || recovery.Status != "succeeded" || recovery.Batch == nil || recovery.Batch.ID != left.batch.ID {
		t.Fatalf("recovery: %#v %v", recovery, err)
	}
	input.IdempotencyKey = uuid.NewString()
	newBatch, err := fresh.CreateBatch(ctx, tenantID, examID, userID, input)
	if err != nil || newBatch.ID == left.batch.ID {
		t.Fatalf("new command incorrectly merged: %#v %v", newBatch, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM capture_batch WHERE tenant_id=$1::uuid AND name='Command batch'`, tenantID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("command count=%d %v", count, err)
	}
	e2eAssertCaptureLostReceiptTakeover(t, scenario, ctx, examID, fresh)
}

// 业务采集批次先提交而收据写入失败，恢复及准确重试只认同一个批次，接管不重复创建。
func e2eAssertCaptureLostReceiptTakeover(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, examID string, fresh *capture.PostgresStore) {
	t.Helper()
	db, tenantID, userID, scope := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Scope
	var count int
	handler := capture.NewHandler(fresh, nil, exam.NewPostgresStore(db), workerruntime.NewPostgresStore(db), auth.NewPostgresStore(db))
	transport := &failCompleteOnceIdempotencyStore{Store: idempotency.NewPostgresStore(db)}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams/{examId}/capture-batches", idempotency.Middleware(transport, idempotency.Options{Enforce: true, TTL: time.Hour, ProcessingTimeout: time.Nanosecond})(http.HandlerFunc(handler.CreateBatch)))
	commandInput := capture.CreateBatchInput{Name: "Lost batch response", SourceType: "web_upload", IdempotencyKey: uuid.NewString()}
	raw, _ := json.Marshal(commandInput)
	request := func() *http.Request {
		req := httptest.NewRequest("POST", "/api/v1/exams/"+examID+"/capture-batches", bytes.NewReader(raw))
		req.Header.Set("Idempotency-Key", commandInput.IdempotencyKey)
		return req.WithContext(auth.WithAccessScope(auth.WithUser(ctx, auth.User{ID: userID, TenantID: tenantID}), scope))
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request())
	if response.Code != 503 {
		t.Fatalf("receipt fault: %d %s", response.Code, response.Body.String())
	}
	recoveredAfter503, err := fresh.RecoverBatchCommand(ctx, tenantID, examID, userID, commandInput.IdempotencyKey)
	if err != nil || recoveredAfter503.Status != "succeeded" || recoveredAfter503.Batch == nil {
		t.Fatalf("receipt failure lost business fact: %#v %v", recoveredAfter503, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM capture_batch WHERE tenant_id=$1::uuid AND operator_id=$2::uuid AND idempotency_key=$3`, tenantID, userID, commandInput.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("503 command count=%d %v", count, err)
	}
	retry := httptest.NewRecorder()
	mux.ServeHTTP(retry, request())
	if retry.Code != http.StatusCreated || !strings.Contains(retry.Body.String(), recoveredAfter503.Batch.ID) {
		t.Fatalf("stale capture command did not recover committed batch: %d %s", retry.Code, retry.Body.String())
	}
	if err := db.QueryRow(`SELECT count(*) FROM capture_batch WHERE tenant_id=$1::uuid AND operator_id=$2::uuid AND idempotency_key=$3`, tenantID, userID, commandInput.IdempotencyKey).Scan(&count); err != nil || count != 1 {
		t.Fatalf("capture takeover duplicated batch: count=%d %v", count, err)
	}

}

type captureProjectionPage struct {
	Before                          processing.Summary
	Capture                         *capture.PostgresStore
	CapturePageID, SubmissionPageID string
}

// 生产采集前保存有效页统计，建立原提交与采集页关联后才验证新增一页。
func e2eCreateCaptureProjectionPage(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, examID string) (processing.Summary, *capture.PostgresStore, string, string) {
	t.Helper()
	db, tenantID, userID, fileID, projectionStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.FileID, scenario.Projection
	before, err := projectionStore.Summary(ctx, tenantID, examID)
	if err != nil {
		t.Fatal(err)
	}
	captureStore := capture.NewPostgresStore(db)
	batch, err := captureStore.CreateBatch(ctx, tenantID, examID, userID, capture.CreateBatchInput{Name: "Projection lifecycle", SourceType: "web_upload"})
	if err != nil {
		t.Fatal(err)
	}
	var subID, spID, cfID, cpID string
	if err := db.QueryRow(`INSERT INTO submission(tenant_id,exam_id,source_type,status,collected_by) VALUES($1::uuid,$2::uuid,'pdf_upload','pages_uploaded',$3::uuid) RETURNING id::text`, tenantID, examID, userID).Scan(&subID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,1,'accepted') RETURNING id::text`, tenantID, subID, fileID).Scan(&spID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,idempotency_key,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'lifecycle.pdf','application/pdf',repeat('a',64),1,'lifecycle-file',$4::uuid) RETURNING id::text`, tenantID, batch.ID, fileID, userID).Scan(&cfID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO capture_page(tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status) VALUES($1::uuid,$2::uuid,$3::uuid,1,$4::uuid,$5::uuid,1,1,$6::uuid,'needs_review') RETURNING id::text`, tenantID, batch.ID, cfID, subID, spID, fileID).Scan(&cpID); err != nil {
		t.Fatal(err)
	}
	if err := projectionStore.RefreshExam(ctx, tenantID, examID); err != nil {
		t.Fatal(err)
	}
	active, err := projectionStore.Summary(ctx, tenantID, examID)
	if err != nil || active.TotalPages != before.TotalPages+1 {
		t.Fatalf("capture page not counted: %#v %v", active, err)
	}
	return before, captureStore, cpID, spID
}

// 删除减去一页，恢复必须按新的生命周期修订恢复该页并重新打开原问题。
func e2eDeleteAndRestoreCaptureProjection(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, examID string, page captureProjectionPage) {
	t.Helper()
	db, tenantID, userID, projectionStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Projection
	before, captureStore, cpID, spID := page.Before, page.Capture, page.CapturePageID, page.SubmissionPageID
	deleted, err := captureStore.DeletePage(ctx, tenantID, cpID, userID, capture.PageLifecycleInput{Revision: 1, Reason: "projection acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	if err := projectionStore.RefreshExam(ctx, tenantID, examID); err != nil {
		t.Fatal(err)
	}
	after, err := projectionStore.Summary(ctx, tenantID, examID)
	if err != nil || after.TotalPages != before.TotalPages {
		t.Fatalf("deleted capture page remains counted: %#v %v", after, err)
	}
	if _, err := captureStore.RestorePage(ctx, tenantID, cpID, userID, capture.PageLifecycleInput{Revision: deleted.Revision, Reason: "restore acceptance"}); err != nil {
		t.Fatal(err)
	}
	if err := projectionStore.RefreshExam(ctx, tenantID, examID); err != nil {
		t.Fatal(err)
	}
	restored, err := projectionStore.Summary(ctx, tenantID, examID)
	if err != nil || restored.TotalPages != before.TotalPages+1 {
		t.Fatalf("restored capture page missing: %#v %v", restored, err)
	}
	var reopened int
	if err := db.QueryRow(`SELECT count(*) FROM operational_exception WHERE tenant_id=$1::uuid AND page_id=$2::uuid AND status='open'`, tenantID, spID).Scan(&reopened); err != nil || reopened != 1 {
		t.Fatalf("restored issue did not recur: %d %v", reopened, err)
	}

}

// 冻结考试仍恢复已接受命令，但新导入、添加来源和重新运行都拒绝且不改当前代次。
func e2eAssertFrozenImportCommands(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, first, paperStore, createImport, job := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.Paper, scenario.ImportInput, scenario.Job
	ctx := context.Background()
	current, err := paperStore.GetPaperImport(ctx, tenantID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE exam SET status='collecting' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	accepted, err := paperStore.CreatePaperImport(ctx, tenantID, first.Exams[0].ID, userID, createImport)
	if err != nil || accepted.ID != job.ID {
		t.Fatalf("frozen exam lost accepted command recovery: %#v %v", accepted, err)
	}
	newInput := createImport
	newInput.CommandID = uuid.NewString()
	if _, err := paperStore.CreatePaperImport(ctx, tenantID, first.Exams[0].ID, userID, newInput); !errors.Is(err, paper.ErrExamFrozen) {
		t.Fatalf("frozen exam accepted a new import: %v", err)
	}
	if _, err := paperStore.AddPaperImportSources(ctx, tenantID, job.ID, userID, paper.AddPaperImportSourcesInput{CommandID: uuid.NewString(), ExpectedGeneration: current.Generation, Sources: createImport.Sources}); !errors.Is(err, paper.ErrExamFrozen) {
		t.Fatalf("frozen exam accepted new sources: %v", err)
	}
	if _, err := paperStore.ReplacePaperImportSources(ctx, tenantID, job.ID, userID, paper.ReplacePaperImportSourcesInput{CommandID: uuid.NewString(), ExpectedGeneration: current.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: current.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}}}); !errors.Is(err, paper.ErrExamFrozen) {
		t.Fatalf("frozen exam accepted rerun: %v", err)
	}
	unchanged, err := paperStore.GetPaperImport(ctx, tenantID, job.ID)
	if err != nil || unchanged.Generation != current.Generation || unchanged.Status != current.Status {
		t.Fatalf("rejected frozen commands changed current run: %#v %v", unchanged, err)
	}

}

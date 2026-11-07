package server

import (
	"bytes"
	"crypto/sha256"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// OCR 回调原子交接持久解析任务，新执行器模拟进程重启，提交失败后按原租约恢复。
func e2eAssertPaperOCRParseRecovery(t *testing.T, scenario paperApplyGateScenario) {
	t.Helper()
	recovery := paperParseRecovery{Gate: scenario}
	recovery.Files, recovery.Objects, recovery.Job, recovery.ClaimedOCR = e2eCreatePaperOCRRecovery(t, recovery)
	e2eCompleteOCRAndAssertParseHandoff(t, recovery)

	parser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode parser request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"documents":[],"question_candidates":[{"candidate_id":"q-recovered","question_no_raw":"1","question_type":"short_answer","stem":"What is 6 x 7?","score":1,"confidence":0.99}],"answer_candidates":[],"solution_candidates":[],"rubric_candidates":[],"issues":[]}`))
	}))
	defer parser.Close()
	recovery.Executor, recovery.TaskID = e2eCreateRestartedPaperParseExecutor(t, recovery, parser.URL)
	recovery.Recovered = e2eRollbackAndRecoverPaperParseCommit(t, recovery)
	recovery.ReplayLeaseToken, recovery.Binding, recovery.Result, recovery.ChangedResult = e2eAssertPaperParseExactReceipt(t, recovery)
	newRun := e2eRacePaperHumanReviewAndSuccessor(t, recovery)
	e2eAssertHistoricalPaperReceiptFenced(t, recovery, newRun)
}

type paperParseRecovery struct {
	Gate                  paperApplyGateScenario
	Files                 *files.PostgresStore
	Objects               *files.MemoryObjectStorage
	Job                   paper.PaperImportJob
	ClaimedOCR            []workerruntime.Task
	Executor              *paper.ParseTaskExecutor
	TaskID                string
	Recovered             paper.PaperImportJob
	ReplayLeaseToken      string
	Binding               paper.PaperImportRunBinding
	Result, ChangedResult paper.PaperImportParseResult
}

// 建立独立合成页与运行，原任务绑定被冻结的运行及代次后才领取 OCR 租约。
func e2eCreatePaperOCRRecovery(t *testing.T, recovery paperParseRecovery) (*files.PostgresStore, *files.MemoryObjectStorage, paper.PaperImportJob, []workerruntime.Task) {
	t.Helper()
	ctx, db, store, runtimeStore, tenantID, userID, examID, paperFileID := recovery.Gate.Context, recovery.Gate.DB, recovery.Gate.Store, recovery.Gate.Runtime, recovery.Gate.TenantID, recovery.Gate.UserID, recovery.Gate.ExamID, recovery.Gate.PaperFileID
	pageContent := []byte("synthetic paper import page")
	pageHash := fmt.Sprintf("%x", sha256.Sum256(pageContent))
	fileStore := files.NewPostgresStore(db)
	pageAsset, createErr := fileStore.Create(ctx, files.CreateAssetInput{
		TenantID: tenantID, ExamID: examID, OwnerType: "exam", OwnerID: examID,
		OriginalName: "paper-page.png", ContentType: "image/png", SizeBytes: int64(len(pageContent)), HashSHA256: pageHash,
		StorageBucket: "paper-import-e2e", StorageKey: "paper-page.png", Visibility: "tenant", UploadedBy: userID,
	})
	if createErr != nil {
		t.Fatalf("create parsed page asset: %v", createErr)
	}
	objectStore := files.NewMemoryObjectStorage()
	if putErr := objectStore.Put(ctx, pageAsset.StorageBucket, pageAsset.StorageKey, bytes.NewReader(pageContent), int64(len(pageContent)), pageAsset.ContentType); putErr != nil {
		t.Fatalf("store parsed page object: %v", putErr)
	}
	parseJob, createErr := store.CreatePaperImport(ctx, tenantID, examID, userID, paper.CreatePaperImportInput{
		Subject: "mathematics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: paperFileID, DocumentIndex: 0, RoleHint: "question"}},
	})
	if createErr != nil {
		t.Fatalf("create parse recovery fixture: %v", createErr)
	}
	ocrTask, createErr := runtimeStore.CreateTask(ctx, tenantID, userID, workerruntime.CreateTaskInput{
		TaskType: "ocr", QueueName: "ocr", SourceType: "paper_import_job", SourceID: parseJob.ID,
		IdempotencyKey: "paper-parse-recovery-ocr:" + parseJob.ID, PayloadSchemaVersion: "test-v1",
		Payload: map[string]any{"pages": []paper.PaperImportDecodedPage{{
			SourceID: parseJob.Sources[0].ID, DocumentIndex: 0, PageNo: 1, FileAssetID: pageAsset.ID, SHA256: pageHash,
		}}},
	})
	if createErr != nil {
		t.Fatalf("create OCR runtime fixture: %v", createErr)
	}
	if _, bindErr := db.ExecContext(ctx, `UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4,task_protocol_version=2 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, ocrTask.ID, parseJob.RunID, parseJob.Generation); bindErr != nil {
		t.Fatalf("bind OCR runtime fixture to import run: %v", bindErr)
	}
	if _, bindErr := db.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='queued',initial_task_id=$2::uuid WHERE tenant_id=$1::uuid AND id=$3::uuid`, tenantID, ocrTask.ID, parseJob.RunID); bindErr != nil {
		t.Fatalf("mark import dispatch queued: %v", bindErr)
	}
	claimed, claimErr := runtimeStore.Claim(ctx, tenantID, workerruntime.ClaimInput{
		QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "recovery-test", Limit: 1, LeaseSeconds: 300,
	})
	if claimErr != nil || len(claimed) != 1 || claimed[0].ID != ocrTask.ID {
		t.Fatalf("claim OCR runtime fixture: %#v %v", claimed, claimErr)
	}
	return fileStore, objectStore, parseJob, claimed
}

// OCR 成功与一个排队解析必须同次提交，后续重启只使用持久化输入。
func e2eCompleteOCRAndAssertParseHandoff(t *testing.T, recovery paperParseRecovery) {
	t.Helper()
	ctx, db, store, tenantID, paperFileID := recovery.Gate.Context, recovery.Gate.DB, recovery.Gate.Store, recovery.Gate.TenantID, recovery.Gate.PaperFileID
	parseJob, claimed := recovery.Job, recovery.ClaimedOCR
	block := paper.PaperImportOCRBlock{SourceID: parseJob.Sources[0].ID, DocumentIndex: 0, PageNo: 1, BlockID: "b1", Text: "1. What is 6 x 7?", Confidence: .99}
	parseInput := paper.PaperImportParseRequest{Documents: []paper.PaperImportParseDocument{{
		SourceID: parseJob.Sources[0].ID, FileAssetID: paperFileID, DocumentIndex: 0, RoleHint: "question", Content: block.Text, Blocks: []paper.PaperImportOCRBlock{block},
	}}}
	if completeErr := store.CompletePaperImportOCR(ctx, tenantID, parseJob.ID, paper.PaperImportOCRResult{
		TaskID: claimed[0].ID, LeaseToken: claimed[0].LeaseToken, DurationMS: 10, Blocks: []paper.PaperImportOCRBlock{block},
	}, parseInput); completeErr != nil {
		t.Fatalf("complete OCR and queue parse: %v", completeErr)
	}
	var succeededOCR, queuedParse int
	if queryErr := db.QueryRowContext(ctx, `SELECT
	  count(*) FILTER (WHERE task_type='ocr' AND status='succeeded'),
	  count(*) FILTER (WHERE task_type='paper_parse' AND status='queued')
	FROM agent_worker_task WHERE tenant_id=$1::uuid AND
	  ((source_type='paper_import_job' AND source_id=$2::uuid) OR
	   (source_type='paper_import_parse' AND source_id IN
	     (SELECT id FROM paper_import_parse_input WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid)))`, tenantID, parseJob.ID).Scan(&succeededOCR, &queuedParse); queryErr != nil || succeededOCR != 1 || queuedParse != 1 {
		t.Fatalf("OCR/parse handoff was not atomic: ocr=%d parse=%d err=%v", succeededOCR, queuedParse, queryErr)
	}

}

// 构造新的服务及执行器，沿用已提交的 OCR 输入；测试解析服务仍由外层保持开启。
func e2eCreateRestartedPaperParseExecutor(t *testing.T, recovery paperParseRecovery, parserURL string) (*paper.ParseTaskExecutor, string) {
	t.Helper()
	ctx, db, store, runtimeStore, tenantID := recovery.Gate.Context, recovery.Gate.DB, recovery.Gate.Store, recovery.Gate.Runtime, recovery.Gate.TenantID
	fileStore, objectStore, parseJob := recovery.Files, recovery.Objects, recovery.Job
	if _, updateErr := db.ExecContext(ctx, `UPDATE paper_import_run SET dispatch_status='not_required' WHERE tenant_id=$1::uuid AND dispatch_status='pending'`, tenantID); updateErr != nil {
		t.Fatalf("close direct-store dispatch fixtures: %v", updateErr)
	}
	// 构造新服务和执行器，模拟网关进程在 OCR 回调提交之后、解析开始之前重启。
	restartedService := paper.NewDocumentImportService(store, fileStore, objectStore, parserURL, strings.Repeat("t", 32), time.Minute)
	executor, executorErr := paper.NewParseTaskExecutor(restartedService, runtimeStore, time.Minute)
	if executorErr != nil {
		t.Fatalf("create restarted parse executor: %v", executorErr)
	}
	var parseTaskID string
	if queryErr := db.QueryRowContext(ctx, `SELECT task.id::text FROM agent_worker_task task JOIN paper_import_parse_input input ON input.tenant_id=task.tenant_id AND input.id=task.source_id WHERE task.tenant_id=$1::uuid AND input.paper_import_id=$2::uuid AND task.task_type='paper_parse'`, tenantID, parseJob.ID).Scan(&parseTaskID); queryErr != nil {
		t.Fatalf("load parse task: %v", queryErr)
	}
	return executor, parseTaskID
}

// 触发器失败必须回滚任务及候选发布；删除注入后令原租约过期，由新执行器重领恢复。
func e2eRollbackAndRecoverPaperParseCommit(t *testing.T, recovery paperParseRecovery) paper.PaperImportJob {
	t.Helper()
	ctx, db, store, tenantID := recovery.Gate.Context, recovery.Gate.DB, recovery.Gate.Store, recovery.Gate.TenantID
	parseJob, executor, parseTaskID := recovery.Job, recovery.Executor, recovery.TaskID
	if _, triggerErr := db.ExecContext(ctx, `CREATE FUNCTION fail_test_paper_parse_publish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.status='processing' AND NEW.status='review_required' THEN RAISE EXCEPTION 'injected parse publish failure'; END IF; RETURN NEW; END $$`); triggerErr != nil {
		t.Fatalf("install parse commit failure injection: %v", triggerErr)
	}
	if _, triggerErr := db.ExecContext(ctx, `CREATE TRIGGER fail_test_paper_parse_publish BEFORE UPDATE ON paper_import_job FOR EACH ROW EXECUTE FUNCTION fail_test_paper_parse_publish()`); triggerErr != nil {
		t.Fatalf("install parse commit failure trigger: %v", triggerErr)
	}
	worked, runErr := executor.RunOnce(ctx)
	if runErr == nil || !worked {
		t.Fatalf("injected parse commit must fail after doing work: worked=%v err=%v", worked, runErr)
	}
	var taskStatus, jobStatus string
	var candidateCount int
	if queryErr := db.QueryRowContext(ctx, `SELECT task.status,job.status,jsonb_array_length(job.question_candidates) FROM agent_worker_task task CROSS JOIN paper_import_job job WHERE task.tenant_id=$1::uuid AND task.id=$2::uuid AND job.tenant_id=$1::uuid AND job.id=$3::uuid`, tenantID, parseTaskID, parseJob.ID).Scan(&taskStatus, &jobStatus, &candidateCount); queryErr != nil {
		t.Fatalf("inspect rolled back parse commit: %v", queryErr)
	}
	if taskStatus != "running" || jobStatus != "processing" || candidateCount != 0 {
		t.Fatalf("parse commit was partially published: task=%s job=%s candidates=%d", taskStatus, jobStatus, candidateCount)
	}
	if _, dropErr := db.ExecContext(ctx, `DROP TRIGGER fail_test_paper_parse_publish ON paper_import_job`); dropErr != nil {
		t.Fatalf("remove parse commit failure injection: %v", dropErr)
	}
	if _, dropErr := db.ExecContext(ctx, `DROP FUNCTION fail_test_paper_parse_publish()`); dropErr != nil {
		t.Fatalf("remove parse commit failure function: %v", dropErr)
	}
	if _, expireErr := db.ExecContext(ctx, `UPDATE agent_worker_task SET lease_expires_at=now()-interval '1 second' WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, parseTaskID); expireErr != nil {
		t.Fatalf("expire failed parse attempt: %v", expireErr)
	}
	worked, runErr = executor.RunOnce(ctx)
	if runErr != nil || !worked {
		t.Fatalf("resume durable parse task after rollback: worked=%v err=%v", worked, runErr)
	}
	recovered, getErr := store.GetPaperImport(ctx, tenantID, parseJob.ID)
	if getErr != nil || recovered.Status != "review_required" || len(recovered.QuestionCandidates) != 1 || recovered.QuestionCandidates[0].CandidateID != "q-recovered" {
		t.Fatalf("recovered parse did not persist one candidate: %#v err=%v", recovered, getErr)
	}
	return recovered
}

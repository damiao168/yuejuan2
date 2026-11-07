package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

// 并发初始导入只创建一个运行，相同命令重放而来源身份改变拒绝。
func e2eCreateReliableImportCommand(t *testing.T, scenario reliabilityCommandScenario) (string, *paper.PostgresStore, paper.CreatePaperImportInput, paper.PaperImportJob) {
	t.Helper()
	db, tenantID, userID, schoolID, first := scenario.DB, scenario.TenantID, scenario.UserID, scenario.SchoolID, scenario.First
	var err error
	fileID := uuid.NewString()
	if _, err = db.Exec(`INSERT INTO file_asset(id,tenant_id,school_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'import',$4::uuid,'paper.pdf','application/pdf',128,$5,'tests','paper.pdf','private',$6::uuid)`, fileID, tenantID, schoolID, first.Exams[0].ID, uuid.NewString()+uuid.NewString(), userID); err != nil {
		t.Fatalf("seed paper file: %v", err)
	}
	paperStore := paper.NewPostgresStore(db)
	createImport := paper.CreatePaperImportInput{CommandID: "paper-create-" + uuid.NewString(), Subject: "math", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: fileID, DocumentIndex: 0, RoleHint: "question"}}}
	type importCreation struct {
		job paper.PaperImportJob
		err error
	}
	importStart := make(chan struct{})
	importResults := make(chan importCreation, 2)
	for range 2 {
		go func() {
			<-importStart
			created, createErr := paperStore.CreatePaperImport(context.Background(), tenantID, first.Exams[0].ID, userID, createImport)
			importResults <- importCreation{created, createErr}
		}()
	}
	close(importStart)
	importLeft, importRight := <-importResults, <-importResults
	if importLeft.err != nil || importRight.err != nil || importLeft.job.ID != importRight.job.ID || importLeft.job.RunID != importRight.job.RunID {
		t.Fatalf("concurrent initial command created inconsistent imports: %#v %#v", importLeft, importRight)
	}
	job := importLeft.job
	replayed, err := paperStore.CreatePaperImport(context.Background(), tenantID, first.Exams[0].ID, userID, createImport)
	if err != nil || replayed.ID != job.ID || replayed.Generation != 1 || replayed.RunID != job.RunID {
		t.Fatalf("import replay=%#v err=%v", replayed, err)
	}
	changedImport := createImport
	changedImport.Sources = append([]paper.CreatePaperImportSourceInput(nil), createImport.Sources...)
	changedImport.Sources[0].RoleHint = "answer"
	if _, err = paperStore.CreatePaperImport(context.Background(), tenantID, first.Exams[0].ID, userID, changedImport); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("changed import command replay error=%v", err)
	}
	return fileID, paperStore, createImport, job
}

// 唯一运行持有可恢复准备意图，领取加载该运行冻结来源及代次。
func e2eAssertImportDurableDispatchIntent(t *testing.T, scenario reliabilityCommandScenario) paper.PaperImportJob {
	t.Helper()
	db, tenantID, paperStore, job := scenario.DB, scenario.TenantID, scenario.Paper, scenario.Job
	var err error
	var runCount, recoverableIntent int
	if err = db.QueryRow(`SELECT count(*),count(*) FILTER (WHERE status='processing' AND dispatch_status='pending') FROM paper_import_run WHERE tenant_id=$1::uuid AND paper_import_id=$2::uuid`, tenantID, job.ID).Scan(&runCount, &recoverableIntent); err != nil {
		t.Fatal(err)
	}
	if runCount != 1 || recoverableIntent != 1 {
		t.Fatalf("initial import lacks a unique durable dispatch intent: runs=%d intents=%d", runCount, recoverableIntent)
	}
	claimedDispatch, dispatchOK, err := paperStore.ClaimPendingPaperImportDispatch(context.Background(), "initial-dispatch", time.Minute)
	if err != nil || !dispatchOK || claimedDispatch.RunID != job.RunID || claimedDispatch.Generation != job.Generation || len(claimedDispatch.Sources) != len(job.Sources) {
		t.Fatalf("dispatch did not load its immutable run: %#v ok=%v err=%v", claimedDispatch, dispatchOK, err)
	}

	return claimedDispatch
}

// 原任务绑定已接受运行并领取后才取消，后续旧回调保留这一租约身份。
func e2eCreateAndCancelImportRuntime(t *testing.T, scenario reliabilityCommandScenario) []workerruntime.Task {
	t.Helper()
	db, tenantID, userID, paperStore, job, runtimeStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Paper, scenario.Job, scenario.Runtime
	var err error
	oldTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, workerruntime.CreateTaskInput{TaskType: "layout", QueueName: "page-processing", SourceType: "paper_import_job", SourceID: job.ID, Priority: 10, PayloadSchemaVersion: "paper-import-decode-v3", IdempotencyKey: "old-run-" + uuid.NewString(), MaxAttempts: 3, RetryBackoffSeconds: 1})
	if err != nil {
		t.Fatalf("create old task: %v", err)
	}
	if _, err = db.Exec(`UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4,task_protocol_version=2 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, oldTask.ID, job.RunID, job.Generation); err != nil {
		t.Fatal(err)
	}
	claimed, err := runtimeStore.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "test", WorkerInstanceID: "old-worker", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim old task=%#v err=%v", claimed, err)
	}
	if _, err = paperStore.CancelPaperImportGeneration(context.Background(), tenantID, job.ID, job.Generation); err != nil {
		t.Fatalf("cancel generation 1: %v", err)
	}
	return claimed
}

// 新运行插入失败整体回滚原作业，移除注入后才允许正常重试。
func e2eAssertSuccessorRunRollback(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, paperStore, job := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Paper, scenario.Job
	var err error
	if _, err = db.Exec(`CREATE FUNCTION fail_test_paper_run_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected run insert failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER fail_test_paper_run_insert BEFORE INSERT ON paper_import_run FOR EACH ROW EXECUTE FUNCTION fail_test_paper_run_insert()`); err != nil {
		t.Fatal(err)
	}
	failedRunInput := paper.ReplacePaperImportSourcesInput{CommandID: "paper-failed-run-" + uuid.NewString(), ExpectedGeneration: job.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}}}
	if _, err = paperStore.ReplacePaperImportSources(context.Background(), tenantID, job.ID, userID, failedRunInput); err == nil {
		t.Fatal("injected run insert failure unexpectedly committed")
	}
	var generationAfterRunRollback int64
	var statusAfterRunRollback string
	if err = db.QueryRow(`SELECT current_generation,status FROM paper_import_job WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, job.ID).Scan(&generationAfterRunRollback, &statusAfterRunRollback); err != nil {
		t.Fatal(err)
	}
	if generationAfterRunRollback != job.Generation || statusAfterRunRollback != "cancelled" {
		t.Fatalf("failed run creation polluted current job: generation=%d status=%s", generationAfterRunRollback, statusAfterRunRollback)
	}
	if _, err = db.Exec(`DROP TRIGGER fail_test_paper_run_insert ON paper_import_run`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP FUNCTION fail_test_paper_run_insert()`); err != nil {
		t.Fatal(err)
	}
}

// 新代次接受后旧准备失败及旧解码回调都拒绝，当前作业不能被旧结果污染。
func e2eAssertSuccessorImportAndStaleCallback(t *testing.T, scenario reliabilityCommandScenario) paper.PaperImportJob {
	t.Helper()
	tenantID, userID, fileID, paperStore, job, claimedDispatch, claimed := scenario.TenantID, scenario.UserID, scenario.FileID, scenario.Paper, scenario.Job, scenario.Dispatch, scenario.OldClaim
	var err error
	restarted, err := paperStore.ReplacePaperImportSources(context.Background(), tenantID, job.ID, userID, paper.ReplacePaperImportSourcesInput{CommandID: "paper-rerun-" + uuid.NewString(), ExpectedGeneration: job.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}}})
	if err != nil {
		t.Fatalf("rerun same source: %v", err)
	}
	if restarted.Generation != 2 || restarted.RunID == job.RunID || restarted.Status != "processing" {
		t.Fatalf("rerun identity=%#v", restarted)
	}
	if err := paperStore.FailPendingPaperImportDispatch(context.Background(), claimedDispatch, "initial-dispatch", "old source download failed"); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("stale preparation failure accepted: %v", err)
	}
	err = paperStore.CompletePaperImportDecode(context.Background(), tenantID, job.ID, paper.PaperImportDecodeResult{TaskID: claimed[0].ID, LeaseToken: claimed[0].LeaseToken, DurationMS: 1, Pages: []paper.PaperImportDecodedPage{{SourceID: job.Sources[0].ID, DocumentIndex: 0, PageNo: 1, FileAssetID: fileID}}})
	if !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("old generation callback error=%v", err)
	}
	current, err := paperStore.GetPaperImport(context.Background(), tenantID, job.ID)
	if err != nil || current.Generation != 2 || current.ResultGeneration != 0 || current.Status != "processing" {
		t.Fatalf("old callback polluted current import: %#v err=%v", current, err)
	}
	return current
}

// 协议一及未绑定代次的结果均拒绝，当前代次、状态和候选保持原值。
func e2eAssertLegacyImportCallbacksRejected(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	tenantID, userID, fileID, paperStore, job, runtimeStore, current := scenario.TenantID, scenario.UserID, scenario.FileID, scenario.Paper, scenario.Job, scenario.Runtime, scenario.Current
	var err error
	legacyTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, workerruntime.CreateTaskInput{TaskType: "layout", QueueName: "page-processing", SourceType: "paper_import_job", SourceID: job.ID, Priority: 10, PayloadSchemaVersion: "paper-import-decode-v1", IdempotencyKey: "legacy-protocol-" + uuid.NewString(), MaxAttempts: 1})
	if err != nil {
		t.Fatalf("create legacy protocol task: %v", err)
	}
	legacyClaim, err := runtimeStore.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "legacy-test", WorkerInstanceID: "legacy-worker", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(legacyClaim) != 1 || legacyClaim[0].ID != legacyTask.ID {
		t.Fatalf("claim legacy protocol task=%#v err=%v", legacyClaim, err)
	}
	err = paperStore.CompletePaperImportDecode(context.Background(), tenantID, job.ID, paper.PaperImportDecodeResult{TaskID: legacyClaim[0].ID, LeaseToken: legacyClaim[0].LeaseToken, DurationMS: 1, Pages: []paper.PaperImportDecodedPage{{SourceID: current.Sources[0].ID, DocumentIndex: 0, PageNo: 1, FileAssetID: fileID}}})
	if !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("protocol-v1 callback error=%v", err)
	}
	if _, err = paperStore.CompletePaperImportCandidates(context.Background(), tenantID, job.ID, paper.PaperImportParseResult{QuestionCandidates: []paper.QuestionCandidate{{CandidateID: "legacy-unversioned-result"}}}); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("legacy unversioned candidate write error=%v", err)
	}
	afterLegacyWrite, err := paperStore.GetPaperImport(context.Background(), tenantID, job.ID)
	if err != nil || afterLegacyWrite.Generation != current.Generation || afterLegacyWrite.Status != "processing" || len(afterLegacyWrite.QuestionCandidates) != 0 {
		t.Fatalf("legacy unversioned write polluted current import: %#v err=%v", afterLegacyWrite, err)
	}
}

// 失败任务绑定当前运行并领取，终态提交使用该任务的原租约。
func e2eCreateImportFailureRuntime(t *testing.T, scenario reliabilityCommandScenario) (workerruntime.Task, []workerruntime.Task) {
	t.Helper()
	db, tenantID, userID, job, runtimeStore, current := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Job, scenario.Runtime, scenario.Current
	var err error
	failureTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, workerruntime.CreateTaskInput{TaskType: "layout", QueueName: "page-processing", SourceType: "paper_import_job", SourceID: job.ID, Priority: 20, PayloadSchemaVersion: "paper-import-decode-v3", IdempotencyKey: "failure-atomicity-" + uuid.NewString(), MaxAttempts: 1})
	if err != nil {
		t.Fatalf("create failure atomicity task: %v", err)
	}
	if _, err = db.Exec(`UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4,task_protocol_version=2 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, failureTask.ID, current.RunID, current.Generation); err != nil {
		t.Fatal(err)
	}
	failureClaim, err := runtimeStore.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "failure-test", WorkerInstanceID: "failure-worker", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(failureClaim) != 1 || failureClaim[0].ID != failureTask.ID {
		t.Fatalf("claim failure atomicity task=%#v err=%v", failureClaim, err)
	}
	return failureTask, failureClaim
}

// 终态故障注入同时回滚任务和作业，删除注入后按同一失败输入提交。
func e2eAssertImportFailureAtomicity(t *testing.T, scenario reliabilityCommandScenario) paper.PaperImportJob {
	t.Helper()
	db, tenantID, paperStore, job, failureTask, failureClaim := scenario.DB, scenario.TenantID, scenario.Paper, scenario.Job, scenario.FailureTask, scenario.FailureClaim
	var err error
	if _, err = db.Exec(`CREATE FUNCTION fail_test_paper_runtime_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.status='processing' AND NEW.status='failed' THEN RAISE EXCEPTION 'injected runtime failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TRIGGER fail_test_paper_runtime_failure BEFORE UPDATE ON paper_import_job FOR EACH ROW EXECUTE FUNCTION fail_test_paper_runtime_failure()`); err != nil {
		t.Fatal(err)
	}
	failureInput := paper.PaperImportRuntimeFailure{TaskID: failureTask.ID, LeaseToken: failureClaim[0].LeaseToken, Retryable: false, ErrorCode: "page_processing_failed", ErrorDetail: map[string]any{"injected": true}}
	if err = paperStore.FailPaperImportRuntime(context.Background(), tenantID, job.ID, failureInput); err == nil {
		t.Fatal("injected runtime failure unexpectedly committed")
	}
	var rolledBackTaskStatus, rolledBackJobStatus string
	if err = db.QueryRow(`SELECT task.status,job.status FROM agent_worker_task task CROSS JOIN paper_import_job job WHERE task.tenant_id=$1::uuid AND task.id=$2::uuid AND job.tenant_id=$1::uuid AND job.id=$3::uuid`, tenantID, failureTask.ID, job.ID).Scan(&rolledBackTaskStatus, &rolledBackJobStatus); err != nil {
		t.Fatal(err)
	}
	if rolledBackTaskStatus != "leased" || rolledBackJobStatus != "processing" {
		t.Fatalf("runtime failure was partially committed: task=%s job=%s", rolledBackTaskStatus, rolledBackJobStatus)
	}
	if _, err = db.Exec(`DROP TRIGGER fail_test_paper_runtime_failure ON paper_import_job`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`DROP FUNCTION fail_test_paper_runtime_failure()`); err != nil {
		t.Fatal(err)
	}
	if err = paperStore.FailPaperImportRuntime(context.Background(), tenantID, job.ID, failureInput); err != nil {
		t.Fatalf("commit runtime terminal failure after rollback: %v", err)
	}
	failedJob, err := paperStore.GetPaperImport(context.Background(), tenantID, job.ID)
	if err != nil || failedJob.Status != "failed" || failedJob.Generation != 2 {
		t.Fatalf("terminal failure state=%#v err=%v", failedJob, err)
	}
	return failedJob
}

// 终态重试创建第三代，准确命令重放同一新运行而不会复用失败运行。
func e2eAssertFailedImportRetryIdentity(t *testing.T, scenario reliabilityCommandScenario) paper.PaperImportJob {
	t.Helper()
	tenantID, userID, paperStore, job, failedJob := scenario.TenantID, scenario.UserID, scenario.Paper, scenario.Job, scenario.FailedJob
	retryCommand := "paper-retry-after-failure-" + uuid.NewString()
	retryInput := paper.ReplacePaperImportSourcesInput{CommandID: retryCommand, ExpectedGeneration: failedJob.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: failedJob.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}}}
	retried, err := paperStore.ReplacePaperImportSources(context.Background(), tenantID, job.ID, userID, retryInput)
	if err != nil {
		t.Fatalf("retry terminal import: %v", err)
	}
	retriedReplay, err := paperStore.ReplacePaperImportSources(context.Background(), tenantID, job.ID, userID, retryInput)
	if err != nil || retried.Generation != 3 || retried.Status != "processing" || retried.RunID == failedJob.RunID || retriedReplay.RunID != retried.RunID || retriedReplay.Generation != retried.Generation {
		t.Fatalf("terminal retry/replay first=%#v replay=%#v err=%v", retried, retriedReplay, err)
	}
	return retried
}

// 解码完成与取消共享屏障，允许明确冲突但最终取消必须清除活动任务及已发布结果。
func e2eRaceImportCancellationAndCompletion(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, fileID, paperStore, job, runtimeStore, retried := scenario.DB, scenario.TenantID, scenario.UserID, scenario.FileID, scenario.Paper, scenario.Job, scenario.Runtime, scenario.Retried
	var err error
	raceTask, err := runtimeStore.CreateTask(context.Background(), tenantID, userID, workerruntime.CreateTaskInput{TaskType: "layout", QueueName: "page-processing", SourceType: "paper_import_job", SourceID: job.ID, Priority: 30, PayloadSchemaVersion: "paper-import-decode-v3", IdempotencyKey: "cancel-complete-race-" + uuid.NewString(), MaxAttempts: 1})
	if err != nil {
		t.Fatalf("create cancel/complete race task: %v", err)
	}
	if _, err = db.Exec(`UPDATE agent_worker_task SET paper_import_run_id=$3::uuid,paper_import_generation=$4,task_protocol_version=2 WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, raceTask.ID, retried.RunID, retried.Generation); err != nil {
		t.Fatal(err)
	}
	raceClaim, err := runtimeStore.Claim(context.Background(), tenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "race-test", WorkerInstanceID: "race-worker", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(raceClaim) != 1 || raceClaim[0].ID != raceTask.ID {
		t.Fatalf("claim cancel/complete race task=%#v err=%v", raceClaim, err)
	}
	raceStart := make(chan struct{})
	raceResults := make(chan error, 2)
	go func() {
		<-raceStart
		raceResults <- paperStore.CompletePaperImportDecode(context.Background(), tenantID, job.ID, paper.PaperImportDecodeResult{TaskID: raceClaim[0].ID, LeaseToken: raceClaim[0].LeaseToken, DurationMS: 1, Pages: []paper.PaperImportDecodedPage{{SourceID: retried.Sources[0].ID, DocumentIndex: 0, PageNo: 1, FileAssetID: fileID}}})
	}()
	go func() {
		<-raceStart
		_, cancelErr := paperStore.CancelPaperImportGeneration(context.Background(), tenantID, job.ID, retried.Generation)
		raceResults <- cancelErr
	}()
	close(raceStart)
	for range 2 {
		raceErr := <-raceResults
		if raceErr != nil && !errors.Is(raceErr, paper.ErrConflict) {
			t.Fatalf("cancel/complete race returned an invalid outcome: %v", raceErr)
		}
	}
	var finalRaceStatus string
	var activeRaceTasks, publishedRaceResult int
	if err = db.QueryRow(`SELECT job.status,
(SELECT count(*) FROM agent_worker_task task WHERE task.tenant_id=job.tenant_id AND task.paper_import_run_id=$3::uuid AND task.status IN ('queued','leased','running')),
CASE WHEN job.result_generation=$4 THEN 1 ELSE 0 END
FROM paper_import_job job WHERE job.tenant_id=$1::uuid AND job.id=$2::uuid`, tenantID, job.ID, retried.RunID, retried.Generation).Scan(&finalRaceStatus, &activeRaceTasks, &publishedRaceResult); err != nil {
		t.Fatal(err)
	}
	if finalRaceStatus != "cancelled" || activeRaceTasks != 0 || publishedRaceResult != 0 {
		t.Fatalf("cancel/complete race left a partial state: status=%s active=%d published=%d", finalRaceStatus, activeRaceTasks, publishedRaceResult)
	}

}

package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

// 准备失败按原三次尝试退避，首轮过期回调拒绝，最终耗尽后不再领取。
func e2eAssertPreparationRetryExhaustion(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, first, paperStore, createImport := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.Paper, scenario.ImportInput
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_available_at=now()+interval '1 day' WHERE status='processing'`); err != nil {
		t.Fatal(err)
	}
	service := paper.NewDocumentImportService(paperStore, nil, nil, "", "", time.Second)
	input := createImport
	input.CommandID = "dispatch-recovery-" + uuid.NewString()
	prepared, err := service.Start(ctx, tenantID, first.Exams[0].ID, userID, input)
	if err != nil || prepared.Status != "processing" {
		t.Fatalf("accept must persist intent without performing file I/O: %#v %v", prepared, err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		e2eFailPreparationAttempt(t, scenario, ctx, prepared, attempt)
	}
	if _, ok, err := paperStore.ClaimPendingPaperImportDispatch(ctx, "after-exhaustion", time.Minute); err != nil || ok {
		t.Fatalf("exhausted preparation reclaimed: %v %v", ok, err)
	}

}

// 每次领取后先处理首轮过期边界，再失败并核对同代次退避或第三次耗尽。
func e2eFailPreparationAttempt(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, prepared paper.PaperImportJob, attempt int) {
	t.Helper()
	db, paperStore := scenario.DB, scenario.Paper
	owner := "dispatch-" + uuid.NewString()
	claimed, ok, err := paperStore.ClaimPendingPaperImportDispatch(ctx, owner, time.Minute)
	if err != nil || !ok || claimed.RunID != prepared.RunID {
		t.Fatalf("claim %d: %#v %v %v", attempt, claimed, ok, err)
	}
	if attempt == 1 {
		e2eAssertExpiredPreparationCallbacks(t, scenario, ctx, prepared, claimed, owner)
	}
	if err := paperStore.FailPendingPaperImportDispatch(ctx, claimed, owner, "download unavailable"); err != nil {
		t.Fatal(err)
	}
	var jobStatus, runStatus, dispatchStatus string
	var count int
	var generation int64
	var backedOff bool
	if err := db.QueryRow(`SELECT j.status,r.status,r.dispatch_status,r.dispatch_attempt_count,j.current_generation,r.dispatch_available_at>now() FROM paper_import_job j JOIN paper_import_run r ON r.id=$2::uuid WHERE j.id=$1::uuid`, prepared.ID, prepared.RunID).Scan(&jobStatus, &runStatus, &dispatchStatus, &count, &generation, &backedOff); err != nil {
		t.Fatal(err)
	}
	want := "processing"
	if attempt == 3 {
		want = "failed"
	}
	if jobStatus != want || runStatus != want || dispatchStatus != "failed" || count != attempt || generation != prepared.Generation || !backedOff {
		t.Fatalf("attempt %d: job=%s run=%s dispatch=%s count=%d generation=%d backoff=%v", attempt, jobStatus, runStatus, dispatchStatus, count, generation, backedOff)
	}
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_available_at=now() WHERE id=$1::uuid`, prepared.RunID); err != nil {
		t.Fatal(err)
	}

}

// 首轮先让准备租约过期，失败和调度回调都拒绝；延长同一租约后再执行原正常失败路径。
func e2eAssertExpiredPreparationCallbacks(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, prepared, claimed paper.PaperImportJob, owner string) {
	t.Helper()
	db, tenantID, userID, paperStore := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Paper
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_lease_expires_at=now()-interval '1 second' WHERE id=$1::uuid`, prepared.RunID); err != nil {
		t.Fatal(err)
	}
	if err := paperStore.FailPendingPaperImportDispatch(ctx, claimed, owner, "expired"); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("expired preparation failure: %v", err)
	}
	assets := []paper.PaperImportOCRAsset{{SourceID: claimed.Sources[0].ID, DocumentIndex: claimed.Sources[0].DocumentIndex, RoleHint: claimed.Sources[0].RoleHint, FileAssetID: claimed.Sources[0].FileAssetID, ContentType: "application/pdf"}}
	if err := paperStore.QueuePaperImportOCR(ctx, tenantID, claimed, userID, assets); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("expired preparation scheduled a task: %v", err)
	}
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_lease_expires_at=now()+interval '1 minute' WHERE id=$1::uuid`, prepared.RunID); err != nil {
		t.Fatal(err)
	}

}

// 崩溃后的准备租约逐次恢复；最终故障注入必须回滚再由新存储完成耗尽。
func e2eAssertAbandonedPreparationRecovery(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, first, paperStore, createImport := scenario.DB, scenario.TenantID, scenario.UserID, scenario.First, scenario.Paper, scenario.ImportInput
	var err error
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_available_at=now()+interval '1 day' WHERE status='processing'`); err != nil {
		t.Fatal(err)
	}
	service := paper.NewDocumentImportService(paperStore, nil, nil, "", "", time.Second)
	input := createImport
	input.CommandID = "abandoned-dispatch-" + uuid.NewString()
	prepared, err := service.Start(ctx, tenantID, first.Exams[0].ID, userID, input)
	if err != nil {
		t.Fatal(err)
	}
	var lastClaim paper.PaperImportJob
	var lastOwner string
	for attempt := 1; attempt <= 3; attempt++ {
		lastClaim, lastOwner = e2eRecoverAbandonedPreparationAttempt(t, scenario, ctx, prepared, attempt)
	}
	var jobStatus, runStatus, dispatchStatus, jobCode, runCode, leaseOwner, sourceStatus string
	var attempts int
	if err := db.QueryRow(`SELECT j.status,r.status,r.dispatch_status,COALESCE(j.error_code,''),COALESCE(r.error_code,''),COALESCE(r.dispatch_lease_owner,''),r.dispatch_attempt_count,s.processing_status
FROM paper_import_job j JOIN paper_import_run r ON r.id=$2::uuid JOIN paper_import_source s ON s.paper_import_id=j.id AND s.deleted_at IS NULL
WHERE j.id=$1::uuid LIMIT 1`, prepared.ID, prepared.RunID).Scan(&jobStatus, &runStatus, &dispatchStatus, &jobCode, &runCode, &leaseOwner, &attempts, &sourceStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "failed" || runStatus != "failed" || dispatchStatus != "failed" || jobCode != "paper_import_dispatch_exhausted" || runCode != jobCode || leaseOwner != "" || attempts != 3 || sourceStatus != "failed" {
		t.Fatalf("abandoned terminal state: job=%s run=%s dispatch=%s codes=%s/%s owner=%s attempts=%d source=%s", jobStatus, runStatus, dispatchStatus, jobCode, runCode, leaseOwner, attempts, sourceStatus)
	}
	if _, ok, err := paperStore.ClaimPendingPaperImportDispatch(ctx, "fourth-attempt", time.Minute); err != nil || ok {
		t.Fatalf("exhausted abandoned dispatch reclaimed: ok=%v err=%v", ok, err)
	}
	if err := paperStore.FailPendingPaperImportDispatch(ctx, lastClaim, lastOwner, "very late callback"); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("terminal run accepted late owner: %v", err)
	}

}

// 模拟每次准备持有者崩溃，先核对最终回滚再恢复，并拒绝原持有者的迟到回调。
func e2eRecoverAbandonedPreparationAttempt(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, prepared paper.PaperImportJob, attempt int) (paper.PaperImportJob, string) {
	t.Helper()
	db, paperStore := scenario.DB, scenario.Paper
	var err error
	owner := "crashed-dispatch-" + uuid.NewString()
	claimed, ok, err := paperStore.ClaimPendingPaperImportDispatch(ctx, owner, time.Minute)
	if err != nil || !ok || claimed.RunID != prepared.RunID {
		t.Fatalf("abandoned claim %d: %#v ok=%v err=%v", attempt, claimed, ok, err)
	}
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_lease_expires_at=now()-interval '1 second' WHERE id=$1::uuid`, prepared.RunID); err != nil {
		t.Fatal(err)
	}
	if attempt == 3 {
		e2eAssertPreparationExhaustionRollback(t, scenario, ctx, prepared, owner)
	}
	fresh := paper.NewPostgresStore(db)
	if repaired, err := fresh.ReconcileExpiredPaperImportTask(ctx); err != nil || !repaired {
		t.Fatalf("abandoned reconcile %d: repaired=%v err=%v", attempt, repaired, err)
	}
	if err := paperStore.FailPendingPaperImportDispatch(ctx, claimed, owner, "late crashed owner callback"); !errors.Is(err, paper.ErrConflict) {
		t.Fatalf("late dispatch owner %d was not fenced: %v", attempt, err)
	}
	if attempt < 3 {
		e2eAssertAbandonedPreparationBackoff(t, scenario, prepared, attempt)
	}

	return claimed, owner
}

// 最终准备耗尽故障不得提前改变作业、运行、持有者或来源状态，撤下注入后才继续恢复。
func e2eAssertPreparationExhaustionRollback(t *testing.T, scenario reliabilityCommandScenario, ctx context.Context, prepared paper.PaperImportJob, owner string) {
	t.Helper()
	db, paperStore := scenario.DB, scenario.Paper
	if _, err := db.Exec(`CREATE FUNCTION reject_dispatch_exhaustion_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.error_code='paper_import_dispatch_exhausted' THEN RAISE EXCEPTION 'injected dispatch exhaustion failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_dispatch_exhaustion_test BEFORE UPDATE ON paper_import_job FOR EACH ROW EXECUTE FUNCTION reject_dispatch_exhaustion_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := paperStore.ReconcileExpiredPaperImportTask(ctx); err == nil {
		t.Fatal("dispatch exhaustion injection did not fail")
	}
	var jobStatus, runStatus, leaseOwner, sourceStatus string
	if err := db.QueryRow(`SELECT j.status,r.status,COALESCE(r.dispatch_lease_owner,''),s.processing_status
FROM paper_import_job j JOIN paper_import_run r ON r.id=$2::uuid JOIN paper_import_source s ON s.paper_import_id=j.id AND s.deleted_at IS NULL
WHERE j.id=$1::uuid LIMIT 1`, prepared.ID, prepared.RunID).Scan(&jobStatus, &runStatus, &leaseOwner, &sourceStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "processing" || runStatus != "processing" || leaseOwner != owner || sourceStatus == "failed" {
		t.Fatalf("partial dispatch exhaustion: job=%s run=%s owner=%s source=%s", jobStatus, runStatus, leaseOwner, sourceStatus)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_dispatch_exhaustion_test ON paper_import_job; DROP FUNCTION reject_dispatch_exhaustion_test()`); err != nil {
		t.Fatal(err)
	}

}

// 前两次恢复仍保留处理状态及原尝试数，退避事实核对后再开放下一次领取。
func e2eAssertAbandonedPreparationBackoff(t *testing.T, scenario reliabilityCommandScenario, prepared paper.PaperImportJob, attempt int) {
	t.Helper()
	db := scenario.DB
	var jobStatus, runStatus, dispatchStatus string
	var attempts int
	var backedOff bool
	if err := db.QueryRow(`SELECT j.status,r.status,r.dispatch_status,r.dispatch_attempt_count,r.dispatch_available_at>now()
FROM paper_import_job j JOIN paper_import_run r ON r.id=$2::uuid WHERE j.id=$1::uuid`, prepared.ID, prepared.RunID).Scan(&jobStatus, &runStatus, &dispatchStatus, &attempts, &backedOff); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "processing" || runStatus != "processing" || dispatchStatus != "failed" || attempts != attempt || !backedOff {
		t.Fatalf("abandoned retry %d: job=%s run=%s dispatch=%s attempts=%d backoff=%v", attempt, jobStatus, runStatus, dispatchStatus, attempts, backedOff)
	}
	if _, err := db.Exec(`UPDATE paper_import_run SET dispatch_available_at=now() WHERE id=$1::uuid`, prepared.RunID); err != nil {
		t.Fatal(err)
	}

}

// 最后活动尝试耗尽必须同时关闭运行；注入失败后新存储恢复且再次恢复无变化。
func e2eAssertFinalAbandonedRuntimeRecovery(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, paperStore := scenario.DB, scenario.Paper
	ctx := context.Background()
	job, taskID := e2eCreateExhaustedImportAttempt(t, scenario, ctx)
	if _, err := db.Exec(`CREATE FUNCTION reject_exhaustion_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.error_code='paper_import_attempts_exhausted' THEN RAISE EXCEPTION 'injected exhaustion failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_exhaustion_test BEFORE UPDATE ON paper_import_job FOR EACH ROW EXECUTE FUNCTION reject_exhaustion_test()`); err != nil {
		t.Fatal(err)
	}
	if _, err := paperStore.ReconcileExpiredPaperImportTask(ctx); err == nil {
		t.Fatal("exhaustion injection did not fail")
	}
	var taskStatus, jobStatus, attemptStatus string
	read := func() {
		t.Helper()
		if err := db.QueryRow(`SELECT t.status,j.status,a.status FROM agent_worker_task t JOIN paper_import_job j ON j.id=$2::uuid JOIN agent_worker_task_attempt a ON a.task_id=t.id AND a.attempt_no=t.attempt_count WHERE t.id=$1::uuid`, taskID, job.ID).Scan(&taskStatus, &jobStatus, &attemptStatus); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if taskStatus != "leased" || jobStatus != "processing" || attemptStatus != "leased" {
		t.Fatalf("partial exhaustion: %s %s %s", taskStatus, jobStatus, attemptStatus)
	}
	if _, err := db.Exec(`DROP TRIGGER reject_exhaustion_test ON paper_import_job; DROP FUNCTION reject_exhaustion_test()`); err != nil {
		t.Fatal(err)
	}
	fresh := paper.NewPostgresStore(db)
	if repaired, err := fresh.ReconcileExpiredPaperImportTask(ctx); err != nil || !repaired {
		t.Fatalf("restart recovery: %v %v", repaired, err)
	}
	read()
	if taskStatus != "dead_letter" || jobStatus != "failed" || attemptStatus != "lease_expired" {
		t.Fatalf("incomplete exhaustion: %s %s %s", taskStatus, jobStatus, attemptStatus)
	}
	if repaired, err := fresh.ReconcileExpiredPaperImportTask(ctx); err != nil || repaired {
		t.Fatalf("duplicate recovery: %v %v", repaired, err)
	}

}

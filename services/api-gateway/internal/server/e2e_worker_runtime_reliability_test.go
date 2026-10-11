package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	ocrpkg "edugrade-enterprise/services/api-gateway/internal/ocr"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"

	"github.com/google/uuid"
)

func TestWorkerRuntimeReliabilityWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping Worker Runtime PostgreSQL reliability tests")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	const tenantID = "00000000-0000-0000-0000-000000000002"
	ctx := context.Background()
	runtimeStore := workerruntime.NewPostgresStore(db)
	scenario := workerRuntimeReliabilityScenario{Context: ctx, DB: db, Runtime: runtimeStore, TenantID: tenantID}

	t.Run("expired final attempt is atomically dead-lettered", func(t *testing.T) {
		e2eAssertExhaustedRuntimeDeadLetter(t, scenario)
	})

	t.Run("transaction primitives share caller commit", func(t *testing.T) {
		e2eAssertRuntimeTransactionOwnership(t, scenario)
	})

	t.Run("expired lease cannot complete or heartbeat while another worker reclaims", func(t *testing.T) {
		e2eAssertRuntimeStaleLeaseRace(t, scenario)
	})

	t.Run("ocr processing start is idempotent", func(t *testing.T) {
		e2eAssertOCRStartIdempotency(t, scenario)
	})
}

type workerRuntimeReliabilityScenario struct {
	Context  context.Context
	DB       *sql.DB
	Runtime  *workerruntime.PostgresStore
	TenantID string
}

// 最终尝试过期后十二个并发领取都不能再租用，原尝试必须原子关闭。
func e2eAssertExhaustedRuntimeDeadLetter(t *testing.T, scenario workerRuntimeReliabilityScenario) {
	t.Helper()
	ctx, runtimeStore, tenantID := scenario.Context, scenario.Runtime, scenario.TenantID
	created := e2eCreateExpiredFinalRuntimeAttempt(t, scenario)
	var claimCount atomic.Int32
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			tasks, claimErr := runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-concurrent-"+string(rune('a'+worker))))
			if claimErr != nil {
				t.Errorf("concurrent claim %d: %v", worker, claimErr)
				return
			}
			claimCount.Add(int32(len(tasks)))
		}(worker)
	}
	wg.Wait()
	if claimCount.Load() != 0 {
		t.Fatalf("expired final attempt must not be re-leased, got %d claims", claimCount.Load())
	}
	stored, err := runtimeStore.Get(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("get runtime task: %v", err)
	}
	if stored.Status != workerruntime.StatusDeadLetter || stored.AttemptCount != 1 || stored.AttemptCount > stored.MaxAttempts {
		t.Fatalf("unexpected exhausted runtime task: %#v", stored)
	}
	if len(stored.Attempts) != 1 || stored.Attempts[0].Status != "lease_expired" || stored.Attempts[0].CompletedAt == nil {
		t.Fatalf("expired attempt was not closed: %#v", stored.Attempts)
	}

}

// 先领取唯一允许的尝试再由数据库令租约过期，后续并发领取共享这一终态前提。
func e2eCreateExpiredFinalRuntimeAttempt(t *testing.T, scenario workerRuntimeReliabilityScenario) workerruntime.Task {
	t.Helper()
	ctx, db, runtimeStore, tenantID := scenario.Context, scenario.DB, scenario.Runtime, scenario.TenantID
	created, err := runtimeStore.CreateTask(ctx, tenantID, "", postgresRuntimeTaskInput(uuid.NewString(), "lease-limit", 1))
	if err != nil {
		t.Fatalf("create runtime task: %v", err)
	}
	claimed, err := runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-first"))
	if err != nil || len(claimed) != 1 || claimed[0].ID != created.ID {
		t.Fatalf("initial claim: %v %#v", err, claimed)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	return created
}

// 可信成功与失败原语均由调用者提交，原事务回滚保护保留到整个场景结束。
func e2eAssertRuntimeTransactionOwnership(t *testing.T, scenario workerRuntimeReliabilityScenario) {
	t.Helper()
	ctx, db := scenario.Context, scenario.DB
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	defer tx.Rollback()
	e2ePersistTrustedRuntimeSuccess(t, scenario, tx)
	e2eCommitRuntimeFailure(t, scenario)
}

// 不同 JSON 键顺序及耗时仍重放同一成功任务，调用者事务提交后才开始失败任务场景。
func e2ePersistTrustedRuntimeSuccess(t *testing.T, scenario workerRuntimeReliabilityScenario, tx *sql.Tx) {
	t.Helper()
	ctx, tenantID := scenario.Context, scenario.TenantID
	create := postgresRuntimeTaskInput(uuid.NewString(), "trusted-import", 3)
	complete := workerruntime.CompleteInput{ResultSchemaVersion: "import.v1", Result: map[string]any{"b": 2, "a": 1}, DurationMS: 8}
	first, err := workerruntime.CreateSucceededTaskInTx(ctx, tx, tenantID, "", create, complete)
	if err != nil || first.Status != workerruntime.StatusSucceeded || first.AttemptCount != 0 {
		t.Fatalf("trusted create succeeded: %v %#v", err, first)
	}
	second, err := workerruntime.CreateSucceededTaskInTx(ctx, tx, tenantID, "", create, workerruntime.CompleteInput{
		ResultSchemaVersion: "import.v1", Result: map[string]any{"a": 1, "b": 2}, DurationMS: 99,
	})
	if err != nil || second.ID != first.ID {
		t.Fatalf("idempotent trusted create: %v %#v", err, second)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit trusted task: %v", err)
	}

}

// 失败领取沿用其租约，失败原语出错先回滚，成功则由调用者提交终态。
func e2eCommitRuntimeFailure(t *testing.T, scenario workerRuntimeReliabilityScenario) {
	t.Helper()
	ctx, db, runtimeStore, tenantID := scenario.Context, scenario.DB, scenario.Runtime, scenario.TenantID
	failedSource := uuid.NewString()
	failed, err := runtimeStore.CreateTask(ctx, tenantID, "", postgresRuntimeTaskInput(failedSource, "tx-fail", 1))
	if err != nil {
		t.Fatalf("create task to fail: %v", err)
	}
	leased, err := runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-fail"))
	if err != nil || len(leased) != 1 || leased[0].ID != failed.ID {
		t.Fatalf("claim task to fail: %v %#v", err, leased)
	}
	failTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fail transaction: %v", err)
	}
	failed, err = workerruntime.FailTaskInTx(ctx, failTx, tenantID, failed.ID, workerruntime.FailInput{
		LeaseToken: leased[0].LeaseToken, ErrorCode: "terminal_test", ErrorDetail: map[string]any{"source": "test"},
	})
	if err != nil {
		_ = failTx.Rollback()
		t.Fatalf("fail in transaction: %v", err)
	}
	if err := failTx.Commit(); err != nil {
		t.Fatalf("commit failure: %v", err)
	}
	if failed.Status != workerruntime.StatusFailed {
		t.Fatalf("unexpected failed task: %#v", failed)
	}

}

// 旧租约完成和心跳与新领取同时开始，过时写入被隔离，新持有者保留完整尝试历史。
func e2eAssertRuntimeStaleLeaseRace(t *testing.T, scenario workerRuntimeReliabilityScenario) {
	t.Helper()
	created, oldTask := e2eCreateExpiredRuntimeRace(t, scenario)
	e2eRaceStaleRuntimeOperations(t, scenario, created, oldTask)
	e2eAssertReclaimedRuntimeHistory(t, scenario, created)
}

// 建立可重领的过期租约；旧任务身份在全部并发操作中保持一致。
func e2eCreateExpiredRuntimeRace(t *testing.T, scenario workerRuntimeReliabilityScenario) (workerruntime.Task, workerruntime.Task) {
	t.Helper()
	ctx, db, runtimeStore, tenantID := scenario.Context, scenario.DB, scenario.Runtime, scenario.TenantID
	created, err := runtimeStore.CreateTask(ctx, tenantID, "", postgresRuntimeTaskInput(uuid.NewString(), "terminal-race", 3))
	if err != nil {
		t.Fatalf("create runtime task: %v", err)
	}
	claimed, err := runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-old"))
	if err != nil || len(claimed) != 1 || claimed[0].ID != created.ID {
		t.Fatalf("initial claim: %v %#v", err, claimed)
	}
	if _, err := db.ExecContext(ctx, `UPDATE agent_worker_task SET lease_expires_at = transaction_timestamp() - interval '1 second' WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	return created, claimed[0]
}

// 领取、旧完成及旧心跳共用开始屏障；SKIP LOCKED 空结果只在过时操作都返回后重试。
func e2eRaceStaleRuntimeOperations(t *testing.T, scenario workerRuntimeReliabilityScenario, created, oldTask workerruntime.Task) {
	t.Helper()
	ctx, runtimeStore, tenantID := scenario.Context, scenario.Runtime, scenario.TenantID
	var err error
	start := make(chan struct{})
	claimResult := make(chan []workerruntime.Task, 1)
	claimErr := make(chan error, 1)
	completeErr := make(chan error, 1)
	heartbeatErr := make(chan error, 1)
	go func() {
		<-start
		tasks, claimFailure := runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-new"))
		claimResult <- tasks
		claimErr <- claimFailure
	}()
	go func() {
		<-start
		_, completeFailure := runtimeStore.Complete(ctx, tenantID, created.ID, workerruntime.CompleteInput{
			LeaseToken: oldTask.LeaseToken, ResultSchemaVersion: "race.v1",
			Result: map[string]any{"answer": "late"}, DurationMS: 10,
		})
		completeErr <- completeFailure
	}()
	go func() {
		<-start
		_, heartbeatFailure := runtimeStore.Heartbeat(ctx, tenantID, created.ID, workerruntime.HeartbeatInput{
			LeaseToken: oldTask.LeaseToken, WorkerService: "ocr-worker",
			WorkerInstanceID: "worker-old", State: workerruntime.StatusRunning, LeaseSeconds: 300,
		})
		heartbeatErr <- heartbeatFailure
	}()
	close(start)

	if err := <-claimErr; err != nil {
		t.Fatalf("replacement claim: %v", err)
	}
	reclaimed := <-claimResult
	for operation, operationErr := range map[string]error{
		"complete":  <-completeErr,
		"heartbeat": <-heartbeatErr,
	} {
		if operationErr == nil || (!errors.Is(operationErr, workerruntime.ErrLeaseExpired) && !errors.Is(operationErr, workerruntime.ErrLeaseMismatch)) {
			t.Fatalf("stale %s should fail by lease, got %v", operation, operationErr)
		}
	}
	// Claim 使用 FOR UPDATE SKIP LOCKED。若过时操作暂时持有行锁，首轮轮询为空是预期结果；
	// 两个过时操作返回并回滚后，下次轮询必须能重新领取。
	if len(reclaimed) == 0 {
		reclaimed, err = runtimeStore.Claim(ctx, tenantID, postgresClaimInput("worker-new"))
		if err != nil {
			t.Fatalf("replacement claim after stale operations: %v", err)
		}
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != created.ID || reclaimed[0].LeaseToken == oldTask.LeaseToken {
		t.Fatalf("unexpected replacement claim: %#v", reclaimed)
	}

}

// 重领仍处于 leased 且第二次尝试，旧完成不能留下结果，历史保留原过期尝试。
func e2eAssertReclaimedRuntimeHistory(t *testing.T, scenario workerRuntimeReliabilityScenario, created workerruntime.Task) {
	t.Helper()
	ctx, runtimeStore, tenantID := scenario.Context, scenario.Runtime, scenario.TenantID
	stored, err := runtimeStore.Get(ctx, tenantID, created.ID)
	if err != nil {
		t.Fatalf("get reclaimed task: %v", err)
	}
	if stored.Status != workerruntime.StatusLeased || stored.AttemptCount != 2 || stored.ResultSchemaVersion != "" {
		t.Fatalf("stale terminal operation changed reclaimed task: %#v", stored)
	}
	if len(stored.Attempts) != 2 || stored.Attempts[0].Status != "lease_expired" || stored.Attempts[1].Status != workerruntime.StatusLeased {
		t.Fatalf("unexpected attempt history after reclaim race: %#v", stored.Attempts)
	}

}

// 并发恢复必须复用首次开始时间；失败可重试，已完成来源不能重新开始。
func e2eAssertOCRStartIdempotency(t *testing.T, scenario workerRuntimeReliabilityScenario) {
	t.Helper()
	ctx, db, tenantID := scenario.Context, scenario.DB, scenario.TenantID
	userID, submissionID := insertPostgresOCRFixture(t, db, tenantID)
	ocrStore := ocrpkg.NewPostgresStore(db)
	task, err := ocrStore.CreateTask(ctx, tenantID, submissionID, userID, ocrpkg.CreateTaskInput{
		Engine: "paddleocr", EngineVersion: "v1", IdempotencyKey: "pg-start-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("create OCR task: %v", err)
	}
	first, err := ocrStore.StartTask(ctx, tenantID, task.ID)
	if err != nil || first.StartedAt == nil {
		t.Fatalf("start OCR task: %v %#v", err, first)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resumed, startErr := ocrStore.StartTask(ctx, tenantID, task.ID)
			if startErr != nil {
				t.Errorf("idempotent PostgreSQL start: %v", startErr)
				return
			}
			if resumed.StartedAt == nil || !resumed.StartedAt.Equal(*first.StartedAt) {
				t.Errorf("idempotent start rewrote started_at: before=%v after=%v", first.StartedAt, resumed.StartedAt)
			}
		}()
	}
	wg.Wait()
	e2eAssertOCRFailureRetryAndTerminalFence(t, scenario, ocrStore, task)
}

// 并发开始结束后才使来源失败再重试，清除失败残留；完成的 OCR 来源则拒绝再次开始。
func e2eAssertOCRFailureRetryAndTerminalFence(t *testing.T, scenario workerRuntimeReliabilityScenario, ocrStore *ocrpkg.PostgresStore, task ocrpkg.Task) {
	t.Helper()
	ctx, db, tenantID := scenario.Context, scenario.DB, scenario.TenantID
	if _, err := ocrStore.FailTask(ctx, tenantID, task.ID, "lease expired"); err != nil {
		t.Fatalf("fail OCR source: %v", err)
	}
	retried, err := ocrStore.StartTask(ctx, tenantID, task.ID)
	if err != nil || retried.Status != "processing" || retried.CompletedAt != nil || retried.ErrorMessage != "" {
		t.Fatalf("restart failed OCR source: %v %#v", err, retried)
	}
	if _, err := db.ExecContext(ctx, `UPDATE ocr_task SET status = 'completed', completed_at = now() WHERE id = $1`, task.ID); err != nil {
		t.Fatalf("mark OCR source completed: %v", err)
	}
	if _, err := ocrStore.StartTask(ctx, tenantID, task.ID); !errors.Is(err, ocrpkg.ErrInvalidTransition) {
		t.Fatalf("completed OCR source must not restart, got %v", err)
	}

}

func postgresRuntimeTaskInput(sourceID string, key string, maxAttempts int) workerruntime.CreateTaskInput {
	return workerruntime.CreateTaskInput{
		TaskType: "ocr", QueueName: "ocr", SourceType: "ocr_task", SourceID: sourceID,
		Payload: map[string]any{"source_id": sourceID}, PayloadSchemaVersion: "ocr.v1",
		IdempotencyKey: key + ":" + sourceID, MaxAttempts: maxAttempts, RetryBackoffSeconds: 1,
	}
}

func postgresClaimInput(worker string) workerruntime.ClaimInput {
	return workerruntime.ClaimInput{
		QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: worker, Limit: 1, LeaseSeconds: 300,
	}
}

func insertPostgresOCRFixture(t *testing.T, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, tenantID string) (string, string) {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM app_user WHERE tenant_id = $1 ORDER BY created_at LIMIT 1`, tenantID).Scan(&userID); err != nil {
		t.Fatalf("lookup fixture user: %v", err)
	}
	schoolID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `INSERT INTO school (id, tenant_id, name, code, status) VALUES ($1, $2, 'Runtime test school', $3, 'active')`, schoolID, tenantID, "runtime-"+schoolID); err != nil {
		t.Fatalf("insert fixture school: %v", err)
	}
	examID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `
INSERT INTO exam (id, tenant_id, school_id, name, subject, exam_type, total_score, status, grading_mode, publish_policy, created_by)
VALUES ($1, $2, $3, 'Runtime OCR test', 'math', 'mock', 100, 'draft', 'ai_assisted', 'manual', $4)
`, examID, tenantID, schoolID, userID); err != nil {
		t.Fatalf("insert fixture exam: %v", err)
	}
	submissionID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `
INSERT INTO submission (id, tenant_id, exam_id, source_type, status, collected_by)
VALUES ($1, $2, $3, 'scanner_upload', 'ready_for_ocr', $4)
`, submissionID, tenantID, examID, userID); err != nil {
		t.Fatalf("insert fixture submission: %v", err)
	}
	return userID, submissionID
}

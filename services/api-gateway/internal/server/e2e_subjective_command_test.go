package server

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"errors"
	"github.com/google/uuid"
	"os"
	"testing"
	"time"
)

func TestSubjectiveCommandRecoveryWithPostgresTestDatabase(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix, story056AcceptanceAnswerCount)
	e2eSeedStory056AcceptanceAnswers(t, db, fixture, suffix)
	ctx := context.Background()
	store := subjective.NewPostgresStore(db)
	var segmentID, questionID string
	if err := db.QueryRow(`SELECT seg.id::text,seg.question_id::text FROM answer_segment seg JOIN submission sub ON sub.id=seg.submission_id WHERE sub.exam_id=$1::uuid LIMIT 1`, fixture.ExamID).Scan(&segmentID, &questionID); err != nil {
		t.Fatal(err)
	}
	input := subjective.CreateBatchInput{IdempotencyKey: "subjective-" + suffix, SegmentIDs: []string{segmentID}}
	batch := e2eCreateConcurrentSubjectiveBatch(t, ctx, store, fixture, input)
	plan := subjective.BatchEnqueuePlan{CommandID: "enqueue:" + batch.ID, BatchID: batch.ID, Runs: []subjective.CreateRunInput{{BatchID: batch.ID, AnswerSegmentID: segmentID, QuestionID: questionID, AnswerVersion: "v1", RubricVersion: "v1", ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8, RequestID: "subjective-frozen-" + suffix}}}
	saved, err := store.SaveEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, plan)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, saved.Runs[0])
	if err != nil {
		t.Fatal(err)
	}
	// 模拟创建运行之后、创建其 worker 任务之前发生中断。
	restarted := subjective.NewPostgresStore(db)
	plan.Runs = append([]subjective.CreateRunInput(nil), plan.Runs...)
	plan.Runs[0].RequestID = "changed-request"
	plan.Runs[0].ModelVersion = "model-v2"
	replay, err := restarted.SaveEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, plan)
	if err != nil || replay.Runs[0] != saved.Runs[0] {
		t.Fatalf("snapshot replay: %+v %v", replay, err)
	}
	repeated, err := restarted.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, replay.Runs[0])
	if err != nil || repeated.ID != run.ID {
		t.Fatalf("run replay: %+v %v", repeated, err)
	}
	runtime := workerruntime.NewPostgresStore(db)
	taskInput := workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: run.ID, Payload: map[string]any{"run_id": run.ID}, PayloadSchemaVersion: "subjective-grade-v1", IdempotencyKey: saved.Runs[0].RequestID, DedupeKey: "subjective:" + batch.ID + ":" + segmentID, MaxAttempts: 3, RetryBackoffSeconds: 30}
	task, err := runtime.CreateTask(ctx, fixture.TenantID, fixture.AdminID, taskInput)
	if err != nil {
		t.Fatal(err)
	}
	again, err := runtime.CreateTask(ctx, fixture.TenantID, fixture.AdminID, taskInput)
	if err != nil || again.ID != task.ID {
		t.Fatalf("task replay: %+v %v", again, err)
	}
	e2eCompleteAndRecoverSubjectiveCommand(t, ctx, db, store, restarted, fixture, subjectiveCommandIdentity{BatchID: batch.ID, IdempotencyKey: input.IdempotencyKey})
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM subjective_grading_run WHERE tenant_id=$1::uuid AND batch_id=$2::uuid`, fixture.TenantID, batch.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("run count %d: %v", count, err)
	}
}

// 两个并发创建请求先同时放行，再等待两个结果；相同请求共享批次，改变答案集合必须冲突。
func e2eCreateConcurrentSubjectiveBatch(t *testing.T, ctx context.Context, store *subjective.PostgresStore, fixture story056AcceptanceFixture, input subjective.CreateBatchInput) subjective.GradingBatch {
	t.Helper()
	type outcome struct {
		batch subjective.GradingBatch
		err   error
	}
	gate := make(chan struct{})
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-gate
			b, e := store.CreateBatch(ctx, fixture.TenantID, fixture.AdminID, input)
			results <- outcome{b, e}
		}()
	}
	close(gate)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.batch.ID != b.batch.ID {
		t.Fatalf("concurrent batch: %+v %+v", a, b)
	}
	bad := input
	bad.SegmentIDs = []string{"00000000-0000-0000-0000-000000000001"}
	if _, err := store.CreateBatch(ctx, fixture.TenantID, fixture.AdminID, bad); !errors.Is(err, subjective.ErrIdempotencyConflict) {
		t.Fatalf("changed request: %v", err)
	}
	return a.batch
}

type subjectiveCommandIdentity struct {
	BatchID, IdempotencyKey string
}

// 完成排队后重建存储读取持久回执；删除业务批次仍可恢复原回执，但其他租户或操作者不能取得它。
func e2eCompleteAndRecoverSubjectiveCommand(t *testing.T, ctx context.Context, db *sql.DB, store, restarted *subjective.PostgresStore, fixture story056AcceptanceFixture, identity subjectiveCommandIdentity) {
	t.Helper()
	if err := store.CompleteEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, identity.BatchID); err != nil {
		t.Fatal(err)
	}
	recovered, err := restarted.GetEnqueuePlan(ctx, fixture.TenantID, fixture.AdminID, identity.BatchID)
	if err != nil || !recovered.Completed {
		t.Fatalf("receipt: %+v %v", recovered, err)
	}
	if _, err = db.Exec(`UPDATE subjective_grading_batch SET deleted_at=now() WHERE id=$1::uuid`, identity.BatchID); err != nil {
		t.Fatal(err)
	}
	receipt, err := restarted.RecoverBatchCommand(ctx, fixture.TenantID, fixture.AdminID, identity.IdempotencyKey)
	if err != nil || receipt.Batch == nil || receipt.Batch.ID != identity.BatchID {
		t.Fatalf("deleted batch receipt: %+v %v", receipt, err)
	}
	for name, scopedIdentity := range map[string][2]string{
		"other tenant": {uuid.NewString(), fixture.AdminID},
		"other actor":  {fixture.TenantID, uuid.NewString()},
	} {
		isolated, err := restarted.RecoverBatchCommand(ctx, scopedIdentity[0], scopedIdentity[1], identity.IdempotencyKey)
		if err != nil || isolated.Status != "not_accepted" || isolated.Batch != nil {
			t.Fatalf("%s batch command scope: %+v %v", name, isolated, err)
		}
	}
	if _, err := restarted.GetEnqueuePlan(ctx, fixture.TenantID, uuid.NewString(), identity.BatchID); !errors.Is(err, subjective.ErrNotFound) {
		t.Fatalf("other actor recovered enqueue plan: %v", err)
	}
}

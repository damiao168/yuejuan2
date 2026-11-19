package server

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestPostgresWorkerExhaustionRecoversBusinessSources(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, "exhausted-source", 2)
	t.Run("capture expiration preserves transaction and allows retry", func(t *testing.T) {
		assertExhaustionRecoversCaptureFile(t, db, fixture)
	})
	t.Run("historical subjective dead letter refreshes batch", func(t *testing.T) {
		assertExhaustionRecoversSubjectiveBatch(t, db, fixture)
	})
}

func claimRecoveryTask(t *testing.T, runtime *workerruntime.PostgresStore, tenantID, queue, taskID string) workerruntime.Task {
	t.Helper()
	input := workerruntime.ClaimInput{QueueName: queue, WorkerService: "fixture-worker", WorkerInstanceID: "fixture-worker-1", Limit: 1, LeaseSeconds: 30}
	tasks, err := runtime.Claim(t.Context(), tenantID, input)
	if err != nil || len(tasks) != 1 || tasks[0].ID != taskID {
		t.Fatalf("claim %s: %+v %v", taskID, tasks, err)
	}
	return tasks[0]
}

func expireRecoveryTask(t *testing.T, db *sql.DB, task workerruntime.Task) {
	t.Helper()
	// 用数据库时钟移动夹具租约，测试不等待实际超时，也不经过 Worker 失败回调。
	if _, err := db.ExecContext(t.Context(), `
UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second',max_attempts=attempt_count
WHERE tenant_id=$1 AND id=$2::uuid`, task.TenantID, task.ID); err != nil {
		t.Fatal(err)
	}
}

func assertExhaustionRecoversCaptureFile(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture) {
	t.Helper()
	ctx := t.Context()
	store := capture.NewPostgresStore(db)
	runtime := workerruntime.NewPostgresStore(db)
	batch, err := store.CreateBatch(ctx, fixture.TenantID, fixture.ExamID, fixture.AdminID, capture.CreateBatchInput{
		Name: "synthetic source exhaustion", SourceType: "web_upload", IdempotencyKey: "source-exhaustion",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fileID, taskID string
	if err := db.QueryRowContext(ctx, `
INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,status,idempotency_key,uploaded_by)
VALUES($1,$2::uuid,$3::uuid,'synthetic.pdf','application/pdf',repeat('a',64),1,1,'uploaded','source-exhaustion',$4::uuid)
RETURNING id::text`, fixture.TenantID, batch.ID, fixture.PaperFileAssetID, fixture.AdminID).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.QueueBatch(ctx, fixture.TenantID, batch.ID, fixture.AdminID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM agent_worker_task
WHERE tenant_id=$1 AND source_id=$2::uuid AND source_type='capture_file'`, fixture.TenantID, fileID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	leased := claimRecoveryTask(t, runtime, fixture.TenantID, "page-processing", taskID)
	expireRecoveryTask(t, db, leased)
	tasks, err := runtime.Claim(ctx, fixture.TenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "fixture-worker", WorkerInstanceID: "fixture-next", Limit: 1, LeaseSeconds: 30})
	if err != nil || len(tasks) != 0 {
		t.Fatalf("expired source lease was reissued: %+v %v", tasks, err)
	}
	// 在来源写入阶段失败时，任务、尝试与来源应一起回滚。
	if _, err := db.ExecContext(ctx, `
CREATE FUNCTION reject_capture_exhaustion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'fixture rejected source update'; END $$;
CREATE TRIGGER reject_capture_exhaustion BEFORE UPDATE ON capture_file
FOR EACH ROW WHEN (NEW.status='failed') EXECUTE FUNCTION reject_capture_exhaustion()`); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileWorkerExhaustion(ctx, db, workerruntime.ExhaustedSourceTask{}); err == nil || !strings.Contains(err.Error(), "fixture rejected source update") {
		t.Fatalf("expected injected transactional failure: %v", err)
	}
	before, err := runtime.Get(ctx, fixture.TenantID, taskID)
	file, fileErr := store.GetFile(ctx, fixture.TenantID, fileID)
	if err != nil || fileErr != nil || file.Status != "queued" || before.Status != workerruntime.StatusLeased ||
		len(before.Attempts) != 1 || before.Attempts[0].CompletedAt != nil {
		t.Fatalf("failed recovery changed a fact: file=%+v task=%+v %v %v", file, before, fileErr, err)
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER reject_capture_exhaustion ON capture_file`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := reconcileWorkerExhaustion(ctx, db, workerruntime.ExhaustedSourceTask{}); err != nil {
			t.Fatal(err)
		}
	}
	dead, err := runtime.Get(ctx, fixture.TenantID, taskID)
	file, fileErr = store.GetFile(ctx, fixture.TenantID, fileID)
	if err != nil || fileErr != nil || file.Status != "failed" || file.ErrorCode != "lease_expired" ||
		dead.Status != workerruntime.StatusDeadLetter || dead.LeaseToken != "" || len(dead.Attempts) != 1 || dead.Attempts[0].Status != "lease_expired" {
		t.Fatalf("sources did not settle together: file=%+v task=%+v %v %v", file, dead, fileErr, err)
	}
	summary, err := store.GetCollectionSummary(ctx, fixture.TenantID, fixture.ExamID)
	if err != nil || len(summary.Issues) != 1 || summary.Issues[0].FileID != fileID || summary.Issues[0].Code != "file_failed" {
		t.Fatalf("collection does not show the retryable file failure: %+v %v", summary.Issues, err)
	}
	if _, err := store.QueueBatch(ctx, fixture.TenantID, batch.ID, fixture.AdminID); err != nil {
		t.Fatalf("normal capture retry must work: %v", err)
	}
	retry := claimRecoveryTask(t, runtime, fixture.TenantID, "page-processing", taskID)
	if retry.AttemptCount != 2 || retry.LeaseToken == leased.LeaseToken {
		t.Fatalf("retry did not start with a new lease: %+v", retry)
	}
}

func assertExhaustionRecoversSubjectiveBatch(t *testing.T, db *sql.DB, f story056AcceptanceFixture) {
	t.Helper()
	ctx := t.Context()
	var submissionID, pageID, segmentID, batchID, runID string
	q := f.QuestionIDs["single_choice"]
	for _, step := range []struct {
		query string
		dest  *string
		args  []any
	}{
		{`INSERT INTO submission(tenant_id,exam_id,student_id,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by,identity_status)
VALUES($1,$2::uuid,$3::uuid,'image_upload','pages_uploaded',1,1,'passed',$4::uuid,'matched') RETURNING id::text`,
			&submissionID, []any{f.TenantID, f.ExamID, f.StudentIDs[0], f.AdminID}},
		{`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status)
VALUES($1,$2::uuid,$3::uuid,1,'accepted') RETURNING id::text`, &pageID, []any{f.TenantID, &submissionID, f.PaperFileAssetID}},
		{`INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
SELECT $1,$2,$3,id,question_no,'{"x":0,"y":0,"width":1,"height":1}','configured_answer_area','accepted'
FROM question WHERE tenant_id=$1 AND id=$4 RETURNING id::text`, &segmentID, []any{f.TenantID, &submissionID, &pageID, q}},
		{`INSERT INTO subjective_grading_batch(tenant_id,idempotency_key,status,segment_ids,total_count,queued_count,created_by)
VALUES($1,'exhausted-subjective','processing',jsonb_build_array($2::text),1,1,$3::uuid) RETURNING id::text`, &batchID, []any{f.TenantID, &segmentID, f.AdminID}},
		{`INSERT INTO subjective_grading_run(tenant_id,batch_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status)
VALUES($1,$2,$3,'v1',$4,'v1','v1','v1','exhausted-subjective','queued') RETURNING id::text`, &runID, []any{f.TenantID, &batchID, &segmentID, q}},
	} {
		if err := db.QueryRowContext(ctx, step.query, step.args...).Scan(step.dest); err != nil {
			t.Fatal(err)
		}
	}
	runtime := workerruntime.NewPostgresStore(db)
	task, err := runtime.CreateTask(ctx, f.TenantID, f.AdminID, workerruntime.CreateTaskInput{
		TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: runID,
		PayloadSchemaVersion: "fixture-v1", IdempotencyKey: "exhausted-subjective", MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	leased := claimRecoveryTask(t, runtime, f.TenantID, "subjective-grading", task.ID)
	expireRecoveryTask(t, db, leased)
	// 旧版本已写技术终态，恢复只补业务侧，不重写旧任务及尝试记录。
	if _, err = db.ExecContext(ctx, `
UPDATE agent_worker_task_attempt SET status='lease_expired',error_code='lease_expired',completed_at=now()
WHERE tenant_id=$1 AND task_id=$2::uuid AND completed_at IS NULL`, f.TenantID, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `
UPDATE agent_worker_task SET status='dead_letter',error_code='lease_expired',
  error_detail='{"reason":"max_attempts_exhausted"}',completed_at=now(),lease_expires_at=NULL,lease_token=NULL
WHERE tenant_id=$1 AND id=$2::uuid`, f.TenantID, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileWorkerExhaustion(ctx, db, workerruntime.ExhaustedSourceTask{}); err != nil {
		t.Fatal(err)
	}
	store := subjective.NewPostgresStore(db)
	run, err := store.GetRun(ctx, f.TenantID, runID)
	batch, batchErr := store.GetBatch(ctx, f.TenantID, batchID)
	if err != nil || batchErr != nil || run.Status != subjective.RunFailed || run.ErrorCode != "lease_expired" || run.AttemptCount != 1 ||
		batch.Status != "failed" || batch.FailedCount != 1 || batch.QueuedCount != 0 {
		t.Fatalf("historical source and batch were not reconciled: run=%+v batch=%+v %v %v", run, batch, err, batchErr)
	}
}

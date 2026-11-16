package capture

import (
	"errors"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 回执写入发生在答卷、页面、质检任务和批次计数全部物化之后。
// 任一失败都必须一起回滚；旧租约被接管后也不能写回这批页面。
func TestPostgresCaptureDecodeCompletionRollsBackBeforeReceipt(t *testing.T) {
	ctx, db := captureRecoveryRemediationDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"tenant", "submission_page", "submission_page_quality_run"} {
		exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)")
	}

	tenant, exam, actor, batchID, fileID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status)
VALUES ($1,$2,$3,'synthetic decode callback','web_upload',$4,'uploading')`, batchID, tenant, exam, actor)
	exec(`INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,idempotency_key,uploaded_by,status)
VALUES ($1,$2,$3,$4,'synthetic.pdf','application/pdf',$5,10,'decode-callback',$6,'uploaded')`,
		fileID, tenant, batchID, uuid.NewString(), strings.Repeat("a", 64), actor)
	store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
	queued, err := store.QueueBatch(ctx, tenant, batchID, actor)
	if err != nil || queued.Status != "processing" {
		t.Fatalf("queue fixture: batch=%+v err=%v", queued, err)
	}
	claim := func(instance string) workerruntime.Task {
		t.Helper()
		tasks, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{
			QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: instance, Limit: 1, LeaseSeconds: 300,
		})
		if err != nil || len(tasks) != 1 || tasks[0].SourceID != fileID {
			t.Fatalf("claim decode task: tasks=%+v err=%v", tasks, err)
		}
		return tasks[0]
	}
	task := claim("decode-worker-a")
	input := FileResultInput{TaskID: task.ID, LeaseToken: task.LeaseToken, ResultVersion: "fixture-v1", OriginalPageCount: 2, Pages: []DecodedPageInput{
		{SourceIndex: 1, FileAssetID: uuid.NewString(), SHA256: strings.Repeat("b", 64), Width: 100, Height: 200},
		{SourceIndex: 2, FileAssetID: uuid.NewString(), SHA256: strings.Repeat("c", 64), Width: 100, Height: 200},
	}}
	result := map[string]any{"capture_file_id": fileID, "original_page_count": 2, "pages": input.Pages, "result_version": input.ResultVersion}
	submit := func(input FileResultInput, result map[string]any) (File, error) {
		return store.SubmitFileResultCommand(ctx, tenant, fileID, input, runtime, result)
	}
	assertFacts := func(status string, counts [5]int, batchRevision int) {
		t.Helper()
		file, err := store.GetFile(ctx, tenant, fileID)
		if err != nil || file.Status != status || file.PageCount != counts[2] {
			t.Fatalf("unexpected file state: status=%q pages=%d err=%v", file.Status, file.PageCount, err)
		}
		batch, err := store.GetBatch(ctx, tenant, batchID)
		if err != nil || batch.Revision != batchRevision || batch.PageCount != counts[2] || batch.SubmissionCount != counts[0] {
			t.Fatalf("unexpected batch projection: revision=%d pages=%d submissions=%d err=%v", batch.Revision, batch.PageCount, batch.SubmissionCount, err)
		}
		var got [5]int
		err = db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM submission WHERE tenant_id=$1 AND exam_id=$2),
 (SELECT count(*) FROM submission_page WHERE tenant_id=$1),
 (SELECT count(*) FROM capture_page WHERE tenant_id=$1 AND capture_file_id=$3),
 (SELECT count(*) FROM submission_page_quality_run WHERE tenant_id=$1),
 (SELECT count(*) FROM agent_worker_task WHERE tenant_id=$1 AND source_type='image_quality_run')`,
			tenant, exam, fileID).Scan(&got[0], &got[1], &got[2], &got[3], &got[4])
		if err != nil || got != counts {
			t.Fatalf("submissions, submission pages, capture pages, quality runs and tasks=%v want=%v err=%v", got, counts, err)
		}
	}
	assertFirstAttemptUnchanged := func() {
		t.Helper()
		before, err := runtime.Get(ctx, tenant, task.ID)
		if err != nil || before.Status != workerruntime.StatusLeased || before.Revision != task.Revision ||
			before.LeaseToken != task.LeaseToken || before.CompletedAt != nil || before.ResultPayloadHash != "" ||
			len(before.Attempts) != 1 || before.Attempts[0].Status != workerruntime.StatusLeased || before.Attempts[0].CompletedAt != nil {
			t.Fatalf("failed callback changed worker receipt: task=%+v err=%v", before, err)
		}
	}

	// 在最后一次尝试写入注入错误。触发器先确认前面的采集与任务写入已经发生，避免测成提前拒绝。
	exec(`CREATE FUNCTION reject_decode_attempt_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM agent_worker_task task
    JOIN capture_file file ON file.tenant_id=task.tenant_id AND file.id=task.source_id
    JOIN capture_batch batch ON batch.tenant_id=file.tenant_id AND batch.id=file.capture_batch_id
    WHERE task.id=NEW.task_id AND task.status='succeeded'
      AND file.status='completed' AND file.page_count=2 AND batch.page_count=2
  ) OR (SELECT count(*) FROM agent_worker_task WHERE tenant_id=NEW.tenant_id AND source_type='image_quality_run') <> 2 THEN
    RAISE EXCEPTION 'receipt fault reached before materialization';
  END IF;
  RAISE EXCEPTION 'synthetic decode attempt write failure';
END $$`)
	exec(`CREATE TRIGGER reject_decode_attempt_receipt BEFORE UPDATE ON agent_worker_task_attempt
FOR EACH ROW WHEN (NEW.status='succeeded') EXECUTE FUNCTION reject_decode_attempt_receipt()`)
	_, err = submit(input, result)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Message != "synthetic decode attempt write failure" {
		t.Fatalf("expected injected late receipt failure, got %v", err)
	}
	assertFacts("queued", [5]int{}, queued.Revision)
	assertFirstAttemptUnchanged()
	exec(`DROP TRIGGER reject_decode_attempt_receipt ON agent_worker_task_attempt`)

	// 文件标完成时把租约期限移到过去，验证第二次校验使用写入后的真实数据库状态，无需等待计时器。
	exec(`CREATE FUNCTION expire_materialized_decode_lease() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second'
  WHERE tenant_id=NEW.tenant_id AND source_type='capture_file' AND source_id=NEW.id;
  RETURN NEW;
END $$`)
	exec(`CREATE TRIGGER expire_materialized_decode_lease AFTER UPDATE ON capture_file
FOR EACH ROW WHEN (NEW.status='completed') EXECUTE FUNCTION expire_materialized_decode_lease()`)
	if _, err := submit(input, result); !errors.Is(err, workerruntime.ErrLeaseExpired) {
		t.Fatalf("expected lease error after file materialization, got %v", err)
	}
	assertFacts("queued", [5]int{}, queued.Revision)
	assertFirstAttemptUnchanged()
	exec(`DROP TRIGGER expire_materialized_decode_lease ON capture_file`)

	exec(`UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND id=$2`, tenant, task.ID)
	reclaimed := claim("decode-worker-b")
	if reclaimed.ID != task.ID || reclaimed.AttemptCount != 2 || reclaimed.LeaseToken == task.LeaseToken {
		t.Fatalf("replacement worker did not take over same decode task: %+v", reclaimed)
	}
	if _, err := submit(input, result); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("previous worker changed file after takeover: %v", err)
	}
	assertFacts("queued", [5]int{}, queued.Revision)

	input.LeaseToken = reclaimed.LeaseToken
	completed, err := submit(input, result)
	if err != nil || completed.ID != fileID || completed.Status != "completed" || completed.PageCount != 2 {
		t.Fatalf("new lease completion: file=%+v err=%v", completed, err)
	}
	accepted, err := runtime.Get(ctx, tenant, task.ID)
	if err != nil || accepted.Status != workerruntime.StatusSucceeded || accepted.ResultPayloadHash == "" ||
		len(accepted.Attempts) != 2 || accepted.Attempts[0].Status != "lease_expired" || accepted.Attempts[1].Status != workerruntime.StatusSucceeded {
		t.Fatalf("wrong completion receipt history: task=%+v err=%v", accepted, err)
	}
	wantCounts := [5]int{1, 2, 2, 2, 2}
	assertFacts("completed", wantCounts, queued.Revision+1)
	if replayed, err := submit(input, result); err != nil || replayed.ID != fileID {
		t.Fatalf("identical successful result did not replay: file=%+v err=%v", replayed, err)
	}
	assertFacts("completed", wantCounts, queued.Revision+1)
}

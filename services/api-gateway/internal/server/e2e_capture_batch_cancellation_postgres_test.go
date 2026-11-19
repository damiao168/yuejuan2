package server

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestPostgresCaptureBatchCancellationRetiresOnlyItsOwnMaterials(t *testing.T) {
	if os.Getenv("EDUGRADE_E2E_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL is not configured")
	}
	db, router, token, exam, answers, _ := repairFixture(t)
	ctx := t.Context()
	captures := capture.NewPostgresStore(db)
	uploads := captureupload.NewPostgresStore(db)
	runtime := workerruntime.NewPostgresStore(db)

	newBatch := func(key string) capture.Batch {
		t.Helper()
		batch, err := captures.CreateBatch(ctx, exam.TenantID, exam.ExamID, exam.AdminID, capture.CreateBatchInput{
			Name: key, SourceType: "desktop_sync", IdempotencyKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		return batch
	}
	batch, remaining := newBatch("cancel-source"), newBatch("shared-source")
	submission, gradedPage := answers[0].submission, answers[0].page
	var ungradedPage string
	if err := db.QueryRowContext(ctx, `
INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status)
VALUES($1,$2,$3,2,'accepted') RETURNING id::text`, exam.TenantID, submission, exam.PaperFileAssetID).Scan(&ungradedPage); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE submission SET actual_page_count=2 WHERE tenant_id=$1 AND id=$2`, exam.TenantID, submission)
	addSource := func(batchID, pageID string, pageNo int) string {
		t.Helper()
		var id string
		if err := db.QueryRowContext(ctx, `
INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,status,idempotency_key,uploaded_by)
VALUES($1,$2,$3,'synthetic.pdf','application/pdf',repeat('a',64),1,1,'completed',gen_random_uuid()::text,$4) RETURNING id::text`,
			exam.TenantID, batchID, exam.PaperFileAssetID, exam.AdminID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		exec(`
INSERT INTO capture_page(tenant_id,capture_batch_id,capture_file_id,source_index,sequence_no,decoded_file_asset_id,status,submission_id,submission_page_id,assigned_page_no)
VALUES($1,$2,$3,1,$4,$5,'ready',$6,$7,$4)`,
			exam.TenantID, batchID, id, pageNo, exam.PaperFileAssetID, submission, pageID)
		return id
	}
	addSource(batch.ID, gradedPage, 1)
	ungradedFile := addSource(batch.ID, ungradedPage, 2)
	_, err := runtime.CreateTask(ctx, exam.TenantID, exam.AdminID, workerruntime.CreateTaskInput{
		TaskType: "preprocess", QueueName: "page-processing", SourceType: "capture_file", SourceID: ungradedFile,
		PayloadSchemaVersion: "fixture-v1", IdempotencyKey: "cancel-with-lease", MaxAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, exam.TenantID, workerruntime.ClaimInput{
		QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "capture-cancel-fixture", Limit: 1, LeaseSeconds: 300,
	})
	if err != nil || len(claimed) != 1 || claimed[0].SourceID != ungradedFile {
		t.Fatalf("claim fixture task: tasks=%+v err=%v", claimed, err)
	}
	lease := claimed[0]

	data := []byte("answer")
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	uploadInput := captureupload.InitInput{
		ExamID: exam.ExamID, BatchID: batch.ID, SHA256: hash, Size: int64(len(data)), MIME: "image/png",
		Filename: "synthetic.png", IdempotencyKey: "cancel-upload",
	}
	session, _, err := uploads.Init(ctx, exam.TenantID, exam.AdminID, uploadInput, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = uploads.AppendChunk(ctx, exam.TenantID, session.ID, captureupload.ChunkInput{Offset: 0, SHA256: hash, Data: data}); err != nil {
		t.Fatal(err)
	}
	finalizing, proceed, err := uploads.BeginComplete(ctx, exam.TenantID, session.ID)
	if err != nil || !proceed || finalizing.CompletionToken == "" {
		t.Fatalf("reserve upload finalizer: %+v proceed=%t err=%v", finalizing, proceed, err)
	}
	assertRetirement := func(want [7]int) {
		t.Helper()
		var got [7]int
		err := db.QueryRowContext(ctx, `
SELECT
 (SELECT count(*) FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2 AND deleted_at IS NULL),
 (SELECT count(*) FROM submission_page WHERE tenant_id=$1 AND id=$3 AND deleted_at IS NULL),
 (SELECT count(*) FROM submission_page WHERE tenant_id=$1 AND id=$4 AND deleted_at IS NULL),
 (SELECT actual_page_count FROM submission WHERE tenant_id=$1 AND id=$5 AND deleted_at IS NULL),
 (SELECT count(*) FROM capture_upload_chunk WHERE tenant_id=$1 AND capture_upload_session_id=$6),
 (SELECT count(*) FROM capture_operation WHERE tenant_id=$1 AND capture_batch_id=$2 AND operation='cancel'),
 (SELECT count(*) FROM question_grade WHERE tenant_id=$1 AND id=$7 AND deleted_at IS NULL)`,
			exam.TenantID, batch.ID, gradedPage, ungradedPage, submission, session.ID, answers[0].grade).
			Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5], &got[6])
		if err != nil || got != want {
			t.Fatalf("active files, graded page, ungraded page, actual pages, chunks, cancellation receipts, grades=%v want=%v err=%v", got, want, err)
		}
	}

	const reason = "测试取消采集来源"
	route := "/api/v1/capture-batches/" + batch.ID
	request := story056JSON(t, map[string]any{"reason": reason})
	// 独占已评分页阻止取消。第二个活跃批次接手同一页后，取消只能退出自己的文件、独占页和旧租约。
	e2ePostJSON(t, router, http.MethodPost, route+"/cancel", token, request, http.StatusConflict)
	assertRetirement([7]int{2, 1, 1, 2, 1, 0, 1})
	leased, err := runtime.Get(ctx, exam.TenantID, lease.ID)
	if err != nil || leased.LeaseToken != lease.LeaseToken || leased.Status != workerruntime.StatusLeased {
		t.Fatalf("rejected cancellation changed lease: %+v err=%v", leased, err)
	}

	addSource(remaining.ID, gradedPage, 1)
	// 旧版单文件排除可能只留下 deleted 状态。这个历史引用不拥有另一份已评分答卷页。
	historicalFile := addSource(batch.ID, gradedPage, 3)
	exec(`UPDATE capture_page SET status='deleted',submission_id=$3,submission_page_id=$4
WHERE tenant_id=$1 AND capture_file_id=$2`, exam.TenantID, historicalFile, answers[1].submission, answers[1].page)
	// 台账写入在材料处理之后；模拟该写入失败，前面的软删除、会话和任务必须一起回滚。
	exec(`CREATE FUNCTION reject_capture_cancellation_receipt() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.operation='cancel' THEN RAISE EXCEPTION 'synthetic cancellation receipt failure'; END IF; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER reject_capture_cancellation_receipt BEFORE INSERT ON capture_operation FOR EACH ROW EXECUTE FUNCTION reject_capture_cancellation_receipt()`)
	e2ePostJSON(t, router, http.MethodPost, route+"/cancel", token, request, http.StatusInternalServerError)
	assertRetirement([7]int{3, 1, 1, 2, 1, 0, 1})
	exec(`DROP TRIGGER reject_capture_cancellation_receipt ON capture_operation`)
	e2ePostJSON(t, router, http.MethodPost, route+"/cancel", token, request, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, route+"/cancel", token, request, http.StatusOK)
	e2ePostJSON(t, router, http.MethodPost, route+"/cancel", token, `{"reason":"different command"}`, http.StatusConflict)
	assertRetirement([7]int{0, 1, 0, 1, 0, 1, 1})
	var directPageStillActive bool
	if err := db.QueryRowContext(ctx, `SELECT deleted_at IS NULL FROM submission_page WHERE tenant_id=$1 AND id=$2`,
		exam.TenantID, answers[1].page).Scan(&directPageStillActive); err != nil || !directPageStillActive {
		t.Fatalf("historical source altered an unrelated submission page: active=%t err=%v", directPageStillActive, err)
	}
	summary, err := captures.GetCollectionSummary(ctx, exam.TenantID, exam.ExamID)
	if err != nil || len(summary.Entries) != 2 {
		t.Fatalf("collection summary after batch retirement: %+v err=%v", summary, err)
	}
	for _, entry := range summary.Entries {
		if entry.SubmissionID == "" || entry.BatchID == batch.ID {
			t.Fatalf("retirement hid a preserved submission or left a stale source: %+v", entry)
		}
	}
	var successfulAuditCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND action='capture_batch.cancelled' AND target_id=$2`,
		exam.TenantID, batch.ID).Scan(&successfulAuditCount); err != nil || successfulAuditCount != 1 {
		t.Fatalf("successful cancellation audit count=%d err=%v", successfulAuditCount, err)
	}
	current, err := runtime.Get(ctx, exam.TenantID, lease.ID)
	if err != nil || current.Status != workerruntime.StatusCancelled || current.LeaseToken != "" {
		t.Fatalf("cancelled source left an active lease: %+v err=%v", current, err)
	}
	var attemptStatus string
	var attemptFinished bool
	if err := db.QueryRowContext(ctx, `SELECT status,completed_at IS NOT NULL FROM agent_worker_task_attempt WHERE tenant_id=$1 AND task_id=$2`,
		exam.TenantID, lease.ID).Scan(&attemptStatus, &attemptFinished); err != nil || attemptStatus != "cancelled" || !attemptFinished {
		t.Fatalf("attempt was not closed: status=%s finished=%t err=%v", attemptStatus, attemptFinished, err)
	}
	retired, err := uploads.Get(ctx, exam.TenantID, session.ID)
	if err != nil || retired.Status != "failed" || retired.CompletionToken != "" || retired.ErrorCode != "capture_batch_cancelled" {
		t.Fatalf("upload staging session was not retired: %+v err=%v", retired, err)
	}

	e2ePostJSON(t, router, http.MethodPost, route+"/reopen", token, request, http.StatusOK)
	if _, err := uploads.Complete(ctx, exam.TenantID, session.ID, exam.PaperFileAssetID, ungradedFile, finalizing.CompletionToken); !errors.Is(err, captureupload.ErrConflict) {
		t.Fatalf("old completion token survived reopening: %v", err)
	}
	_, err = captures.RegisterFile(ctx, exam.TenantID, batch.ID, exam.AdminID, capture.RegisterFileInput{
		FileAssetID: exam.PaperFileAssetID, IdempotencyKey: uploadInput.IdempotencyKey,
		UploadSessionID: session.ID, UploadCompletionToken: finalizing.CompletionToken,
	}, capture.FileAssetSnapshot{ID: exam.PaperFileAssetID, ExamID: exam.ExamID, OriginalName: "synthetic.png", ContentType: "image/png", SizeBytes: uploadInput.Size, SHA256: hash})
	if !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("old finalizer registered a new file after reopening: %v", err)
	}
	if _, err := captures.SubmitFileResultCommand(ctx, exam.TenantID, ungradedFile, capture.FileResultInput{TaskID: lease.ID, LeaseToken: lease.LeaseToken}, runtime, nil); !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("old worker receipt can still use retired file: %v", err)
	}
	currentBatch, err := captures.GetBatch(ctx, exam.TenantID, batch.ID)
	if err != nil || currentBatch.Status != "draft" || currentBatch.FileCount != 0 || currentBatch.PageCount != 0 || currentBatch.SubmissionCount != 0 {
		t.Fatalf("reopening revived prior materials: %+v err=%v", currentBatch, err)
	}
	assertRetirement([7]int{0, 1, 0, 1, 0, 1, 1})

	// 旧取消记录没有退出有效材料。重开时补偿退出并清空旧计数，仍保留活跃批次共享的评分页。
	legacy := newBatch("legacy-cancelled-source")
	legacyFile := addSource(legacy.ID, gradedPage, 1)
	exec(`UPDATE capture_batch SET status='cancelled',file_count=1,page_count=1,submission_count=1
WHERE tenant_id=$1 AND id=$2`, exam.TenantID, legacy.ID)
	legacyReopened, err := captures.SetBatchStatus(ctx, exam.TenantID, legacy.ID, exam.AdminID, "draft", "recover old cancellation")
	if err != nil || legacyReopened.FileCount != 0 || legacyReopened.PageCount != 0 || legacyReopened.SubmissionCount != 0 {
		t.Fatalf("legacy reopening kept stale counts: %+v err=%v", legacyReopened, err)
	}
	if _, err := captures.GetFile(ctx, exam.TenantID, legacyFile); !errors.Is(err, capture.ErrNotFound) {
		t.Fatalf("legacy reopening revived a retired capture file: %v", err)
	}
	assertRetirement([7]int{0, 1, 0, 1, 0, 1, 1})
}

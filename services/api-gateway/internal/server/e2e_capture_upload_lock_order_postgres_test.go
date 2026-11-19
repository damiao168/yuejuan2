package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
)

func TestPostgresCaptureCollectionConflictsWithInFlightChunk(t *testing.T) {
	db, _, _, exam, _, _ := repairFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	captures := capture.NewPostgresStore(db)
	uploads := captureupload.NewPostgresStore(db)
	batch, err := captures.CreateBatch(ctx, exam.TenantID, exam.ExamID, exam.AdminID, capture.CreateBatchInput{
		Name: "synthetic chunk and collection", SourceType: "desktop_sync", IdempotencyKey: "collection-lock",
	})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("answer")
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	input := captureupload.InitInput{ExamID: exam.ExamID, BatchID: batch.ID, SHA256: hash, Size: int64(len(data)), MIME: "image/png", Filename: "synthetic.png", IdempotencyKey: "collection-lock-upload"}
	upload, _, err := uploads.Init(ctx, exam.TenantID, exam.AdminID, input, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	before, err := captures.GetCollectionSummary(ctx, exam.TenantID, exam.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	chunk, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chunk.Rollback()
	// 用实际偏移更新触发器取得归档考试锁，并保持在提交前。收卷要立即返回可重试冲突。
	if _, err = chunk.ExecContext(ctx, `
UPDATE capture_upload_session SET confirmed_offset=$3
WHERE tenant_id=$1 AND id=$2::uuid`, exam.TenantID, upload.ID, len(data)); err != nil {
		t.Fatal(err)
	}
	pausedCtx, stop := context.WithTimeout(ctx, 2*time.Second)
	_, closeErr := captures.CompleteCollection(pausedCtx, exam.TenantID, exam.ExamID, exam.AdminID,
		capture.CompleteCollectionInput{Revision: before.Revision, IdempotencyKey: "seal-during-chunk"})
	stop()
	if !errors.Is(closeErr, capture.ErrConflict) {
		t.Fatalf("collection did not return a retryable conflict: %v", closeErr)
	}
	if err := chunk.Rollback(); err != nil {
		t.Fatal(err)
	}
	appended, err := uploads.AppendChunk(ctx, exam.TenantID, upload.ID, captureupload.ChunkInput{Offset: 0, SHA256: hash, Data: data})
	if err != nil || appended.ConfirmedOffset != int64(len(data)) {
		t.Fatalf("real chunk append did not commit offset after conflict: %+v %v", appended, err)
	}
	after, err := captures.GetCollectionSummary(ctx, exam.TenantID, exam.ExamID)
	if err != nil || after.Closed || after.Revision <= before.Revision {
		t.Fatalf("append must change the open collection revision: before=%+v after=%+v %v", before, after, err)
	}
	_, err = captures.CompleteCollection(ctx, exam.TenantID, exam.ExamID, exam.AdminID, capture.CompleteCollectionInput{Revision: before.Revision, IdempotencyKey: "seal-during-chunk"})
	if !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("stale collection revision was accepted after a chunk committed: %v", err)
	}
}

func TestPostgresCaptureUploadTakesBatchLockBeforeExamLock(t *testing.T) {
	db, _, _, exam, _, _ := repairFixture(t)
	captures := capture.NewPostgresStore(db)
	uploads := captureupload.NewService(captureupload.NewPostgresStore(db), captures, nil, nil, config.FileConfig{})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	batch, err := captures.CreateBatch(ctx, exam.TenantID, exam.ExamID, exam.AdminID, capture.CreateBatchInput{
		Name: "mixed upload lock order", SourceType: "desktop_sync", IdempotencyKey: "mixed-upload-lock-order",
	})
	if err != nil {
		t.Fatal(err)
	}
	registration, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer registration.Rollback()
	// 文件登记先持有批次写锁。初始化等待它时，登记方仍须能取得归档触发器使用的考试锁。
	var registrationPID int
	if err := registration.QueryRowContext(ctx, `
SELECT pg_backend_pid() FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE
`, exam.TenantID, batch.ID).Scan(&registrationPID); err != nil {
		t.Fatal(err)
	}
	input := captureupload.InitInput{
		ExamID: exam.ExamID, BatchID: batch.ID, SHA256: strings.Repeat("a", 64), Size: 10,
		MIME: "image/png", Filename: "synthetic.png", IdempotencyKey: "same-batch-init",
	}
	type initResult struct {
		response captureupload.InitResponse
		err      error
	}
	initialized := make(chan initResult, 1)
	go func() {
		response, err := uploads.Init(ctx, exam.TenantID, exam.AdminID, input)
		initialized <- initResult{response, err}
	}()
	if err := waitForUploadBatchLock(ctx, db, registrationPID); err != nil {
		t.Fatal(err)
	}
	var acquired bool
	if err := registration.QueryRowContext(ctx,
		`SELECT pg_try_advisory_xact_lock(hashtext($1),hashtext($2))`, exam.TenantID, exam.ExamID).Scan(&acquired); err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("initialization holds the exam lock while waiting for the batch lock")
	}
	if err := registration.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-initialized:
		if result.err != nil || result.response.RemoteUploadID == "" || result.response.AlreadyExists {
			t.Fatalf("new upload did not initialize after the batch lock was released: %+v", result)
		}
		replay, err := uploads.Init(ctx, exam.TenantID, exam.AdminID, input)
		if err != nil || replay.RemoteUploadID != result.response.RemoteUploadID || !replay.AlreadyExists {
			t.Fatalf("idempotent initialization did not reuse the new session: %+v error=%v", replay, err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitForUploadBatchLock(ctx context.Context, db *sql.DB, registrationPID int) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `
SELECT EXISTS(
 SELECT 1 FROM pg_stat_activity
 WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock'
   AND $1=ANY(pg_blocking_pids(pid))
)
`, registrationPID).Scan(&waiting)
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if waiting {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("initialization never waited for the held batch row: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

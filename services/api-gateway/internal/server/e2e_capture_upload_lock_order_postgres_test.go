package server

import (
	"context"
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

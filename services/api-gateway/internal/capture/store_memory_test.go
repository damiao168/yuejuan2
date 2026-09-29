package capture

import (
	"context"
	"errors"
	"testing"
)

func TestCaptureBatchDecodeFlow(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	batch, err := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "第一扫描批次", SourceType: "web_upload", IdempotencyKey: "batch-1"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "第一扫描批次", SourceType: "web_upload", IdempotencyKey: "batch-1"})
	if err != nil || again.ID != batch.ID {
		t.Fatalf("idempotent create returned %#v, %v", again, err)
	}
	if _, err := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "changed", SourceType: "web_upload", IdempotencyKey: "batch-1"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("same command with changed input must conflict, got %v", err)
	}
	file, err := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset-1", IdempotencyKey: "file-1"}, FileAssetSnapshot{ID: "asset-1", ExamID: "exam-1", OriginalName: "answers.pdf", ContentType: "application/pdf", SizeBytes: 2048, SHA256: "sha256:a"})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1")
	if err != nil || queued.Status != "processing" {
		t.Fatalf("queue returned %#v, %v", queued, err)
	}
	result, err := store.ApplyFileResult(ctx, "tenant-1", file.ID, []DecodedPageInput{
		{SourceIndex: 1, FileAssetID: "page-1", SHA256: "sha256:p1", Width: 2480, Height: 3508},
		{SourceIndex: 2, FileAssetID: "page-2", SHA256: "sha256:p2", Width: 2480, Height: 3508},
	})
	if err != nil || result.PageCount != 2 || result.Status != "completed" {
		t.Fatalf("result returned %#v, %v", result, err)
	}
	pages, err := store.ListPages(ctx, "tenant-1", batch.ID)
	if err != nil || len(pages) != 2 || pages[0].SubmissionID == "" || pages[0].SubmissionID != pages[1].SubmissionID {
		t.Fatalf("pages returned %#v, %v", pages, err)
	}
	if pages[0].Status != "quality_checking" {
		t.Fatalf("decoded page should wait for quality processing, got %q", pages[0].Status)
	}
	if err := store.ApplyQualityOutcome(ctx, "tenant-1", pages[0].SubmissionPageID, "passed"); err != nil {
		t.Fatal(err)
	}
	pages, _ = store.ListPages(ctx, "tenant-1", batch.ID)
	if pages[0].Status != "normalized" {
		t.Fatalf("passed page should expose normalized state, got %q", pages[0].Status)
	}
	// 质检通过只进入 normalized，配准尚未完成时汇总仍应阻塞批次完成。
	summary, err := store.GetProcessingSummary(ctx, "tenant-1", pages[0].SubmissionID)
	if err != nil || summary.TotalPages != 2 || summary.PendingPages != 2 || summary.CanComplete {
		t.Fatalf("processing summary must reflect the stored page state: %#v, %v", summary, err)
	}
	summaries, err := store.ListProcessingSummaries(ctx, "tenant-1", batch.ID)
	if err != nil || len(summaries) != 1 || summaries[0].SubmissionID != pages[0].SubmissionID {
		t.Fatalf("batch processing summaries returned %#v, %v", summaries, err)
	}
}

func TestCaptureRejectsStalePageRevisionAndCrossTenant(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	batch, _ := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "批次", SourceType: "web_upload"})
	file, _ := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset", IdempotencyKey: "file"}, FileAssetSnapshot{ID: "asset", SizeBytes: 10, SHA256: "sha256:a"})
	_, _ = store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1")
	_, _ = store.ApplyFileResult(ctx, "tenant-1", file.ID, []DecodedPageInput{{SourceIndex: 1, FileAssetID: "page", SHA256: "sha256:p", Width: 100, Height: 200}})
	pages, _ := store.ListPages(ctx, "tenant-1", batch.ID)
	rotation := 90
	updated, err := store.UpdatePage(ctx, "tenant-1", pages[0].ID, "user-1", UpdatePageInput{Revision: 1, RotationDegrees: &rotation})
	if err != nil || updated.RotationDegrees != 90 {
		t.Fatalf("update returned %#v, %v", updated, err)
	}
	if _, err := store.UpdatePage(ctx, "tenant-1", pages[0].ID, "user-1", UpdatePageInput{Revision: 1, RotationDegrees: &rotation}); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := store.GetBatch(ctx, "tenant-2", batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected tenant isolation, got %v", err)
	}
}

func TestCompletedBatchRejectsFurtherWrites(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	batch, _ := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "locked", SourceType: "web_upload"})
	file, _ := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset", IdempotencyKey: "file"}, FileAssetSnapshot{ID: "asset", SizeBytes: 10, SHA256: "sha256:a"})
	_, _ = store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1")
	_, _ = store.ApplyFileResult(ctx, "tenant-1", file.ID, []DecodedPageInput{{SourceIndex: 1, FileAssetID: "page", SHA256: "sha256:p", Width: 100, Height: 200}})
	pages, _ := store.ListPages(ctx, "tenant-1", batch.ID)
	batch = store.batches[batch.ID]
	batch.Status = "completed"
	store.batches[batch.ID] = batch

	rotation := 90
	if _, err := store.UpdatePage(ctx, "tenant-1", pages[0].ID, "user-1", UpdatePageInput{Revision: pages[0].Revision, RotationDegrees: &rotation}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completed batch page update should be rejected, got %v", err)
	}
	if _, err := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset-2", IdempotencyKey: "file-2"}, FileAssetSnapshot{ID: "asset-2", SizeBytes: 10, SHA256: "sha256:b"}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completed batch file registration should be rejected, got %v", err)
	}
	if _, err := store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completed batch queue should be rejected, got %v", err)
	}
}

func TestFailedCaptureFileCanRetryAndSameHashCanBeReuploaded(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	batch, err := store.CreateBatch(ctx, "tenant-1", "exam-1", "user-1", CreateBatchInput{Name: "recovery", SourceType: "web_upload"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset-1", IdempotencyKey: "file-1"}, FileAssetSnapshot{ID: "asset-1", SizeBytes: 10, SHA256: "sha256:same"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1"); err != nil {
		t.Fatal(err)
	}
	failed, err := store.ApplyFileFailure(ctx, "tenant-1", first.ID, "pdf_decode_failed", false)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("fail source file: %#v %v", failed, err)
	}
	reuploaded, err := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset-2", IdempotencyKey: "file-2"}, FileAssetSnapshot{ID: "asset-2", SizeBytes: 10, SHA256: "sha256:same"})
	if err != nil || reuploaded.Status != "uploaded" {
		t.Fatalf("failed hash should be accepted again: %#v %v", reuploaded, err)
	}
	duplicate, err := store.RegisterFile(ctx, "tenant-1", batch.ID, "user-1", RegisterFileInput{FileAssetID: "asset-3", IdempotencyKey: "file-3"}, FileAssetSnapshot{ID: "asset-3", SizeBytes: 10, SHA256: "sha256:same"})
	if err != nil || duplicate.Status != "duplicate" {
		t.Fatalf("active hash should still be deduplicated: %#v %v", duplicate, err)
	}
	if _, err = store.QueueBatch(ctx, "tenant-1", batch.ID, "user-1"); err != nil {
		t.Fatalf("retry failed file in batch: %v", err)
	}
	first, _ = store.GetFile(ctx, "tenant-1", first.ID)
	reuploaded, _ = store.GetFile(ctx, "tenant-1", reuploaded.ID)
	if first.Status != "queued" || first.ErrorCode != "" || reuploaded.Status != "queued" {
		t.Fatalf("recovery queue did not reset eligible files: first=%#v reuploaded=%#v", first, reuploaded)
	}
}

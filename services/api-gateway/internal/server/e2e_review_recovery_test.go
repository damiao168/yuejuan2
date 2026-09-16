package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/calibration"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/captureupload"
	"edugrade-enterprise/services/api-gateway/internal/config"
	"edugrade-enterprise/services/api-gateway/internal/files"
	"edugrade-enterprise/services/api-gateway/internal/goldpaper"
	"edugrade-enterprise/services/api-gateway/internal/imagequality"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/seedquality"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type repairFailPut struct {
	files.ObjectStorage
	fail bool
}

func (s *repairFailPut) Put(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if s.fail {
		s.fail = false
		return errors.New("injected object storage outage")
	}
	return s.ObjectStorage.Put(ctx, bucket, key, body, size, contentType)
}

func TestReviewRepairsRecoveryWithPostgresTestDatabase(t *testing.T) {
	db, router, adminToken, f, answers := repairFixture(t)
	ctx := context.Background()
	t.Run("seed follows normal authenticated claim context submit path", func(t *testing.T) {
		graderID := e2eLookupUserID(t, db, "demo", "grader")
		graderToken := e2eLoginWithTenant(t, router, "demo", "grader", "ChangeMe123!")
		q := f.QuestionIDs["single_choice"]
		goldStore := goldpaper.NewPostgresStore(db)
		gold, err := goldStore.Nominate(ctx, f.TenantID, f.ExamID, q, f.AdminID, goldpaper.NominateInput{SubmissionID: answers[0].submission, ReferenceScore: 0, Explanation: "verified reference", SourceGradeIDs: []string{answers[0].grade}})
		if err != nil {
			t.Fatal(err)
		}
		gold, err = goldStore.Approve(ctx, f.TenantID, gold.ID, graderID, 1)
		if err != nil {
			t.Fatal(err)
		}
		calib := calibration.NewService(calibration.NewPostgresStore(db), goldStore)
		_, err = calib.PutPolicy(ctx, f.TenantID, f.ExamID, q, calibration.PutPolicyInput{ArchetypeCode: gold.ArchetypeCode, MaxScore: 1, MinimumSamples: 1, MinimumExactAgreement: 1, MinimumWithinOneAgreement: 1, SevereErrorThreshold: 1, QualificationValidityDays: 30})
		if err != nil {
			t.Fatal(err)
		}
		session, err := calib.CreateSession(ctx, f.TenantID, f.ExamID, q, graderID)
		if err != nil {
			t.Fatal(err)
		}
		_, _, qualification, err := calib.SubmitAttempt(ctx, f.TenantID, session.ID, calibration.SubmitAttemptInput{GoldPaperID: gold.ID, SubmittedScore: 0})
		if err != nil || qualification == nil {
			t.Fatalf("qualification: %+v %v", qualification, err)
		}
		seed := seedquality.NewService(seedquality.NewPostgresStore(db), goldStore, calib, assessment.NewPostgresStore(db))
		_, err = seed.PutPolicy(ctx, f.TenantID, f.ExamID, q, f.AdminID, seedquality.PutPolicyInput{Rate: 1, MinInterval: 1, MaxInterval: 1, Status: seedquality.PolicyActive})
		if err != nil {
			t.Fatal(err)
		}
		claimed := e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/next", graderToken, story056JSON(t, map[string]any{"exam_id": f.ExamID, "question_id": q}), http.StatusOK)
		task := claimed["task"].(map[string]any)
		id := e2eString(t, task, "id")
		var ordinary int
		if err = db.QueryRow(`SELECT count(*) FROM review_task WHERE tenant_id=$1 AND id=$2`, f.TenantID, id).Scan(&ordinary); err != nil || ordinary != 0 {
			t.Fatalf("not a hidden seed: %d %v", ordinary, err)
		}
		e2eGetJSON(t, router, "/api/v1/review-tasks/"+id+"/context", graderToken, http.StatusOK)
		e2eGetJSON(t, router, "/api/v1/review-tasks/"+id+"/context", adminToken, http.StatusForbidden)
		e2ePostJSON(t, router, http.MethodPost, "/api/v1/review-tasks/"+id+"/submit", graderToken, `{"score":0,"expected_revision":1,"rubric_selections":[]}`, http.StatusCreated)
	})
	t.Run("upload retry and expired completion lease", func(t *testing.T) {
		captures := capture.NewPostgresStore(db)
		batch, err := captures.CreateBatch(ctx, f.TenantID, f.ExamID, f.AdminID, capture.CreateBatchInput{Name: "repair", SourceType: "desktop_sync", IdempotencyKey: "repair-capture-batch"})
		if err != nil {
			t.Fatal(err)
		}
		store := captureupload.NewPostgresStore(db)
		fileStore := files.NewPostgresStore(db)
		objects := &repairFailPut{ObjectStorage: files.NewMemoryObjectStorage(), fail: true}
		svc := captureupload.NewService(store, captures, fileStore, objects, config.FileConfig{Bucket: "repair-test", MaxUploadBytes: 1024 * 1024})
		for i := 0; i < 2; i++ {
			payload := []byte(fmt.Sprintf("\x89PNG\r\n\x1a\nrepair-%d", i))
			hash := fmt.Sprintf("%x", sha256.Sum256(payload))
			init, err := svc.Init(ctx, f.TenantID, f.AdminID, captureupload.InitInput{SHA256: hash, Size: int64(len(payload)), MIME: "image/png", ExamID: f.ExamID, BatchID: batch.ID, IdempotencyKey: fmt.Sprintf("repair-upload-%d", i), Filename: "page.png"})
			if err != nil {
				t.Fatal(err)
			}
			id := init.RemoteUploadID
			if _, err = svc.AppendChunk(ctx, f.TenantID, id, captureupload.ChunkInput{SHA256: hash, Data: payload}); err != nil {
				t.Fatal(err)
			}
			var failedAssetID string
			if i == 0 {
				if _, err = svc.Complete(ctx, f.TenantID, f.AdminID, id, captureupload.CompleteInput{SHA256: hash}); !errors.Is(err, captureupload.ErrStorage) {
					t.Fatalf("outage: %v", err)
				}
				asset, found, err := fileStore.FindDuplicate(ctx, f.TenantID, "capture_batch", batch.ID, hash)
				if err != nil || !found {
					t.Fatalf("failed asset: %v", err)
				}
				failedAssetID = asset.ID
			} else {
				old, _, err := store.BeginComplete(ctx, f.TenantID, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, _, err = store.BeginComplete(ctx, f.TenantID, id); !errors.Is(err, captureupload.ErrConflict) {
					t.Fatalf("live lease: %v", err)
				}
				expire := func() {
					if _, err = db.Exec(`UPDATE capture_upload_session SET completion_lease_until=now()-interval '1 second' WHERE id=$1`, id); err != nil {
						t.Fatal(err)
					}
				}
				expire()
				owner, _, err := store.BeginComplete(ctx, f.TenantID, id)
				if err != nil || owner.CompletionToken == old.CompletionToken {
					t.Fatalf("takeover: %+v %v", owner, err)
				}
				if err = store.Resume(ctx, f.TenantID, id, "stale failure", old.CompletionToken); !errors.Is(err, captureupload.ErrConflict) {
					t.Fatalf("stale reset: %v", err)
				}
				if _, err = store.Complete(ctx, f.TenantID, id, f.PaperFileAssetID, f.PaperFileAssetID, old.CompletionToken); err == nil {
					t.Fatal("stale completion accepted")
				}
				expire()
			}
			completed, err := svc.Complete(ctx, f.TenantID, f.AdminID, id, captureupload.CompleteInput{SHA256: hash})
			if err != nil || completed.Status != "completed" || (failedAssetID != "" && completed.FileAssetID != failedAssetID) {
				t.Fatalf("recovery: %+v %v", completed, err)
			}
		}
		registered, err := captures.ListFiles(ctx, f.TenantID, batch.ID)
		if err != nil || len(registered) != 2 {
			t.Fatalf("capture registrations: %d %v", len(registered), err)
		}
	})
	t.Run("terminal quality retry resets source and runtime atomically", func(t *testing.T) {
		quality := imagequality.NewPostgresStore(db)
		runtime := workerruntime.NewPostgresStore(db)
		asset, err := files.NewPostgresStore(db).Get(ctx, f.TenantID, f.PaperFileAssetID)
		if err != nil {
			t.Fatal(err)
		}
		runs, err := quality.CreateRunsWithTasks(ctx, f.TenantID, f.AdminID, imagequality.CreateRunsInput{SubmissionID: answers[0].submission, Profile: imagequality.DefaultProfile(), Pages: []imagequality.PageSource{{SubmissionPageID: answers[0].page, PageNo: 1, SourceFileAssetID: asset.ID, SourceSHA256: asset.HashSHA256}}})
		if err != nil || len(runs) != 1 {
			t.Fatalf("create: %+v %v", runs, err)
		}
		claim := func() workerruntime.Task {
			tasks, err := runtime.Claim(ctx, f.TenantID, workerruntime.ClaimInput{QueueName: "image-quality", WorkerService: "image-quality-worker", WorkerInstanceID: "repair-worker", Limit: 1, LeaseSeconds: 300})
			if err != nil || len(tasks) != 1 {
				t.Fatalf("claim: %+v %v", tasks, err)
			}
			task := tasks[0]
			if _, err = quality.LeaseRun(ctx, f.TenantID, runs[0].ID, "repair-worker", task.LeaseToken, *task.LeaseExpiresAt, task.AttemptCount); err != nil {
				t.Fatal(err)
			}
			return task
		}
		first := claim()
		_, err = quality.SubmitResultCommand(ctx, f.TenantID, f.AdminID, runs[0].ID, imagequality.ResultInput{LeaseToken: first.LeaseToken, AttemptNo: first.AttemptCount, ResultVersion: "repair-failed-v1", ProcessingStatus: imagequality.ProcessingTerminalError, ErrorCode: "decode_failed"})
		if err != nil {
			t.Fatal(err)
		}
		projector := processing.NewPostgresStore(db)
		if err = projector.RefreshExam(ctx, f.TenantID, f.ExamID); err != nil {
			t.Fatal(err)
		}
		list, err := projector.ListExceptions(ctx, f.TenantID, processing.ExceptionFilter{ExamID: f.ExamID, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		var exceptionID string
		for _, entry := range list.Exceptions {
			target, targetErr := projector.RetryTarget(ctx, f.TenantID, entry.ID)
			if targetErr == nil && target.SourceID == runs[0].ID {
				exceptionID = entry.ID
				break
			}
		}
		if exceptionID == "" {
			t.Fatalf("missing retry exception: %+v", list)
		}
		// Fail the source write after the runtime requeue. Both changes must roll
		// back, rather than leaving an unclaimable queued task behind.
		_, err = db.Exec(`CREATE FUNCTION repair_reject_quality_retry() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.processing_status='retryable_error' THEN RAISE EXCEPTION 'injected retry failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER repair_reject_quality_retry BEFORE UPDATE ON submission_page_quality_run FOR EACH ROW EXECUTE FUNCTION repair_reject_quality_retry()`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = projector.RetryImageQuality(ctx, f.TenantID, runs[0].ID, first.ID); err == nil {
			t.Fatal("injected failure did not fail")
		}
		unchanged, err := runtime.Get(ctx, f.TenantID, first.ID)
		if err != nil || (unchanged.Status != workerruntime.StatusFailed && unchanged.Status != workerruntime.StatusDeadLetter) {
			t.Fatalf("runtime write escaped rollback: %+v %v", unchanged, err)
		}
		var sourceStatus string
		if err = db.QueryRow(`SELECT processing_status FROM submission_page_quality_run WHERE id=$1`, runs[0].ID).Scan(&sourceStatus); err != nil || sourceStatus != imagequality.ProcessingTerminalError {
			t.Fatalf("source write escaped rollback: %s %v", sourceStatus, err)
		}
		if _, err = db.Exec(`DROP TRIGGER repair_reject_quality_retry ON submission_page_quality_run; DROP FUNCTION repair_reject_quality_retry()`); err != nil {
			t.Fatal(err)
		}
		retried, err := processing.NewService(projector, runtime).Retry(ctx, f.TenantID, exceptionID)
		if err != nil || retried.Status != workerruntime.StatusQueued {
			t.Fatalf("retry: %+v %v", retried, err)
		}
		second := claim()
		if second.LeaseToken == first.LeaseToken {
			t.Fatal("old worker lease reused")
		}
		_, err = quality.SubmitResultCommand(ctx, f.TenantID, f.AdminID, runs[0].ID, imagequality.ResultInput{LeaseToken: second.LeaseToken, AttemptNo: second.AttemptCount, ResultVersion: "repair-completed-v2", ProcessingStatus: imagequality.ProcessingCompleted, QualityStatus: imagequality.QualityReview})
		if err != nil {
			t.Fatal(err)
		}
	})
}

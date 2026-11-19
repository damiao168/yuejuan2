package capture

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"
)

const registerTxGroup = "20000000-0000-0000-0000-00000000a001"

func fileTxRow(fileID, assetID, status, message, command, group string, groupCount, groupIndex, pagesPerStudent int64) []driver.Value {
	return []driver.Value{fileID, captureTxTenant, captureTxBatch, assetID, "实扫答卷.pdf", "application/pdf",
		"page-image-sha", int64(512), int64(0), status, message, command, captureTxActor,
		time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC), group, groupCount, groupIndex, pagesPerStudent}
}

func registrationTxPrefix() []collectionSQLStep {
	return []collectionSQLStep{
		{kind: "begin"}, captureTxWritable("draft"),
		captureTxQuery(2, [][]driver.Value{{captureTxExam, "draft"}}, []driver.Value{captureTxTenant, captureTxBatch},
			"SELECT exam_id::text, status FROM capture_batch", "tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE"),
	}
}

func registrationTxGroupCheck(conflicting bool) collectionSQLStep {
	return captureTxQuery(1, [][]driver.Value{{conflicting}},
		[]driver.Value{captureTxTenant, captureTxBatch, "second-file", "asset", registerTxGroup, int64(3), int64(2), int64(4)},
		"tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL",
		"idempotency_key=$3 AND (file_asset_id<>$4::uuid", "COALESCE(upload_group_id::text,'')<>$5",
		"upload_group_file_index=$7 AND idempotency_key<>$3", "pages_per_student<>$8")
}

func TestPostgresFileRegistrationPreservesAcceptedAssetAndDuplicateState(t *testing.T) {
	countErr := errors.New("file count update failed")
	for _, tc := range []struct {
		name          string
		activeMatch   bool
		returnedState string
		countErr      error
	}{
		{name: "new content uploaded", returnedState: "uploaded"},
		{name: "second asset with active SHA marked duplicate", activeMatch: true, returnedState: "duplicate"},
		{name: "command replay preserves queued record", activeMatch: true, returnedState: "queued"},
		{name: "cannot commit file when batch count update fails", returnedState: "uploaded", countErr: countErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var activeRows [][]driver.Value
			insertStatus := "uploaded"
			if tc.activeMatch {
				activeRows = [][]driver.Value{{"active-file"}}
				insertStatus = "duplicate"
			}
			count := captureTxExec([]driver.Value{captureTxTenant, captureTxBatch}, "UPDATE capture_batch SET status=CASE WHEN status='draft' THEN 'uploading'",
				"file_count=(SELECT count(*) FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL)")
			count.queryErr = tc.countErr
			row := fileTxRow("accepted-file", "asset", tc.returnedState, "", "second-file", registerTxGroup, 3, 2, 4)
			steps := append(registrationTxPrefix(), registrationTxGroupCheck(false),
				captureTxQuery(1, activeRows, []driver.Value{captureTxTenant, captureTxBatch, "page-image-sha"},
					"sha256=$3", "status IN ('uploaded','queued','processing','completed')", "deleted_at IS NULL"),
				captureTxQuery(18, [][]driver.Value{row},
					[]driver.Value{captureTxTenant, captureTxBatch, "asset", "实扫答卷.pdf", "application/pdf", "page-image-sha", int64(512),
						insertStatus, "second-file", captureTxActor, registerTxGroup, int64(3), int64(2), int64(4)},
					"INSERT INTO capture_file", "ON CONFLICT (tenant_id, capture_batch_id, idempotency_key)",
					"DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key",
					"WHERE capture_file.deleted_at IS NULL\nRETURNING"),
				count)
			if tc.countErr != nil {
				steps = append(steps, collectionSQLStep{kind: "rollback"})
			} else {
				steps = append(steps, collectionSQLStep{kind: "commit"})
			}
			store, _ := collectionProbeStore(t, steps)
			file, err := store.RegisterFile(t.Context(), captureTxTenant, captureTxBatch, captureTxActor, RegisterFileInput{
				FileAssetID: " asset ", IdempotencyKey: " second-file ", UploadGroupID: strings.ToUpper(registerTxGroup),
				UploadGroupFileCount: 3, UploadGroupFileIndex: 2, PagesPerStudent: 4,
			}, FileAssetSnapshot{ID: "asset", ExamID: captureTxExam, OriginalName: "实扫答卷.pdf",
				ContentType: "application/pdf", SHA256: "page-image-sha", SizeBytes: 512})
			if tc.countErr != nil {
				if !errors.Is(err, tc.countErr) || file.ID != "" {
					t.Fatalf("register on count failure returned file=%+v, error=%v", file, err)
				}
				return
			}
			if err != nil || file.ID != "accepted-file" || file.FileAssetID != "asset" || file.Status != tc.returnedState ||
				file.IdempotencyKey != "second-file" || file.UploadGroupID != registerTxGroup ||
				file.UploadGroupFileCount != 3 || file.UploadGroupFileIndex != 2 || file.PagesPerStudent != 4 {
				t.Fatalf("register returned file=%+v, error=%v", file, err)
			}
		})
	}
}

func TestPostgresFileRegistrationRejectsInvalidExamAndExistingGroupConflicts(t *testing.T) {
	lookupErr := errors.New("duplicate lookup unavailable")
	duplicateLookup := captureTxQuery(1, nil, []driver.Value{captureTxTenant, captureTxBatch, "page-image-sha"},
		"tenant_id=$1 AND capture_batch_id=$2::uuid AND sha256=$3", "deleted_at IS NULL")
	duplicateLookup.queryErr = lookupErr
	for _, tc := range []struct {
		name, exam string
		steps      []collectionSQLStep
		want       error
	}{
		{name: "asset belongs to another exam", exam: "other-exam", steps: registrationTxPrefix(), want: ErrInvalidInput},
		{name: "command changed source asset or grouping", exam: captureTxExam, steps: append(registrationTxPrefix(), registrationTxGroupCheck(true)), want: ErrConflict},
		{name: "batch already completed", exam: captureTxExam, steps: []collectionSQLStep{{kind: "begin"}, captureTxWritable("completed")}, want: ErrInvalidTransition},
		{name: "deduplication read fails", exam: captureTxExam,
			steps: append(registrationTxPrefix(), registrationTxGroupCheck(false), duplicateLookup), want: lookupErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := collectionProbeStore(t, append(tc.steps, collectionSQLStep{kind: "rollback"}))
			file, err := store.RegisterFile(t.Context(), captureTxTenant, captureTxBatch, captureTxActor, RegisterFileInput{
				FileAssetID: "asset", IdempotencyKey: "second-file", UploadGroupID: registerTxGroup,
				UploadGroupFileCount: 3, UploadGroupFileIndex: 2, PagesPerStudent: 4,
			}, FileAssetSnapshot{ID: "asset", ExamID: tc.exam, OriginalName: "实扫答卷.pdf",
				ContentType: "application/pdf", SHA256: "page-image-sha", SizeBytes: 512})
			if !errors.Is(err, tc.want) || file.ID != "" {
				t.Fatalf("register on rejected input returned file=%+v, error=%v, want %v", file, err, tc.want)
			}
		})
	}
}

func queueTxPrefix(rows ...[]driver.Value) []collectionSQLStep {
	return []collectionSQLStep{
		{kind: "begin"}, captureTxWritable("needs_review"),
		captureTxQuery(2, [][]driver.Value{{"needs_review", captureTxExam}}, []driver.Value{captureTxTenant, captureTxBatch},
			"SELECT status, exam_id::text", "WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE"),
		captureTxQuery(18, rows, []driver.Value{captureTxTenant, captureTxBatch}, "FROM capture_file",
			"WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND status IN ('uploaded','failed')",
			"AND deleted_at IS NULL", "ORDER BY created_at\nFOR UPDATE"),
	}
}

func TestPostgresQueueBatchResetsFailedLeaseAndCommitsNewTasksTogether(t *testing.T) {
	const newPayload = `{"capture_file_id":"fresh","content_type":"application/pdf","download_url":"/api/v1/files/fresh-asset/download","exam_id":"exam","file_asset_id":"fresh-asset","max_pages":500,"render_dpi":300,"sha256":"page-image-sha"}`
	retryErr := errors.New("worker task lease reset failed")
	for _, tc := range []struct {
		name     string
		retryErr error
	}{
		{name: "new and failed files queued"},
		{name: "reset error rolls back the already queued new file", retryErr: retryErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			retry := captureTxExec([]driver.Value{captureTxTenant, "reprocess"}, "UPDATE agent_worker_task",
				"status='queued'", "lease_token=NULL", "lease_expires_at=NULL", "not_before=NULL",
				"max_attempts=GREATEST(max_attempts,attempt_count+1)", "AND status IN ('failed','dead_letter')")
			retry.queryErr = tc.retryErr
			steps := append(queueTxPrefix(
				fileTxRow("fresh", "fresh-asset", "uploaded", "", "fresh-command", "", 0, 0, 0),
				fileTxRow("reprocess", "reprocess-asset", "failed", "decode_failed", "retry-command", "", 0, 0, 0)),
				captureTxExec([]driver.Value{captureTxTenant, "fresh", []byte(newPayload), "capture-decode:fresh:v1", captureTxActor},
					"INSERT INTO agent_worker_task", "'capture_file_decode','page-processing','capture_file'",
					"'capture-file-decode-v1',$4,$4", "ON CONFLICT (tenant_id,task_type,idempotency_key) DO NOTHING"),
				captureTxExec([]driver.Value{captureTxTenant, "fresh"}, "UPDATE capture_file SET status='queued',error_code=NULL"), retry)
			if tc.retryErr != nil {
				steps = append(steps, collectionSQLStep{kind: "rollback"})
			} else {
				steps = append(steps,
					captureTxExec([]driver.Value{captureTxTenant, "reprocess"}, "UPDATE capture_file SET status='queued',error_code=NULL"),
					captureTxQuery(19, [][]driver.Value{captureTxBatchRow("processing", 8, 2, 0)}, []driver.Value{captureTxTenant, captureTxBatch},
						"UPDATE capture_batch SET status='processing',started_at=COALESCE(started_at,now()),revision=revision+1"),
					collectionSQLStep{kind: "commit"})
			}
			store, _ := collectionProbeStore(t, steps)
			batch, err := store.QueueBatch(t.Context(), captureTxTenant, captureTxBatch, captureTxActor)
			if tc.retryErr != nil {
				if !errors.Is(err, tc.retryErr) || batch.ID != "" {
					t.Fatalf("queue on retry reset failure returned batch=%+v, error=%v", batch, err)
				}
				return
			}
			if err != nil || batch.ID != captureTxBatch || batch.Status != "processing" || batch.Revision != 8 ||
				batch.FileCount != 2 || batch.PageCount != 0 {
				t.Fatalf("queue returned batch=%+v, error=%v", batch, err)
			}
		})
	}
}

func TestPostgresQueueBatchWritesNothingWithoutCompleteEligibleFileSet(t *testing.T) {
	rowsErr := errors.New("interrupted file list")
	broken := queueTxPrefix(fileTxRow("fresh", "fresh-asset", "uploaded", "", "fresh-command", "", 0, 0, 0))
	broken[len(broken)-1].readErr = rowsErr
	for _, tc := range []struct {
		name  string
		steps []collectionSQLStep
		want  error
	}{
		{name: "no uploaded or failed files", steps: queueTxPrefix(), want: ErrInvalidTransition},
		{name: "partial list from database", steps: broken, want: rowsErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := collectionProbeStore(t, append(tc.steps, collectionSQLStep{kind: "rollback"}))
			batch, err := store.QueueBatch(t.Context(), captureTxTenant, captureTxBatch, captureTxActor)
			if !errors.Is(err, tc.want) || batch.ID != "" {
				t.Fatalf("queue rejected list with batch=%+v, error=%v, want %v", batch, err, tc.want)
			}
		})
	}
}

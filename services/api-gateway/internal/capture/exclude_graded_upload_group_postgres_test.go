package capture

import (
	"errors"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

func TestPostgresCaptureExclusionRollsBackEntireGroupWhenLaterFileHasGrades(t *testing.T) {
	ctx, db := captureRecoveryRemediationDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"submission_page", "answer_segment", "ai_grade", "human_grade", "question_grade", "final_grade", "subjective_grading_run", "scoring_run_item", "page_template_match_run", "capture_operation"} {
		exec(`CREATE TABLE ` + table + ` (LIKE public.` + table + ` INCLUDING ALL)`)
	}
	tenantID, examID, actorID, batchID, groupID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	submissionID, reviewTaskID, gradeID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status,file_count,page_count,submission_count)
VALUES($1,$2,$3,'synthetic upload group','web_upload',$4,'processing',2,2,1)`, batchID, tenantID, examID, actorID)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,collected_by)
VALUES($1,$2,$3,'image_upload','pages_uploaded',2,2,$4)`, submissionID, tenantID, examID, actorID)

	// 排除按文件 ID 顺序处理。先排到无评分文件，才能验证后续命中评分边界时会回滚已经发生的写入。
	const firstFileID = "00000000-0000-4000-8000-000000000001"
	const gradedFileID = "00000000-0000-4000-8000-000000000002"
	for i, fileID := range []string{firstFileID, gradedFileID} {
		pageNo := i + 1
		pageID, assetID, segmentID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,idempotency_key,uploaded_by,status,upload_group_id,upload_group_file_count,upload_group_file_index,pages_per_student)
VALUES($1,$2,$3,$4,'synthetic.png','image/png',$5,10,1,$1::uuid::text,$6,'completed',$7,2,$8,2)`,
			fileID, tenantID, batchID, assetID, strings.Repeat(string('a'+rune(i)), 64), actorID, groupID, pageNo)
		exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status)
VALUES($1,$2,$3,$4,$5,'uploaded')`, pageID, tenantID, submissionID, assetID, pageNo)
		exec(`INSERT INTO capture_page(tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status)
VALUES($1,$2,$3,1,$4,$5,$6,$6,$7,'ready')`, tenantID, batchID, fileID, submissionID, pageID, pageNo, assetID)
		exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status)
VALUES($1,$2,$3,$4,$5,$6::int::text,'{}','configured_answer_area','accepted')`, segmentID, tenantID, submissionID, pageID, uuid.NewString(), pageNo)
		if fileID == gradedFileID {
			exec(`INSERT INTO human_grade(id,tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round)
VALUES($1,$2,$3,$4,$5,4,5,'single')`, gradeID, tenantID, reviewTaskID, segmentID, actorID)
		}
	}

	store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
	_, err := runtime.CreateTask(ctx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "preprocess", QueueName: "page-processing", SourceType: "capture_file", SourceID: firstFileID, PayloadSchemaVersion: "fixture-v1", IdempotencyKey: firstFileID, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing-worker", WorkerInstanceID: "synthetic-exclusion", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].SourceID != firstFileID {
		t.Fatalf("claim first file: tasks=%+v err=%v", claimed, err)
	}
	lease := claimed[0]
	assertActiveRows := func(want [6]int) {
		t.Helper()
		var got [6]int
		err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2 AND deleted_at IS NULL),
 (SELECT count(*) FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2 AND status='ready' AND revision=1),
 (SELECT count(*) FROM submission_page WHERE tenant_id=$1 AND submission_id=$3 AND deleted_at IS NULL),
 (SELECT count(*) FROM answer_segment WHERE tenant_id=$1 AND submission_id=$3 AND deleted_at IS NULL AND processing_status='completed'),
 (SELECT count(*) FROM submission WHERE tenant_id=$1 AND id=$3 AND deleted_at IS NULL AND actual_page_count=2),
 (SELECT count(*) FROM capture_operation WHERE tenant_id=$1 AND capture_batch_id=$2)`, tenantID, batchID, submissionID).
			Scan(&got[0], &got[1], &got[2], &got[3], &got[4], &got[5])
		if err != nil || got != want {
			t.Fatalf("files, untouched capture pages, submission pages, segments, submissions, audit events=%v want=%v err=%v", got, want, err)
		}
	}

	input := ExcludeFileInput{Reason: "exclude the incorrectly scanned upload"}
	if _, err = store.ExcludeFile(ctx, tenantID, firstFileID, actorID, input); !errors.Is(err, ErrFileHasGrades) {
		t.Fatalf("expected active grade on the other file to block group exclusion, got %v", err)
	}
	assertActiveRows([6]int{2, 2, 2, 2, 1, 0})
	current, err := runtime.Get(ctx, tenantID, lease.ID)
	if err != nil || current.Status != workerruntime.StatusLeased || current.LeaseToken != lease.LeaseToken || current.CompletedAt != nil {
		t.Fatalf("rejected group exclusion changed prior file lease: task=%+v err=%v", current, err)
	}
	batch, err := store.GetBatch(ctx, tenantID, batchID)
	if err != nil || batch.Revision != 1 || batch.FileCount != 2 || batch.PageCount != 2 || batch.SubmissionCount != 1 {
		t.Fatalf("rejected group exclusion changed batch projection: batch=%+v err=%v", batch, err)
	}

	// 只在合成夹具中撤销评分，证明同一处置请求可以成功，且未清除历史评分这一行。
	exec(`UPDATE human_grade SET deleted_at=now() WHERE tenant_id=$1 AND id=$2`, tenantID, gradeID)
	batch, err = store.ExcludeFile(ctx, tenantID, firstFileID, actorID, input)
	if err != nil || batch.Revision != 2 || batch.FileCount != 0 || batch.PageCount != 0 || batch.SubmissionCount != 0 {
		t.Fatalf("ungraded group exclusion did not update whole batch: batch=%+v err=%v", batch, err)
	}
	assertActiveRows([6]int{0, 0, 0, 0, 0, 1})
	current, err = runtime.Get(ctx, tenantID, lease.ID)
	if err != nil || current.Status != workerruntime.StatusCancelled || current.LeaseToken != "" || current.CompletedAt == nil {
		t.Fatalf("successful exclusion left the old file lease usable: task=%+v err=%v", current, err)
	}
	var archivedScore float64
	if err = db.QueryRowContext(ctx, `SELECT score FROM human_grade WHERE tenant_id=$1 AND id=$2 AND deleted_at IS NOT NULL`, tenantID, gradeID).Scan(&archivedScore); err != nil || archivedScore != 4 {
		t.Fatalf("group exclusion changed historical scoring: score=%f err=%v", archivedScore, err)
	}
}

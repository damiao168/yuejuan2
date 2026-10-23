package server

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCaptureCollectionCompletionWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	f := e2eCreateStory056AcceptanceFixture(t, db, router, token, strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", ""), 2)
	ctx := context.Background()
	store := capture.NewPostgresStore(db)
	batch, err := store.CreateBatch(ctx, f.TenantID, f.ExamID, f.AdminID, capture.CreateBatchInput{Name: "导入结束验收", SourceType: "web_upload", IdempotencyKey: "collection-first"})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(student string) (string, string) {
		t.Helper()
		var fileID, subID, pageID string
		err := db.QueryRow(`INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,status,idempotency_key,uploaded_by) VALUES($1,$2::uuid,$3::uuid,'synthetic.pdf','application/pdf',repeat('a',64),1,1,'completed',gen_random_uuid()::text,$4::uuid) RETURNING id::text`, f.TenantID, batch.ID, f.PaperFileAssetID, f.AdminID).Scan(&fileID)
		if err != nil {
			t.Fatal(err)
		}
		err = db.QueryRow(`INSERT INTO submission(tenant_id,exam_id,student_id,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by,identity_status) VALUES($1,$2::uuid,NULLIF($3,'')::uuid,'image_upload','pages_uploaded',1,1,'passed',$4::uuid,'matched') RETURNING id::text`, f.TenantID, f.ExamID, student, f.AdminID).Scan(&subID)
		if err != nil {
			t.Fatal(err)
		}
		err = db.QueryRow(`INSERT INTO submission_page(tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2::uuid,$3::uuid,1,'accepted') RETURNING id::text`, f.TenantID, subID, f.PaperFileAssetID).Scan(&pageID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO capture_page(tenant_id,capture_batch_id,capture_file_id,source_index,sequence_no,decoded_file_asset_id,status,submission_id,submission_page_id,assigned_page_no) VALUES($1,$2::uuid,$3::uuid,1,1,$4::uuid,'ready',$5::uuid,$6::uuid,1)`, f.TenantID, batch.ID, fileID, f.PaperFileAssetID, subID, pageID)
		if err != nil {
			t.Fatal(err)
		}
		return fileID, subID
	}
	seed(f.StudentIDs[0])
	summary, err := store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ReadyCount != 1 || summary.PendingCount != 1 || summary.CanComplete {
		t.Fatalf("missing student must block: %+v", summary)
	}
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+f.ExamID+"/roster/"+f.StudentIDs[1]+"/attendance", token, `{"status":"absent","reason":"核对监考记录，学生确实缺考"}`, http.StatusOK)
	absent, err := store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if !absent.CanComplete || absent.AbsentCount != 1 || absent.RequiredCount != 1 {
		t.Fatalf("explicit absence should resolve missing sheet: %+v", absent)
	}
	e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+f.ExamID+"/roster/"+f.StudentIDs[1]+"/attendance", token, `{"status":"expected","reason":"已找到该生答卷，恢复应考并导入"}`, http.StatusOK)
	var failedID string
	err = db.QueryRow(`INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by) VALUES($1,$2::uuid,$3::uuid,'broken.pdf','application/pdf',repeat('b',64),1,'failed','broken',$4::uuid) RETURNING id::text`, f.TenantID, batch.ID, f.PaperFileAssetID, f.AdminID).Scan(&failedID)
	if err != nil {
		t.Fatal(err)
	}
	boundaries := auth.NewPostgresStore(db)
	fileScope := auth.AccessScope{TenantID: f.TenantID, ActorID: f.AdminID, ClassIDs: []string{f.ClassID}}
	checkFileBoundary := func() {
		t.Helper()
		boundary, err := boundaries.ResolveResourceBoundary(ctx, fileScope, "capture_file", failedID)
		if err != nil || boundary.ExamID != f.ExamID || boundary.SchoolID != f.SchoolID || !auth.AllowsResourceBoundary(fileScope, boundary) {
			t.Fatalf("capture file must retain its owning exam scope: boundary=%+v error=%v", boundary, err)
		}
		foreignClass := auth.AccessScope{TenantID: f.TenantID, ClassIDs: []string{"00000000-0000-0000-0000-000000000001"}}
		if auth.AllowsResourceBoundary(foreignClass, boundary) {
			t.Fatal("capture file must reject an unrelated class scope")
		}
		foreignTenant := auth.AccessScope{TenantID: "00000000-0000-0000-0000-000000000001", TenantWide: true}
		if _, err := boundaries.ResolveResourceBoundary(ctx, foreignTenant, "capture_file", failedID); !errors.Is(err, auth.ErrResourceBoundaryNotFound) {
			t.Fatalf("capture file must not resolve in a foreign tenant: %v", err)
		}
	}
	checkFileBoundary()
	// 缓存状态即使被旧回调写成 ready，完成入口也必须重新看到失败文件。
	if _, err = db.Exec(`UPDATE capture_batch SET status='ready' WHERE id=$1::uuid`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetBatchStatus(ctx, f.TenantID, batch.ID, f.AdminID, "completed", "verify"); !errors.Is(err, capture.ErrInvalidTransition) {
		t.Fatalf("failed file completion: %v", err)
	}
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-files/"+failedID+"/exclude", token, `{"reason":"误传文件，经核对不计入本次导入"}`, http.StatusOK)
	// 已排除文件仍按原考试授权恢复相同命令；其他租户或班级不能借恢复入口取得访问权限。
	checkFileBoundary()
	e2ePostJSON(t, router, http.MethodPost, "/api/v1/capture-files/"+failedID+"/exclude", token, `{"reason":"误传文件，经核对不计入本次导入"}`, http.StatusOK)
	seed(f.StudentIDs[1])
	summary, err = store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.CanComplete || summary.ReadyCount != 2 {
		t.Fatalf("ungraded complete sheets should pass: %+v", summary)
	}
	old := summary.Revision
	if _, err = db.Exec(`UPDATE capture_batch SET name=name||'更新' WHERE id=$1::uuid`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteCollection(ctx, f.TenantID, f.ExamID, f.AdminID, capture.CompleteCollectionInput{Revision: old, IdempotencyKey: "stale"}); !errors.Is(err, capture.ErrConflict) {
		t.Fatalf("stale completion: %v", err)
	}
	summary, err = store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	input := capture.CompleteCollectionInput{Revision: summary.Revision, IdempotencyKey: "finish", Reason: "确认本场齐卷"}
	locked, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locked.ExecContext(ctx, `SELECT id FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, f.TenantID, batch.ID); err != nil {
		locked.Rollback()
		t.Fatal(err)
	}
	if _, err = store.CompleteCollection(ctx, f.TenantID, f.ExamID, f.AdminID, input); !errors.Is(err, capture.ErrConflict) {
		locked.Rollback()
		t.Fatalf("concurrent batch writer must ask refresh rather than deadlock: %v", err)
	}
	if err = locked.Rollback(); err != nil {
		t.Fatal(err)
	}
	done, err := store.CompleteCollection(ctx, f.TenantID, f.ExamID, f.AdminID, input)
	if err != nil {
		t.Fatal(err)
	}
	if !done.Closed || done.ExamStatus != "grading" || done.ReadyCount != 2 {
		t.Fatalf("bad completion: %+v", done)
	}
	if _, err = store.CompleteCollection(ctx, f.TenantID, f.ExamID, f.AdminID, input); err != nil {
		t.Fatalf("same command must recover: %v", err)
	}
	if _, err = db.Exec(`UPDATE capture_file SET updated_at=now() WHERE id=$1::uuid`, failedID); err == nil {
		t.Fatal("late result must not reactivate excluded source")
	}
	supplement, err := store.CreateBatch(ctx, f.TenantID, f.ExamID, f.AdminID, capture.CreateBatchInput{Name: "补传", SourceType: "web_upload", Purpose: "supplement", Reason: "补充扫描文件", IdempotencyKey: "supplement"})
	if err != nil {
		t.Fatal(err)
	}
	if supplement.Purpose != "supplement" {
		t.Fatal("purpose lost")
	}
	batch = supplement
	duplicateFile, _ := seed("")
	summary, err = store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Closed || summary.ExamStatus != "grading" || summary.CanComplete || len(summary.Issues) == 0 {
		t.Fatalf("duplicate supplement must block without phase rollback: %+v", summary)
	}
	if _, err = store.ExcludeFile(ctx, f.TenantID, duplicateFile, f.AdminID, capture.ExcludeFileInput{Reason: "保留原答卷，排除重复扫描"}); err != nil {
		t.Fatal(err)
	}
	summary, err = store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.CanComplete {
		t.Fatalf("resolved duplicate still blocks: %+v", summary)
	}
	oldFile, _ := seed("")
	if _, err = db.Exec(`UPDATE capture_file SET sha256=repeat('c',64) WHERE id=$1::uuid`, oldFile); err != nil {
		t.Fatal(err)
	}
	var replacementID string
	err = db.QueryRow(`INSERT INTO capture_file(tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by) VALUES($1,$2::uuid,$3::uuid,'replacement.pdf','application/pdf',repeat('c',64),1,'duplicate','replacement',$4::uuid) RETURNING id::text`, f.TenantID, batch.ID, f.PaperFileAssetID, f.AdminID).Scan(&replacementID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ExcludeFile(ctx, f.TenantID, oldFile, f.AdminID, capture.ExcludeFileInput{Reason: "完整重传替代原选择", ReplacementFileID: replacementID}); err != nil {
		t.Fatal(err)
	}
	var replacementStatus string
	if err = db.QueryRow(`SELECT status FROM capture_file WHERE id=$1::uuid`, replacementID).Scan(&replacementStatus); err != nil {
		t.Fatal(err)
	}
	if replacementStatus != "uploaded" {
		t.Fatalf("replacement remains stuck as %s", replacementStatus)
	}
	summary, err = store.GetCollectionSummary(ctx, f.TenantID, f.ExamID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CanComplete {
		t.Fatal("registered replacement is not processed yet")
	}
}

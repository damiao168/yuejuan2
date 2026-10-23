package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/capture"
)

func TestExamTemplateBindingE2EWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL template binding workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	adminToken := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	fixture := e2eCreateStory056MutableTemplateFixture(
		t,
		db,
		router,
		adminToken,
		"template-routing-"+time.Now().UTC().Format("20060102150405.000000000"),
		nil,
	)
	var submissionID, capturePageID string
	// 已人工确认第 1 页，只留下模板歧义；绑定模板应恢复该页，不能另行猜测文件顺序。
	if err := db.QueryRow(`
WITH created_submission AS (
  INSERT INTO submission (
    tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,
    quality_status,collected_by,identity_status
  ) VALUES ($1::uuid,$2::uuid,'pdf_upload','pages_uploaded',1,1,'passed',$3::uuid,'matched')
  RETURNING id
), created_normalized_asset AS (
  INSERT INTO file_asset (
    tenant_id,school_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,
    size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by
  )
  SELECT $1::uuid,fa.school_id,$2::uuid,created_submission.id,'submission',created_submission.id,
         'template-routing-normalized.png','image/png',fa.size_bytes,
         md5(created_submission.id::text) || md5(created_submission.id::text || '-template-routing'),
         fa.storage_bucket,fa.storage_key || '/template-routing-normalized','private',$3::uuid
  FROM created_submission JOIN file_asset fa ON fa.tenant_id=$1::uuid AND fa.id=$4::uuid
  RETURNING id,submission_id,original_name,content_type,size_bytes,hash_sha256
), created_submission_page AS (
  INSERT INTO submission_page (
    tenant_id,submission_id,file_asset_id,page_no,status,normalized_file_asset_id,quality_status
  ) SELECT $1::uuid,submission_id,id,1,'accepted',id,'passed' FROM created_normalized_asset
  RETURNING id,submission_id
), created_batch AS (
  INSERT INTO capture_batch (tenant_id,exam_id,name,source_type,status,operator_id)
  VALUES ($1::uuid,$2::uuid,'template routing recovery','web_upload','processing',$3::uuid)
  RETURNING id
), created_file AS (
  INSERT INTO capture_file (
    tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,
    byte_size,page_count,status,idempotency_key,uploaded_by
  )
  SELECT $1::uuid,created_batch.id,created_normalized_asset.id,created_normalized_asset.original_name,
         created_normalized_asset.content_type,created_normalized_asset.hash_sha256,
         created_normalized_asset.size_bytes,1,'completed','template-routing-recovery',$3::uuid
  FROM created_batch CROSS JOIN created_normalized_asset
  RETURNING id,capture_batch_id,sha256
), created_capture_page AS (
  INSERT INTO capture_page (
    tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
    assigned_page_no,sequence_no,decoded_file_asset_id,status,revision,match_candidates,manual_override
  )
  SELECT $1::uuid,created_file.capture_batch_id,created_file.id,1,created_submission_page.submission_id,
         created_submission_page.id,1,1,created_normalized_asset.id,'needs_review',1,
         jsonb_build_array(jsonb_build_object('template_id',$5::text,'score',0.82)),jsonb_build_object('page_no',1)
  FROM created_file CROSS JOIN created_submission_page CROSS JOIN created_normalized_asset
  RETURNING id,submission_id,submission_page_id
), created_match AS (
  INSERT INTO page_template_match_run (
    tenant_id,exam_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,
    source_page_revision,processing_status,decision,candidates,created_by,completed_at
  )
  SELECT $1::uuid,$2::uuid,created_capture_page.id,created_capture_page.submission_page_id,created_normalized_asset.id,
         created_normalized_asset.hash_sha256,1,'completed','ambiguous',
         jsonb_build_array(jsonb_build_object('template_id',$5::text,'score',0.82)),$3::uuid,now()
  FROM created_capture_page CROSS JOIN created_normalized_asset
)
SELECT created_capture_page.submission_id::text,created_capture_page.id::text
FROM created_capture_page
`, fixture.TenantID, fixture.ExamID, fixture.AdminID, fixture.PaperFileAssetID, fixture.TemplateID).Scan(&submissionID, &capturePageID); err != nil {
		t.Fatalf("seed a template-review page: %v", err)
	}
	captureStore := capture.NewPostgresStore(db)
	if _, err := captureStore.QueueSubmissionPages(context.Background(), fixture.TenantID, submissionID, fixture.AdminID); !errors.Is(err, capture.ErrInvalidTransition) {
		t.Fatalf("unbound exam must not bypass a pending template decision, got %v", err)
	}

	bound := e2ePostJSON(t, router, http.MethodPut, "/api/v1/exams/"+fixture.ExamID+"/answer-sheet-template-binding", adminToken, story056JSON(t, map[string]any{
		"template_id": fixture.TemplateID, "mode": "locked_with_guard", "expected_revision": 0,
	}), http.StatusOK)["binding"].(map[string]any)
	if e2eString(t, bound, "template_id") != fixture.TemplateID || e2eString(t, bound, "template_content_hash") != fixture.TemplateContentHash || bound["revision"] != float64(1) {
		t.Fatalf("binding must pin the exact immutable template version: %#v", bound)
	}

	loaded := e2eGetJSON(t, router, "/api/v1/exams/"+fixture.ExamID+"/answer-sheet-template-binding", adminToken, http.StatusOK)["binding"].(map[string]any)
	if e2eString(t, loaded, "mode") != "locked_with_guard" || e2eString(t, loaded, "source") != "manual" {
		t.Fatalf("stored binding lost routing evidence: %#v", loaded)
	}
	runs, err := captureStore.QueueSubmissionPages(context.Background(), fixture.TenantID, submissionID, fixture.AdminID)
	if err != nil || len(runs) != 1 {
		t.Fatalf("manual exam binding must resume the exact pending page: runs=%#v err=%v", runs, err)
	}
	if runs[0].CapturePageID != capturePageID || runs[0].TemplateID != fixture.TemplateID || runs[0].TemplateContentHash != fixture.TemplateContentHash || runs[0].RoutingMode != "locked_with_guard" {
		t.Fatalf("resumed registration must pin the confirmed template version: %#v", runs[0])
	}

	e2eExpectStatus(t, router, http.MethodDelete, "/api/v1/exams/"+fixture.ExamID+"/answer-sheet-template-binding", adminToken, `{"expected_revision":1,"reason":""}`, http.StatusBadRequest)
	e2ePostJSON(t, router, http.MethodDelete, "/api/v1/exams/"+fixture.ExamID+"/answer-sheet-template-binding", adminToken, `{"expected_revision":1,"reason":"operator selected the wrong version"}`, http.StatusOK)
	unbound := e2eGetJSON(t, router, "/api/v1/exams/"+fixture.ExamID+"/answer-sheet-template-binding", adminToken, http.StatusOK)
	if unbound["binding"] != nil {
		t.Fatalf("binding should be absent after an audited release: %#v", unbound)
	}
}

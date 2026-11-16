package capture

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// 应用和撤销都会先替换配准证据，再写审计及批次汇总。末尾写入失败时，原页和原切片必须完整恢复。
func TestPostgresRegistrationCorrectionAuditFailureRollsBackApplyAndUndo(t *testing.T) {
	ctx, db := captureRecoveryRemediationDB(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"question", "answer_segment", "page_registration_correction", "capture_operation"} {
		exec(`CREATE TABLE ` + table + ` (LIKE public.` + table + ` INCLUDING ALL)`)
	}
	tenant, exam, actor, batch, file := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	submission, page, submissionPage, baseRun, correction := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	question, template, sourceAsset, oldCrop, previewAsset, previewCrop := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	exec(`INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status,file_count,page_count,submission_count,review_count)
VALUES($1,$2,$3,'synthetic correction','web_upload',$4,'needs_review',1,1,1,1)`, batch, tenant, exam, actor)
	exec(`INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,page_count,idempotency_key,uploaded_by,status)
VALUES($1,$2,$3,$4,'synthetic.png','image/png',$5,64,1,'correction-source',$6,'completed')`, file, tenant, batch, sourceAsset, hash, actor)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,source_type,status,identity_status,expected_page_count,actual_page_count,collected_by)
VALUES($1,$2,$3,'image_upload','pages_uploaded','matched',1,1,$4)`, submission, tenant, exam, actor)
	exec(`INSERT INTO capture_page(id,tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,assigned_page_no,sequence_no,decoded_file_asset_id,status,revision)
VALUES($1,$2,$3,$4,1,$5,$6,1,1,$7,'needs_review',3)`, page, tenant, batch, file, submission, submissionPage, sourceAsset)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status)
VALUES($1,$2,$3,'1','short_answer',5,1,'active')`, question, tenant, exam)
	exec(`INSERT INTO page_registration_run(id,tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,profile_version,processing_status,match_status,created_at)
VALUES($1,$2,$3,$4,$5,$6,$7,$6,1,'fixture','completed','needs_review',now()-interval '1 minute')`, baseRun, tenant, page, submissionPage, sourceAsset, hash, template)
	exec(`INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,question_version,processing_status,confidence)
VALUES($1,$2,$3,$4,'1','{"x":0.1,"y":0.2,"width":0.3,"height":0.4}','configured_answer_area','accepted',$5,$6,$7,'{"x":0.1,"y":0.2,"width":0.3,"height":0.4}','{"x":10,"y":20,"width":30,"height":40}',$8,$6,2,'completed',0.6)`, tenant, submission, submissionPage, question, template, hash, baseRun, oldCrop)
	segments, err := json.Marshal([]SegmentCropInput{{QuestionID: question, FileAssetID: previewCrop, SHA256: strings.Repeat("b", 64), NormalizedBBox: map[string]any{"x": 0, "y": 0, "width": 1, "height": 1}, PixelBBox: map[string]any{"x": 0, "y": 0, "width": 100, "height": 200}}})
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO page_registration_correction(id,tenant_id,capture_page_id,base_registration_run_id,source_page_revision,template_id,template_content_hash,page_no,source_points,template_points,status,points_hash,preview_registered_file_asset_id,preview_segments,source_to_template_matrix,template_to_source_matrix,validation_report,created_by,expires_at)
VALUES($1,$2,$3,$4,3,$5,$6,1,'[{"x":0,"y":0},{"x":1,"y":0},{"x":1,"y":1},{"x":0,"y":1}]','[{"x":0,"y":0},{"x":1,"y":0},{"x":1,"y":1},{"x":0,"y":1}]','preview_ready',$6,$7,$8::jsonb,'[[1,0,0],[0,1,0],[0,0,1]]','[[1,0,0],[0,1,0],[0,0,1]]','{"valid":true}',$9,now()+interval '1 hour')`, correction, tenant, page, baseRun, template, hash, previewAsset, string(segments), actor)
	store := NewPostgresStore(db)
	// 快照忽略更新时间，保留页面版本、切片内容、运行、校正回执、批次计数和操作审计的全部业务字段。
	readFacts := func() string {
		t.Helper()
		var state string
		err := db.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'page',(SELECT to_jsonb(p)-'updated_at'-'created_at' FROM capture_page p WHERE tenant_id=$1 AND id=$2),
 'segment',(SELECT to_jsonb(s)-'updated_at'-'created_at' FROM answer_segment s WHERE tenant_id=$1 AND question_id=$3),
 'runs',(SELECT jsonb_agg(to_jsonb(r)-'updated_at' ORDER BY r.created_at,r.id) FROM page_registration_run r WHERE tenant_id=$1 AND capture_page_id=$2),
 'correction',(SELECT to_jsonb(c)-'updated_at'-'created_at' FROM page_registration_correction c WHERE tenant_id=$1 AND id=$4),
 'batch',(SELECT to_jsonb(b)-'updated_at'-'created_at' FROM capture_batch b WHERE tenant_id=$1 AND id=$5),
 'audit',(SELECT count(*) FROM capture_operation WHERE tenant_id=$1)
)::text`, tenant, page, question, correction, batch).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}

	exec(`CREATE FUNCTION reject_correction_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM page_registration_correction WHERE tenant_id=NEW.tenant_id AND id=NEW.target_id AND status IN ('applied','undone')) THEN
    RAISE EXCEPTION 'audit failure reached before correction mutation';
  END IF;
  RAISE EXCEPTION 'synthetic correction audit failure';
END $$`)
	setAuditFailure := func() {
		t.Helper()
		exec(`CREATE TRIGGER reject_correction_audit BEFORE INSERT ON capture_operation FOR EACH ROW EXECUTE FUNCTION reject_correction_audit()`)
	}
	assertAuditFailure := func(err error) {
		t.Helper()
		var postgresErr *pgconn.PgError
		if !errors.As(err, &postgresErr) || postgresErr.Message != "synthetic correction audit failure" {
			t.Fatalf("expected late audit failure, got %v", err)
		}
	}

	before := readFacts()
	setAuditFailure()
	_, err = store.ApplyRegistrationCorrection(ctx, tenant, correction, actor, RegistrationCorrectionDecisionInput{Revision: 1, Reason: "reviewed corrected page"})
	assertAuditFailure(err)
	if after := readFacts(); after != before {
		t.Fatal("failed apply changed page, segment, run, receipt, batch or audit facts")
	}
	exec(`DROP TRIGGER reject_correction_audit ON capture_operation`)
	applied, err := store.ApplyRegistrationCorrection(ctx, tenant, correction, actor, RegistrationCorrectionDecisionInput{Revision: 1, Reason: "reviewed corrected page"})
	if err != nil || applied.Status != "applied" || applied.Revision != 2 || applied.AppliedRegistrationRunID == "" {
		t.Fatalf("same apply request could not recover: result=%+v err=%v", applied, err)
	}
	var currentCrop, currentRun, pageStatus, baseStatus string
	var pageRevision, operations int
	if err := db.QueryRowContext(ctx, `SELECT s.crop_file_asset_id::text,s.registration_run_id::text,p.status,p.revision,r.processing_status,(SELECT count(*) FROM capture_operation WHERE tenant_id=$1)
FROM answer_segment s JOIN capture_page p ON p.tenant_id=s.tenant_id AND p.submission_page_id=s.submission_page_id JOIN page_registration_run r ON r.tenant_id=s.tenant_id AND r.id=$3
WHERE s.tenant_id=$1 AND s.question_id=$2`, tenant, question, baseRun).Scan(&currentCrop, &currentRun, &pageStatus, &pageRevision, &baseStatus, &operations); err != nil || currentCrop != previewCrop || currentRun != applied.AppliedRegistrationRunID || pageStatus != "ready" || pageRevision != 4 || baseStatus != "invalidated" || operations != 1 {
		t.Fatalf("apply did not atomically replace evidence: crop=%s run=%s page=%s/%d base=%s audits=%d err=%v", currentCrop, currentRun, pageStatus, pageRevision, baseStatus, operations, err)
	}

	beforeUndo := readFacts()
	if _, err := store.UndoRegistrationCorrection(ctx, tenant, correction, actor, RegistrationCorrectionDecisionInput{Revision: 1, Reason: "undo reviewed correction"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale undo revision accepted: %v", err)
	}
	setAuditFailure()
	_, err = store.UndoRegistrationCorrection(ctx, tenant, correction, actor, RegistrationCorrectionDecisionInput{Revision: 2, Reason: "undo reviewed correction"})
	assertAuditFailure(err)
	if after := readFacts(); after != beforeUndo {
		t.Fatal("failed or stale undo changed published correction facts")
	}
	exec(`DROP TRIGGER reject_correction_audit ON capture_operation`)
	undone, err := store.UndoRegistrationCorrection(ctx, tenant, correction, actor, RegistrationCorrectionDecisionInput{Revision: 2, Reason: "undo reviewed correction"})
	if err != nil || undone.Status != "undone" || undone.Revision != 3 {
		t.Fatalf("same undo request could not recover: result=%+v err=%v", undone, err)
	}
	var segmentVersion int
	var cropHash string
	if err := db.QueryRowContext(ctx, `SELECT s.crop_file_asset_id::text,s.crop_sha256,s.registration_run_id::text,s.question_version,p.status,p.revision,r.processing_status,(SELECT count(*) FROM capture_operation WHERE tenant_id=$1)
FROM answer_segment s JOIN capture_page p ON p.tenant_id=s.tenant_id AND p.submission_page_id=s.submission_page_id JOIN page_registration_run r ON r.tenant_id=s.tenant_id AND r.id=$3
WHERE s.tenant_id=$1 AND s.question_id=$2`, tenant, question, baseRun).Scan(&currentCrop, &cropHash, &currentRun, &segmentVersion, &pageStatus, &pageRevision, &baseStatus, &operations); err != nil || currentCrop != oldCrop || cropHash != hash || currentRun != baseRun || segmentVersion != 2 || pageStatus != "needs_review" || pageRevision != 5 || baseStatus != "completed" || operations != 2 {
		t.Fatalf("undo did not restore original evidence: crop=%s hash=%s run=%s version=%d page=%s/%d base=%s audits=%d err=%v", currentCrop, cropHash, currentRun, segmentVersion, pageStatus, pageRevision, baseStatus, operations, err)
	}
}

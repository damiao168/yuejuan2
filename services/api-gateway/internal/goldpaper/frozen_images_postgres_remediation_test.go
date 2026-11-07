package goldpaper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/files"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 只在显式合成实例的随机 schema 克隆空表类型；实际执行 78/79/216 迁移及生产 Gold 写入/读取，不触碰业务记录。
func TestPostgresFrozenGoldMigrationAndSourceReplacement(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*config)
	defer admin.Close()
	schema := "gold_frozen_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	config.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*config)
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("SQL: %v\n%s", err, query)
		}
	}
	for _, table := range []string{"tenant", "exam", "question", "submission", "app_user", "exam_question_snapshot", "answer_segment", "file_asset", "page_registration_run", "page_registration_correction", "question_grade", "human_grade", "seed_task"} {
		exec(`CREATE TABLE ` + table + ` AS TABLE public.` + table + ` WITH NO DATA`)
		if table == "tenant" {
			exec(`ALTER TABLE tenant ADD UNIQUE(id)`)
		} else {
			exec(`ALTER TABLE ` + table + ` ADD UNIQUE(tenant_id,id)`)
		}
	}
	apply := func(name string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		exec(string(data))
	}
	apply("000078_storyA08_gold_papers.sql")
	apply("000079_storyA09_grader_calibration.sql")
	tenant, exam, question, submission, snapshot, segment, run, asset, grade, nominator, approver, grader := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert := func(table string, fields map[string]any) {
		t.Helper()
		if fields["id"] == nil {
			fields["id"] = uuid.NewString()
		}
		if table != "tenant" {
			fields["tenant_id"] = tenant
		}
		fields["created_at"], fields["updated_at"] = time.Now().UTC(), time.Now().UTC()
		raw, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO `+table+` SELECT * FROM jsonb_populate_record(NULL::`+table+`,$1::jsonb)`, raw)
	}
	insert("tenant", map[string]any{"id": tenant})
	insert("exam", map[string]any{"id": exam})
	insert("question", map[string]any{"id": question, "exam_id": exam, "score": 5})
	insert("submission", map[string]any{"id": submission, "exam_id": exam})
	for _, actor := range []string{nominator, approver, grader} {
		insert("app_user", map[string]any{"id": actor})
	}
	insert("exam_question_snapshot", map[string]any{"id": snapshot, "exam_id": exam, "question_id": question, "snapshot_version": 1, "profile_snapshot_json": map[string]any{"subject_code": "general"}, "archetype_code": "short_constructed", "risk_tier": "R2", "rubric_snapshot_json": map[string]any{}})
	digest := sha256.Sum256([]byte("original-answer"))
	hash := hex.EncodeToString(digest[:])
	insert("page_registration_run", map[string]any{"id": run, "processing_status": "completed"})
	insert("file_asset", map[string]any{"id": asset, "exam_id": exam, "owner_type": "answer_segment_crop", "owner_id": run, "hash_sha256": hash, "content_type": "image/png", "size_bytes": 15, "lifecycle_status": "active", "original_name": "answer.png", "storage_bucket": "synthetic", "storage_key": run, "visibility": "private", "revision": 1, "delete_attempts": 0, "legal_hold": false})
	insert("answer_segment", map[string]any{"id": segment, "submission_id": submission, "question_id": question, "registration_run_id": run, "crop_file_asset_id": asset, "crop_sha256": hash, "normalized_bbox": map[string]any{"answer_scope": "question"}, "processing_status": "completed"})
	insert("question_grade", map[string]any{"id": grade, "answer_segment_id": segment})
	var goldID string
	if err := db.QueryRowContext(ctx, `INSERT INTO grading_gold_paper(tenant_id,exam_id,question_id,submission_id,nominated_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, tenant, exam, question, submission, nominator).Scan(&goldID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO grading_gold_paper_version(tenant_id,gold_paper_id,version,exam_question_snapshot_id,reference_score,max_score,rubric_snapshot_json,explanation,source_grade_ids_json,nominated_by,approved_by,approved_at) VALUES($1,$2,1,$3,2,5,'{}','historical approved',$4,$5,$6,now()),($1,$2,2,$3,2,5,'{}','historical pending',$4,$5,NULL,NULL)`, tenant, goldID, snapshot, `["`+grade+`"]`, nominator, approver)
	exec(`UPDATE grading_gold_paper SET active_version=1,status='active' WHERE id=$1`, goldID)
	sessionID, openSessionID := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO grader_calibration_session(id,tenant_id,exam_id,question_id,grader_id,exam_question_snapshot_id,gold_version,policy_snapshot_json,sample_manifest_json,status,metrics_json,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,'{}','[{}]','passed','{}',now()),($8,$2,$3,$4,$5,$6,$7,'{}','[{}]','in_progress',NULL,NULL)`, sessionID, tenant, exam, question, grader, snapshot, strings.Repeat("a", 64), openSessionID)
	exec(`INSERT INTO grader_question_qualification(tenant_id,exam_id,question_id,grader_id,status,valid_until,calibration_session_id,gold_version,metric_snapshot_json) VALUES($1,$2,$3,$4,'qualified',now()+interval '1 day',$5,$6,'{}')`, tenant, exam, question, grader, sessionID, strings.Repeat("a", 64))
	insert("seed_task", map[string]any{"status": "in_progress", "revision": 1})
	apply("000216_gold_paper_frozen_images.sql")
	// 本夹具验证 216 的图像冻结；当前仓库另需 221 的分池字段，沿用已迁移 public 的登记表结构。
	// 221 的完整迁移和运行角色隔离由真实数据库测试覆盖，不把这里的空表克隆计为迁移验证。
	exec(`ALTER TABLE grading_gold_paper ADD COLUMN quality_pool TEXT NOT NULL DEFAULT 'calibration'`)
	exec(`CREATE TABLE gold_quality_image_pool (LIKE public.gold_quality_image_pool INCLUDING ALL)`)
	store := NewPostgresStore(db)
	history, err := store.Get(ctx, tenant, goldID)
	if err != nil || history.Status != StatusPendingApproval || history.ActiveVersion != 0 || history.Versions[0].ApprovedBy != approver || history.Versions[0].ApprovedAt == nil || history.Versions[1].ImageInvalidatedAt == nil || history.AnswerImageURL != "" {
		t.Fatalf("history was rewritten or retained usable identity: %+v %v", history, err)
	}
	for _, tc := range []struct{ query, want string }{{`SELECT status FROM grader_question_qualification`, "revoked"}, {`SELECT status FROM grader_calibration_session WHERE id='` + openSessionID + `'`, "invalidated"}, {`SELECT status FROM grader_calibration_session WHERE id='` + sessionID + `'`, "passed"}, {`SELECT status FROM seed_task`, "expired"}} {
		var value string
		if err := db.QueryRowContext(ctx, tc.query).Scan(&value); err != nil || value != tc.want {
			t.Fatalf("migration state=%s want=%s err=%v", value, tc.want, err)
		}
	}
	if _, err := store.Approve(ctx, tenant, goldID, approver, 2); err != ErrImageUnavailable {
		t.Fatalf("invalidated historical proposal approved: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE grading_gold_paper SET active_version=1,status='active' WHERE id=$1`, goldID); err == nil {
		t.Fatal("unfrozen historical approval reactivated")
	}
	item, err := store.CreateVersion(ctx, tenant, goldID, nominator, CreateVersionInput{ReferenceScore: 2, Explanation: "frozen new nomination", SourceGradeIDs: []string{grade}})
	if err != nil {
		t.Fatal(err)
	}
	item, err = store.Approve(ctx, tenant, goldID, approver, 3)
	if err != nil || item.ActiveVersion != 3 || item.Versions[2].ImageBinding.AssetID != asset || item.AnswerImageURL != VersionImageURL(goldID, 3) {
		t.Fatalf("new version not frozen: %+v %v", item, err)
	}
	objects := files.NewMemoryObjectStorage()
	if err := objects.Put(ctx, "synthetic", run, strings.NewReader("original-answer"), 15, "image/png"); err != nil {
		t.Fatal(err)
	}
	images := NewImageService(store, files.NewPostgresStore(db), objects)
	assertImage := func(number int, want string) {
		t.Helper()
		resource, err := images.ReadVersionImage(ctx, tenant, goldID, number)
		if err != nil {
			t.Fatal(err)
		}
		body, err := resource.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		raw, err := io.ReadAll(body)
		if err != nil || string(raw) != want {
			t.Fatalf("PG frozen image version %d: %q %v", number, raw, err)
		}
	}
	assertImage(3, "original-answer")
	if _, err := db.ExecContext(ctx, `UPDATE grading_gold_paper_version SET frozen_crop_sha256=$2 WHERE gold_paper_id=$1 AND version=3`, goldID, strings.Repeat("b", 64)); err == nil {
		t.Fatal("approved frozen identity mutated")
	}
	correctedAsset, correctedRun := uuid.NewString(), uuid.NewString()
	correctedDigest := sha256.Sum256([]byte("corrected-answer"))
	correctedHash := hex.EncodeToString(correctedDigest[:])
	insert("page_registration_run", map[string]any{"id": correctedRun, "processing_status": "completed"})
	insert("file_asset", map[string]any{"id": correctedAsset, "exam_id": exam, "owner_type": "answer_segment_crop", "owner_id": correctedRun, "hash_sha256": correctedHash, "content_type": "image/png", "size_bytes": 16, "lifecycle_status": "active", "original_name": "answer.png", "storage_bucket": "synthetic", "storage_key": correctedRun, "visibility": "private", "revision": 1, "delete_attempts": 0, "legal_hold": false})
	if err := objects.Put(ctx, "synthetic", correctedRun, strings.NewReader("corrected-answer"), 16, "image/png"); err != nil {
		t.Fatal(err)
	}
	// 与校正 UPSERT 相同地保留 segment ID 并替换裁图；生产读取必须完全脱离这一可变关联。
	exec(`UPDATE answer_segment SET registration_run_id=$2,crop_file_asset_id=$3,crop_sha256=$4 WHERE id=$1`, segment, correctedRun, correctedAsset, correctedHash)
	exec(`UPDATE page_registration_run SET processing_status='invalidated' WHERE id=$1`, run)
	item, err = store.Get(ctx, tenant, goldID)
	if err != nil || item.Versions[2].ImageBinding.AssetID != asset || item.Versions[2].ImageBinding.SHA256 != hash || item.AnswerImageURL != VersionImageURL(goldID, 3) {
		t.Fatalf("approved version followed correction: %+v %v", item, err)
	}
	assertImage(3, "original-answer")
	item, err = store.CreateVersion(ctx, tenant, goldID, nominator, CreateVersionInput{ReferenceScore: 3, Explanation: "new corrected image", SourceGradeIDs: []string{grade}})
	if err != nil || item.Versions[3].ImageBinding.AssetID != correctedAsset {
		t.Fatalf("new version did not bind new source: %+v %v", item, err)
	}
	assertImage(4, "corrected-answer")
	exec(`UPDATE file_asset SET lifecycle_status='quarantined' WHERE id=$1`, asset)
	if _, err := images.ReadVersionImage(ctx, tenant, goldID, 3); err != ErrImageUnavailable {
		t.Fatalf("quarantined frozen asset read: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE grading_gold_paper_version SET frozen_crop_sha256=$2 WHERE gold_paper_id=$1 AND version=4`, goldID, strings.Repeat("c", 64)); err == nil {
		t.Fatal("pending frozen identity mutated")
	}
	// 校正应用使用 preview 所有者；提名仍须核对该校正与当前配准运行的真实绑定。
	secondSubmission, secondSegment, secondGrade, correction, previewAsset := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	insert("submission", map[string]any{"id": secondSubmission, "exam_id": exam})
	insert("page_registration_correction", map[string]any{"id": correction, "applied_registration_run_id": correctedRun, "status": "applied"})
	insert("file_asset", map[string]any{"id": previewAsset, "exam_id": exam, "owner_type": "page_registration_correction_preview", "owner_id": correction, "hash_sha256": correctedHash, "content_type": "image/png", "size_bytes": 16, "lifecycle_status": "active", "original_name": "answer.png", "storage_bucket": "synthetic", "storage_key": correctedRun, "visibility": "private", "revision": 1, "delete_attempts": 0, "legal_hold": false})
	insert("answer_segment", map[string]any{"id": secondSegment, "submission_id": secondSubmission, "question_id": question, "registration_run_id": correctedRun, "crop_file_asset_id": previewAsset, "crop_sha256": correctedHash, "normalized_bbox": map[string]any{"answer_scope": "grading_group"}, "processing_status": "completed"})
	insert("question_grade", map[string]any{"id": secondGrade, "answer_segment_id": secondSegment})
	nominate := func() (GoldPaper, error) {
		return store.Nominate(ctx, tenant, exam, question, nominator, NominateInput{SubmissionID: secondSubmission, ReferenceScore: 3, Explanation: "corrected preview reference", SourceGradeIDs: []string{secondGrade}})
	}
	if _, err := nominate(); err != ErrNotFound {
		t.Fatalf("whole-region crop accepted as single-question Gold: %v", err)
	}
	exec(`UPDATE answer_segment SET normalized_bbox='{"answer_scope":"question"}' WHERE id=$1`, secondSegment)
	exec(`UPDATE page_registration_correction SET status='undone' WHERE id=$1`, correction)
	if _, err := nominate(); err != ErrNotFound {
		t.Fatalf("unapplied correction preview accepted: %v", err)
	}
	exec(`UPDATE page_registration_correction SET status='applied' WHERE id=$1`, correction)
	previewGold, err := nominate()
	if err != nil || previewGold.Versions[0].ImageBinding.OwnerType != "page_registration_correction_preview" || previewGold.Versions[0].ImageBinding.OwnerID != correction {
		t.Fatalf("applied correction nomination failed: %+v %v", previewGold, err)
	}
	resource, err := images.ReadVersionImage(ctx, tenant, previewGold.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, err := resource.Open(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil || string(raw) != "corrected-answer" {
		t.Fatalf("preview identity read: %q %v", raw, err)
	}
}

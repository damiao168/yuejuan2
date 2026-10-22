package capture

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// 用独立 schema 重现乱序解码、缺页与跨次上传；质检外键在归组前后都由 PostgreSQL 检查。
func TestPostgresCaptureUploadGrouping(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PostgreSQL fixture not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("capture_upload_group_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddl := []string{
		`CREATE TABLE capture_batch(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,exam_id uuid,name text,source_type text,status text DEFAULT 'uploading',revision int DEFAULT 1,operator_id uuid,scanner_device text,file_count int DEFAULT 0,page_count int DEFAULT 0,submission_count int DEFAULT 0,normal_count int DEFAULT 0,review_count int DEFAULT 0,failed_count int DEFAULT 0,started_at timestamptz,completed_at timestamptz,created_at timestamptz DEFAULT now(),updated_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE capture_file(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,capture_batch_id uuid,file_asset_id uuid,original_name text,content_type text,sha256 text,byte_size bigint,page_count int DEFAULT 0,status text,error_code text,idempotency_key text,uploaded_by uuid,created_at timestamptz DEFAULT now(),updated_at timestamptz,deleted_at timestamptz,UNIQUE(tenant_id,capture_batch_id,idempotency_key))`,
		`CREATE TABLE submission(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,exam_id uuid,source_type text,status text,expected_page_count int,actual_page_count int,quality_status text,quality_issues jsonb,collected_by uuid,identity_status text DEFAULT 'unassigned',identity_revision int DEFAULT 1,student_id uuid,candidate_no text,identity_evidence jsonb DEFAULT '{}',created_at timestamptz DEFAULT now(),updated_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE submission_page(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,submission_id uuid REFERENCES submission(id),file_asset_id uuid,page_no int,status text,quality_status text,quality_override jsonb,quality_issues jsonb,latest_quality_run_id uuid,created_at timestamptz DEFAULT now(),updated_at timestamptz,deleted_at timestamptz,UNIQUE(tenant_id,submission_id,page_no),UNIQUE(tenant_id,submission_id,id))`,
		`CREATE TABLE capture_page(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,capture_batch_id uuid,capture_file_id uuid,source_index int,submission_id uuid REFERENCES submission(id),submission_page_id uuid REFERENCES submission_page(id),assigned_page_no int,sequence_no int,rotation_degrees int DEFAULT 0,decoded_file_asset_id uuid,barcode_student_id uuid,barcode_template_id uuid,sheet_serial uuid,status text,duplicate_of_page_id uuid,revision int DEFAULT 1,page_identity jsonb DEFAULT '{}',match_candidates jsonb DEFAULT '[]',manual_override jsonb DEFAULT '{}',created_at timestamptz DEFAULT now(),updated_at timestamptz,deleted_at timestamptz,UNIQUE(tenant_id,capture_file_id,source_index))`,
		`CREATE TABLE submission_page_quality_run(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,submission_id uuid,submission_page_id uuid,source_file_asset_id uuid,source_sha256 text,processing_status text,profile_name text,profile_version text,profile_config_hash text,metric_schema_version text,report_schema_version text,FOREIGN KEY(tenant_id,submission_id,submission_page_id) REFERENCES submission_page(tenant_id,submission_id,id))`,
		`CREATE TABLE agent_worker_task(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid,task_type text,queue_name text,source_type text,source_id uuid,priority int,payload jsonb,payload_schema_version text,idempotency_key text,dedupe_key text,max_attempts int,retry_backoff_seconds int,created_by uuid)`,
	}
	for _, statement := range ddl {
		if _, err = db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	migrationPath := os.Getenv("YUEJUAN_UPLOAD_GROUP_MIGRATION_PATH")
	if migrationPath == "" {
		migrationPath = "../../migrations/000201_capture_upload_groups.sql"
	}
	migration, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	tenant, actor, exam := uuid.NewString(), uuid.NewString(), uuid.NewString()
	newBatch := func() string {
		var id string
		if err := db.QueryRowContext(ctx, `INSERT INTO capture_batch(tenant_id,exam_id,operator_id) VALUES($1,$2,$3) RETURNING id::text`, tenant, exam, actor).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	register := func(batch, group string, count, index, n int) File {
		asset := FileAssetSnapshot{ID: uuid.NewString(), SizeBytes: 100, SHA256: uuid.NewString(), OriginalName: "scan.png", ContentType: "image/png"}
		f, err := store.RegisterFile(ctx, tenant, batch, actor, RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: uuid.NewString(), UploadGroupID: group, UploadGroupFileCount: count, UploadGroupFileIndex: index, PagesPerStudent: n}, asset)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `UPDATE capture_file SET status='queued' WHERE id=$1::uuid`, f.ID); err != nil {
			t.Fatal(err)
		}
		return f
	}
	decode := func(f File, count int) {
		pages := []DecodedPageInput{}
		for i := 1; i <= count; i++ {
			pages = append(pages, DecodedPageInput{SourceIndex: i, FileAssetID: uuid.NewString(), SHA256: fmt.Sprintf("decoded-%s-%d", f.ID, i), Width: 3304, Height: 2338})
		}
		if _, err := store.ApplyFileResult(ctx, tenant, f.ID, pages); err != nil {
			t.Fatal(err)
		}
	}
	countRuns := func(batch string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM submission_page_quality_run r JOIN capture_page cp ON cp.submission_page_id=r.submission_page_id WHERE cp.capture_batch_id=$1`, batch).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Run("reverse_completion_and_idempotent_replay", func(t *testing.T) {
		batch, group := newBatch(), uuid.NewString()
		front, back := register(batch, group, 2, 1, 2), register(batch, group, 2, 2, 2)
		decode(back, 1)
		if n := countRuns(batch); n != 0 {
			t.Fatalf("quality started before group complete: %d", n)
		}
		decode(front, 1)
		decode(front, 1)
		pages, err := store.ListPages(ctx, tenant, batch)
		if err != nil {
			t.Fatal(err)
		}
		if len(pages) != 2 || pages[0].SubmissionID != pages[1].SubmissionID {
			t.Fatalf("expected one two-page answer: %+v", pages)
		}
		for _, p := range pages {
			want := 1
			if p.CaptureFileID == back.ID {
				want = 2
			}
			if p.AssignedPageNo != want || p.PageIdentity["grouping_status"] != "complete" {
				t.Fatalf("completion order changed grouping: %+v", p)
			}
		}
		if n := countRuns(batch); n != 2 {
			t.Fatalf("expected two quality tasks after replay: %d", n)
		}
	})
	t.Run("single_sided_is_two_students", func(t *testing.T) {
		batch, group := newBatch(), uuid.NewString()
		a, b := register(batch, group, 2, 1, 1), register(batch, group, 2, 2, 1)
		decode(a, 1)
		decode(b, 1)
		pages, _ := store.ListPages(ctx, tenant, batch)
		if len(pages) != 2 || pages[0].SubmissionID == pages[1].SubmissionID {
			t.Fatal("single sides incorrectly merged")
		}
	})
	t.Run("separate_selections_never_share_leftovers", func(t *testing.T) {
		batch := newBatch()
		for i := 0; i < 2; i++ {
			f := register(batch, uuid.NewString(), 1, 1, 2)
			decode(f, 1)
		}
		if countRuns(batch) != 0 {
			t.Fatal("incomplete groups started quality")
		}
		pages, _ := store.ListPages(ctx, tenant, batch)
		if len(pages) != 2 || pages[0].SubmissionID == pages[1].SubmissionID {
			t.Fatal("different selections mixed")
		}
		for _, p := range pages {
			if p.PageIdentity["grouping_status"] != "incomplete_pages" {
				t.Fatal("missing page not visible")
			}
			tx, _ := db.BeginTx(ctx, nil)
			runs, err := QueueSubmissionPagesInTx(ctx, tx, tenant, p.SubmissionID, actor)
			tx.Rollback()
			if err != nil || len(runs) != 0 {
				t.Fatalf("incomplete group entered registration: %v", err)
			}
		}
	})
	t.Run("one_pdf_four_pages_is_two_students", func(t *testing.T) {
		batch := newBatch()
		f := register(batch, uuid.NewString(), 1, 1, 2)
		decode(f, 4)
		pages, _ := store.ListPages(ctx, tenant, batch)
		groups := map[string]int{}
		for _, p := range pages {
			groups[p.SubmissionID]++
		}
		if len(groups) != 2 || countRuns(batch) != 4 {
			t.Fatalf("wrong multipage grouping: %+v", groups)
		}
		for _, n := range groups {
			if n != 2 {
				t.Fatal("wrong page count")
			}
		}
	})
	t.Run("missing_upload_waits_and_metadata_is_immutable", func(t *testing.T) {
		batch, group := newBatch(), uuid.NewString()
		f := register(batch, group, 2, 1, 2)
		decode(f, 1)
		if countRuns(batch) != 0 {
			t.Fatal("started before second registered file")
		}
		asset := FileAssetSnapshot{ID: uuid.NewString(), SizeBytes: 100, SHA256: uuid.NewString()}
		_, err := store.RegisterFile(ctx, tenant, batch, actor, RegisterFileInput{FileAssetID: asset.ID, IdempotencyKey: uuid.NewString(), UploadGroupID: group, UploadGroupFileCount: 2, UploadGroupFileIndex: 2, PagesPerStudent: 1}, asset)
		if err != ErrConflict {
			t.Fatalf("group settings changed: %v", err)
		}
	})
}

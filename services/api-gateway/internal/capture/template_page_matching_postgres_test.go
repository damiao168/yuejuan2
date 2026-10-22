package capture

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

// 只在独立 schema 中执行本次路由和回写链路，不读取或修改真实考试。
func TestPostgresTemplatePageMatchingPersistsReverseSideAndKeepsQualityAndExplicitNumbers(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("capture_page_match_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("remove fixture schema: %v", err)
		}
	}()
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		t.Fatalf("fixture requires PostgreSQL URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, ddl := range []string{
		`CREATE TABLE file_asset(tenant_id uuid,id uuid,hash_sha256 text,content_type text)`,
		`CREATE TABLE exam_paper(tenant_id uuid,id uuid,file_asset_id uuid)`,
		`CREATE TABLE answer_sheet_template(tenant_id uuid,id uuid,exam_id uuid,content_hash text,name text,version_no int,layout jsonb,exam_paper_id uuid,status text,deleted_at timestamptz)`,
		`CREATE TABLE exam_answer_sheet_template_binding_revision(tenant_id uuid,exam_id uuid,last_revision int,updated_at timestamptz,UNIQUE(tenant_id,exam_id))`,
		`CREATE TABLE exam_answer_sheet_template_binding(tenant_id uuid,exam_id uuid,template_id uuid,template_content_hash text,mode text)`,
		`CREATE TABLE submission(tenant_id uuid,id uuid,exam_id uuid,identity_status text DEFAULT 'unknown',deleted_at timestamptz)`,
		`CREATE TABLE submission_page(tenant_id uuid,id uuid,submission_id uuid,page_no int,normalized_file_asset_id uuid,quality_status text,updated_at timestamptz,UNIQUE(tenant_id,submission_id,page_no))`,
		`CREATE TABLE capture_batch(tenant_id uuid,id uuid,status text,revision int DEFAULT 1,file_count int,page_count int,submission_count int,normal_count int,failed_count int,review_count int,updated_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE capture_file(tenant_id uuid,id uuid,capture_batch_id uuid,status text,deleted_at timestamptz)`,
		`CREATE TABLE capture_page(tenant_id uuid,id uuid,capture_batch_id uuid,submission_id uuid,submission_page_id uuid,assigned_page_no int,revision int,status text,manual_override jsonb DEFAULT '{}',page_identity jsonb DEFAULT '{}',match_candidates jsonb DEFAULT '[]',barcode_template_id uuid,sheet_serial uuid,sequence_no int DEFAULT 1,updated_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE answer_sheet_print_sheet(tenant_id uuid,id uuid,template_id uuid,template_content_hash text)`,
		`CREATE TABLE page_template_match_run(id uuid DEFAULT gen_random_uuid(),tenant_id uuid,exam_id uuid,capture_page_id uuid,submission_page_id uuid,source_file_asset_id uuid,source_sha256 text,source_page_revision int,processing_status text DEFAULT 'processing',decision text,selected_template_id uuid,selected_template_content_hash text,score numeric,margin numeric,candidates jsonb DEFAULT '[]',profile_version text DEFAULT 'phash-orb-ransac-v1',runtime_task_id uuid,error_code text,error_detail jsonb,created_by uuid,created_at timestamptz DEFAULT now(),updated_at timestamptz,completed_at timestamptz,deleted_at timestamptz,result_version text,duration_ms int)`,
		`CREATE TABLE page_registration_run(id uuid DEFAULT gen_random_uuid(),tenant_id uuid,capture_page_id uuid,submission_page_id uuid,source_file_asset_id uuid,source_sha256 text,template_id uuid,template_content_hash text,routing_mode text,guard_report jsonb DEFAULT '{}',page_no int,processing_status text,match_status text,confidence numeric,method text,profile_version text,source_to_template_matrix jsonb DEFAULT '[]',template_to_source_matrix jsonb DEFAULT '[]',feature_count int DEFAULT 0,match_count int DEFAULT 0,inlier_count int DEFAULT 0,inlier_ratio numeric,reprojection_error numeric,registered_file_asset_id uuid,runtime_task_id uuid,error_code text,created_at timestamptz DEFAULT now(),updated_at timestamptz,started_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE agent_worker_task(id uuid DEFAULT gen_random_uuid(),tenant_id uuid,task_type text,queue_name text,source_type text,source_id uuid,priority int,payload jsonb,payload_schema_version text,idempotency_key text,dedupe_key text,max_attempts int,retry_backoff_seconds int,created_by uuid)`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	const tenant = "00000000-0000-0000-0000-000000000301"
	const exam = "00000000-0000-0000-0000-000000000302"
	const actor = "00000000-0000-0000-0000-000000000303"
	const templateID = "00000000-0000-0000-0000-000000000304"
	const submissionID = "00000000-0000-0000-0000-000000000305"
	const pageID = "00000000-0000-0000-0000-000000000306"
	const batchID = "00000000-0000-0000-0000-000000000307"
	const assetID = "00000000-0000-0000-0000-000000000308"
	const captureID = "00000000-0000-0000-0000-000000000309"
	for _, scenario := range []string{"reverse_side", "quality_failed", "source_changed", "manual", "controlled"} {
		t.Run(scenario, func(t *testing.T) {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, seed := range []struct {
				query string
				args  []any
			}{
				{`INSERT INTO file_asset VALUES($1::uuid,$2::uuid,'source-hash','image/png')`, []any{tenant, assetID}},
				{`INSERT INTO exam_paper VALUES($1::uuid,$2::uuid,$2::uuid)`, []any{tenant, assetID}},
				{`INSERT INTO answer_sheet_template VALUES($1::uuid,$2::uuid,$3::uuid,'template-hash','two pages',1,'{"pages":[{"page_no":1,"width":420,"height":600},{"page_no":2,"width":420,"height":600}]}',$4::uuid,'locked',NULL)`, []any{tenant, templateID, exam, assetID}},
				{`INSERT INTO exam_answer_sheet_template_binding VALUES($1::uuid,$2::uuid,$3::uuid,'template-hash','bound_auto')`, []any{tenant, exam, templateID}},
				{`INSERT INTO submission(tenant_id,id,exam_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, []any{tenant, submissionID, exam}},
				{`INSERT INTO submission_page VALUES($1::uuid,$2::uuid,$3::uuid,1,$4::uuid,'passed',now())`, []any{tenant, pageID, submissionID, assetID}},
				{`INSERT INTO capture_batch(tenant_id,id,status) VALUES($1::uuid,$2::uuid,'processing')`, []any{tenant, batchID}},
				{`INSERT INTO capture_page(tenant_id,id,capture_batch_id,submission_id,submission_page_id,assigned_page_no,revision,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,1,3,'normalized')`, []any{tenant, captureID, batchID, submissionID, pageID}},
			} {
				if _, err = tx.ExecContext(ctx, seed.query, seed.args...); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "manual" || scenario == "controlled" {
				query := `UPDATE capture_page SET manual_override='{"page_no":1}' WHERE id=$1::uuid`
				if scenario == "controlled" {
					query = `UPDATE capture_page SET page_identity='{"barcode_status":"verified"}' WHERE id=$1::uuid`
				}
				if _, err = tx.ExecContext(ctx, query, captureID); err != nil {
					t.Fatal(err)
				}
			}
			registrations, err := QueueSubmissionPagesInTx(ctx, tx, tenant, submissionID, actor)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "manual" || scenario == "controlled" {
				if len(registrations) != 1 || registrations[0].PageNo != 1 {
					t.Fatalf("explicit page number changed: %#v", registrations)
				}
				var matches int
				if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM page_template_match_run`).Scan(&matches); err != nil || matches != 0 {
					t.Fatalf("explicit page should bypass page matching: count=%d err=%v", matches, err)
				}
				return
			}
			if len(registrations) != 0 {
				t.Fatalf("bound_auto must identify unknown page before registration: %#v", registrations)
			}
			run, err := scanTemplateMatchRun(tx.QueryRowContext(ctx, `SELECT `+templateMatchColumns+` FROM page_template_match_run`))
			if err != nil || len(run.Candidates) != 2 {
				t.Fatalf("both template pages must be queued: %#v err=%v", run, err)
			}
			if scenario == "quality_failed" {
				if _, err = tx.ExecContext(ctx, `UPDATE submission_page SET quality_status='failed'`); err != nil {
					t.Fatal(err)
				}
				if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='quality_rejected',revision=revision+1`); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "source_changed" {
				if _, err = tx.ExecContext(ctx, `UPDATE file_asset SET hash_sha256='new-source-hash'`); err != nil {
					t.Fatal(err)
				}
			}
			out, registrations, err := applyTemplateMatchResultInTx(ctx, tx, tenant, run.ID, TemplateMatchResultInput{
				Decision: "matched", SelectedTemplateID: templateID, SelectedTemplateContentHash: "template-hash", Score: 0.96, Margin: 0.2,
				Candidates: []map[string]any{{"template_id": templateID, "template_content_hash": "template-hash", "template_page_index": float64(2), "score": 0.96, "geometry_passed": true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var captureNo, submissionNo int
			var status, identitySource string
			if err = tx.QueryRowContext(ctx, `SELECT cp.assigned_page_no,sp.page_no,cp.status,COALESCE(cp.page_identity->>'page_no_source','') FROM capture_page cp JOIN submission_page sp ON sp.id=cp.submission_page_id`).Scan(&captureNo, &submissionNo, &status, &identitySource); err != nil {
				t.Fatal(err)
			}
			if scenario == "reverse_side" {
				if out.Decision != "matched" || captureNo != 2 || submissionNo != 2 || identitySource != "visual_template_match" || len(registrations) != 1 || registrations[0].PageNo != 2 {
					t.Fatalf("reverse side not persisted before registration: decision=%s cp=%d sp=%d identity=%s registrations=%#v", out.Decision, captureNo, submissionNo, identitySource, registrations)
				}
			} else if out.Decision != "unknown" || captureNo != 1 || submissionNo != 1 || len(registrations) != 0 || (scenario == "quality_failed" && status != "quality_rejected") {
				t.Fatalf("stale result changed current page: decision=%s cp=%d sp=%d status=%s registrations=%#v", out.Decision, captureNo, submissionNo, status, registrations)
			}
		})
	}
}

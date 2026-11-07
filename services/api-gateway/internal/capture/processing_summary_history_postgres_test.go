package capture

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 用真实查询检查当前批次范围，并让其他批次积累3000条运行与切片，记录实际索引计划。
// 夹具只复制本查询需要的列和索引，不把性能专项当作完整采集流程或生产容量测试。
func TestPostgresProcessingSummaryLimitsHistoryToBatchPages(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is required for processing summary history test")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "capture_summary_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE capture_page(id uuid PRIMARY KEY,tenant_id uuid,submission_id uuid,capture_batch_id uuid,submission_page_id uuid,assigned_page_no int,status text,sequence_no int,deleted_at timestamptz);
CREATE INDEX batch_pages ON capture_page(tenant_id,capture_batch_id);
CREATE TABLE submission_page(id uuid PRIMARY KEY,tenant_id uuid,quality_status text,deleted_at timestamptz);
CREATE TABLE page_registration_run(id uuid PRIMARY KEY,tenant_id uuid,capture_page_id uuid,processing_status text,match_status text,created_at timestamptz,deleted_at timestamptz);
CREATE INDEX idx_page_registration_current ON page_registration_run(tenant_id,capture_page_id,created_at DESC,id DESC);
CREATE TABLE answer_segment(id uuid,tenant_id uuid,registration_run_id uuid,processing_status text,deleted_at timestamptz);
CREATE INDEX idx_answer_segment_registration ON answer_segment(tenant_id,registration_run_id)`)
	tenantID, batchID, submissionID, pageID, runID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO capture_page VALUES($1,$2,$3,$4,NULL,1,'processing',1,NULL)`, pageID, tenantID, submissionID, batchID)
	exec(`INSERT INTO page_registration_run VALUES($1,$2,$3,'terminal_error','needs_review',now(),NULL)`, runID, tenantID, pageID)
	// 较旧成功运行及其切片不能掩盖当前失败；已失效的新运行也不能成为当前运行。
	exec(`INSERT INTO page_registration_run VALUES(gen_random_uuid(),$1,$2,'completed','matched',now()-interval '1 hour',NULL),(gen_random_uuid(),$1,$2,'invalidated','matched',now()+interval '1 hour',NULL)`, tenantID, pageID)
	exec(`INSERT INTO page_registration_run SELECT gen_random_uuid(),$1,gen_random_uuid(),'completed','matched',now(),NULL FROM generate_series(1,3000)`, tenantID)
	exec(`INSERT INTO answer_segment SELECT gen_random_uuid(),tenant_id,id,'completed',NULL FROM page_registration_run WHERE id<>$1`, runID)
	for _, table := range []string{"capture_page", "page_registration_run", "answer_segment"} {
		exec("ANALYZE " + table)
	}
	items, err := NewPostgresStore(db).ListProcessingSummaries(ctx, tenantID, batchID)
	if err != nil || len(items) != 1 || items[0].TotalPages != 1 || items[0].BlockedPages != 1 || len(items[0].Blockers) != 1 || items[0].Blockers[0].RegistrationRunID != runID {
		t.Fatalf("incorrect scoped summary: %+v %v", items, err)
	}
	if unrelated, err := NewPostgresStore(db).ListProcessingSummaries(ctx, uuid.NewString(), batchID); err != nil || len(unrelated) != 0 {
		t.Fatalf("other tenant summary: %+v %v", unrelated, err)
	}
	var plan string
	if err := db.QueryRowContext(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+processingSummariesSQL("cp.capture_batch_id=$2::uuid"), tenantID, batchID).Scan(&plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_page_registration_current") || !strings.Contains(plan, "idx_answer_segment_registration") {
		t.Fatalf("scoped summary did not use page/run indexes: %s", plan)
	}
	t.Logf("one current page with 3000 unrelated history rows; actual query plan: %s", plan)
}

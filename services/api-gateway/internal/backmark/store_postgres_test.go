package backmark

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresBackmarkCountsAndFilteredCursorPages(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A single connection and temporary table isolate this test from all
	// persisted tenant data, even when run against a development database.
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TEMP TABLE backmark_item (
id uuid, tenant_id uuid, batch_id uuid, review_task_id uuid, original_grade_id uuid,
reassigned_task_id uuid, new_grade_id uuid, original_reviewer_id uuid, reassigned_to uuid,
original_score float8, max_score float8, new_score float8, diff float8, status text,
revision bigint, created_at timestamptz, updated_at timestamptz)`)
	if err != nil {
		t.Fatal(err)
	}
	const tenantID = "00000000-0000-0000-0000-000000000001"
	const batchID = "00000000-0000-0000-0000-000000000002"
	_, err = db.Exec(`INSERT INTO backmark_item
SELECT ('00000000-0000-0000-0000-' || lpad(i::text,12,'0'))::uuid,
$1::uuid, $2::uuid, $2::uuid, $2::uuid, NULL::uuid, NULL::uuid, $2::uuid, $2::uuid,
3,5,CASE WHEN i>50 THEN 1 ELSE 3 END,CASE WHEN i>50 THEN -2 ELSE 0 END,
CASE WHEN i>50 THEN 'regrade_required' ELSE 'diff_ready' END,1,
'2026-09-15T01:00:00Z'::timestamptz,'2026-09-15T01:00:00Z'::timestamptz
FROM generate_series(1,55) i`, tenantID, batchID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO backmark_item (id,tenant_id,batch_id,status)
VALUES ('00000000-0000-0000-0000-000000000099','00000000-0000-0000-0000-000000000003',$1::uuid,'regrade_required')`, batchID)
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	counts, err := store.GetStatusCounts(context.Background(), tenantID, batchID)
	if err != nil || counts.RegradeRequiredCount != 5 || counts.CompletedCount != 55 || counts.PendingCount != 0 {
		t.Fatalf("counts=%#v err=%v", counts, err)
	}
	first, err := store.ListBatchItems(context.Background(), tenantID, batchID, PageOptions{Limit: 50})
	if err != nil || len(first) != 50 {
		t.Fatalf("first items=%d err=%v", len(first), err)
	}
	for _, item := range first {
		if item.Status == ItemRegradeRequired {
			t.Fatal("severe item appeared on first page")
		}
	}
	filtered, err := store.ListBatchItems(context.Background(), tenantID, batchID, PageOptions{Limit: 2, Status: ItemRegradeRequired})
	if err != nil || len(filtered) != 2 {
		t.Fatalf("filtered items=%d err=%v", len(filtered), err)
	}
	last := filtered[1]
	second, err := store.ListBatchItems(context.Background(), tenantID, batchID, PageOptions{Limit: 10, Status: ItemRegradeRequired, CursorID: last.ID, CursorCreatedAt: last.CreatedAt})
	if err != nil || len(second) != 3 || second[0].ID == filtered[0].ID {
		t.Fatalf("second items=%#v err=%v", second, err)
	}
}

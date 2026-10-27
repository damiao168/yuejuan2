package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func applyAuditAppendOrderMigration(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000209_strong_audit_append_order.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func installHistoricalAuditTrigger(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	// public 测试库可能已应用新迁移；只在随机 schema 的克隆表上恢复旧结构来验证升级。
	execAuthScopeFixture(t, ctx, db, `ALTER TABLE audit_log DROP COLUMN IF EXISTS append_sequence CASCADE`)
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000068_strong_audit_outbox.sql"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	start := strings.Index(source, "CREATE OR REPLACE FUNCTION audit_log_chain_before_insert()")
	end := strings.Index(source, "CREATE OR REPLACE FUNCTION reject_audit_log_mutation()")
	if start < 0 || end <= start {
		t.Fatal("historical chain function/trigger was not found")
	}
	execAuthScopeFixture(t, ctx, db, source[start:end])
}

func TestPostgresAuditAppendOrderPreservesHistoryAndHandlesSameTransactionRows(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	installHistoricalAuditTrigger(t, ctx, db)
	tenant := uuid.NewString()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"f0000000-0000-4000-8000-000000000001", "10000000-0000-4000-8000-000000000002", "90000000-0000-4000-8000-000000000003"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(id,tenant_id,action,target_type) VALUES($1,$2,$3,'synthetic')`, id, tenant, "synthetic.history."+id); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var oldHashes, oldLinks string
	if err := db.QueryRowContext(ctx, `SELECT jsonb_object_agg(id,record_hash)::text,jsonb_object_agg(id,previous_hash)::text FROM audit_log`).Scan(&oldHashes, &oldLinks); err != nil {
		t.Fatal(err)
	}
	var oldForks int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT previous_hash FROM audit_log GROUP BY previous_hash HAVING count(*)>1) forks`).Scan(&oldForks); err != nil || oldForks != 1 {
		t.Fatalf("historical control must demonstrate the original fork: forks=%d err=%v", oldForks, err)
	}
	applyAuditAppendOrderMigration(t, ctx, db)
	var preservedHashes, preservedLinks string
	if err := db.QueryRowContext(ctx, `SELECT jsonb_object_agg(id,record_hash)::text,jsonb_object_agg(id,previous_hash)::text FROM audit_log WHERE append_sequence IS NULL`).Scan(&preservedHashes, &preservedLinks); err != nil {
		t.Fatal(err)
	}
	if preservedHashes != oldHashes || preservedLinks != oldLinks {
		t.Fatal("migration rewrote historical evidence")
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, id := range []string{"80000000-0000-4000-8000-000000000004", "70000000-0000-4000-8000-000000000005", "60000000-0000-4000-8000-000000000006"} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(id,tenant_id,action,target_type) VALUES($1,$2,'synthetic.new-transaction','synthetic')`, id, tenant); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,action,target_type) SELECT $1,'synthetic.multi-row','synthetic' FROM generate_series(1,3)`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertAuditAppendOrderLinear(t, ctx, db, tenant, 6)
	var firstPrevious, historicalTerminal string
	if err := db.QueryRowContext(ctx, `SELECT previous_hash FROM audit_log WHERE append_sequence IS NOT NULL ORDER BY append_sequence LIMIT 1`).Scan(&firstPrevious); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT record_hash FROM audit_log WHERE id='90000000-0000-4000-8000-000000000003'`).Scan(&historicalTerminal); err != nil || firstPrevious != historicalTerminal {
		t.Fatalf("new segment did not attach to the deterministic historical terminal: err=%v", err)
	}
}

func assertAuditAppendOrderLinear(t *testing.T, ctx context.Context, db *sql.DB, tenant string, wantCount int) {
	t.Helper()
	var count, mismatches, invalidHashes int
	if err := db.QueryRowContext(ctx, `WITH ordered AS (SELECT previous_hash,lag(record_hash) OVER(ORDER BY append_sequence) expected FROM audit_log WHERE tenant_id=$1 AND append_sequence IS NOT NULL) SELECT count(*),count(*) FILTER(WHERE expected IS NOT NULL AND expected<>previous_hash) FROM ordered`, tenant).Scan(&count, &mismatches); err != nil {
		t.Fatal(err)
	}
	if count != wantCount || mismatches != 0 {
		t.Fatalf("new chain rows=%d want=%d mismatches=%d", count, wantCount, mismatches)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit_log WHERE tenant_id=$1 AND record_hash IS DISTINCT FROM encode(digest(convert_to(concat_ws('|',previous_hash,tenant_id::text,COALESCE(actor_id::text,''),action,target_type,COALESCE(target_id::text,''),COALESCE(before_value::text,''),COALESCE(after_value::text,''),COALESCE(reason,''),COALESCE(request_id,''),created_at::text,chain_version::text),'UTF8'),'sha256'),'hex')`, tenant).Scan(&invalidHashes); err != nil || invalidHashes != 0 {
		t.Fatalf("self digest invalid=%d err=%v", invalidHashes, err)
	}
}

func TestPostgresAuditAppendOrderSkipsRollbackConflictAndSerializesWriters(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	installHistoricalAuditTrigger(t, ctx, db)
	applyAuditAppendOrderMigration(t, ctx, db)
	tenant, firstID := uuid.NewString(), uuid.NewString()
	// 未来时间不能成为后续写入的固定链尾；追加顺序独立于用户给定时间和随机 UUID。
	execAuthScopeFixture(t, ctx, db, `INSERT INTO audit_log(id,tenant_id,action,target_type,created_at) VALUES($1,$2,'synthetic.first','synthetic',now()+interval '1 year')`, firstID, tenant)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,action,target_type) VALUES($1,'synthetic.rollback','synthetic')`, tenant); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	execAuthScopeFixture(t, ctx, db, `INSERT INTO audit_log(id,tenant_id,action,target_type) VALUES($1,$2,'synthetic.conflict','synthetic') ON CONFLICT(id) DO NOTHING`, firstID, tenant)
	const workers, perWorker = 4, 3
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				errors <- err
				return
			}
			defer tx.Rollback()
			for range perWorker {
				if _, err := tx.ExecContext(ctx, `INSERT INTO audit_log(tenant_id,action,target_type) VALUES($1,'synthetic.concurrent','synthetic')`, tenant); err != nil {
					errors <- err
					return
				}
			}
			errors <- tx.Commit()
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertAuditAppendOrderLinear(t, ctx, db, tenant, 1+workers*perWorker)
}

func TestPostgresAuditMaximumPageReturnsRemainingRecord(t *testing.T) {
	ctx, db := authScopePostgresFixture(t)
	tenant := uuid.NewString()
	execAuthScopeFixture(t, ctx, db, `INSERT INTO audit_log(tenant_id,action,target_type) SELECT $1,'synthetic.page','synthetic' FROM generate_series(1,201)`, tenant)
	handler := NewHandler(NewPostgresStore(db), time.Hour)
	actor := User{ID: uuid.NewString(), TenantID: tenant}
	cursor := ""
	seen := map[string]bool{}
	for page, want := range []int{200, 1} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?limit=200&cursor="+cursor, nil)
		req = req.WithContext(WithAccessScope(WithUser(ctx, actor), AccessScope{TenantID: tenant, ActorID: actor.ID, TenantWide: true}))
		rec := httptest.NewRecorder()
		handler.ListAudits(rec, req)
		var body struct {
			Records []AuditRecord `json:"audit_logs"`
			HasMore bool          `json:"has_more"`
			Next    string        `json:"next_cursor"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || len(body.Records) != want || body.HasMore != (page == 0) {
			t.Fatalf("page %d response=%d %s", page, rec.Code, rec.Body)
		}
		for _, record := range body.Records {
			if seen[record.ID] {
				t.Fatal("duplicate record across audit pages")
			}
			seen[record.ID] = true
		}
		cursor = body.Next
	}
	if len(seen) != 201 || cursor != "" {
		t.Fatalf("audit pagination incomplete: count=%d cursor=%q", len(seen), cursor)
	}
}

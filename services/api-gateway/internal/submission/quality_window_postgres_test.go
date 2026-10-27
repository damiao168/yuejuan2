package submission

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestQualityCheckWaitsForReplacementBeforeReadingPages(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "quality_window_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	cfg.RuntimeParams["application_name"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	for _, table := range []string{"submission", "submission_page"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	tenant, actor, exam, file := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	store := NewPostgresStore(db)
	item, err := store.Create(ctx, tenant, exam, actor, CreateSubmissionInput{SourceType: "manual_import", ExpectedPageCount: 1, CandidateNo: "synthetic-001"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := store.AddPage(ctx, tenant, item.ID, actor, AddPageInput{FileAssetID: file, PageNo: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE submission_page SET quality_status='passed' WHERE id=$1`, page.ID); err != nil {
		t.Fatal(err)
	}
	if result, err := store.RunQualityCheck(ctx, tenant, item.ID, actor); err != nil || !result.Valid {
		t.Fatalf("passed control: %+v %v", result, err)
	}

	// 用页更新触发器暂停真实 ReplacePage：它已持有父锁，检查必须等提交后才读新页面。
	key := time.Now().UnixNano()
	blocker, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	if _, err = blocker.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}
	defer blocker.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, key)
	trigger := fmt.Sprintf(`CREATE FUNCTION pause_replacement() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(%d); RETURN NEW; END $$;
CREATE TRIGGER pause_replacement BEFORE UPDATE ON submission_page FOR EACH ROW EXECUTE FUNCTION pause_replacement()`, key)
	if _, err = db.ExecContext(ctx, trigger); err != nil {
		t.Fatal(err)
	}
	replaced := make(chan error, 1)
	newFile := uuid.NewString()
	go func() {
		_, err := store.ReplacePage(ctx, tenant, item.ID, actor, 1, AddPageInput{FileAssetID: newFile})
		replaced <- err
	}()
	waitQualityQueryLock(t, ctx, admin, schema, "%UPDATE submission_page%")
	checked := make(chan error, 1)
	go func() {
		_, err := store.RunQualityCheck(ctx, tenant, item.ID, actor)
		checked <- err
	}()
	waitQualityQueryLock(t, ctx, admin, schema, "%SELECT status FROM submission%FOR UPDATE%")
	if _, err = blocker.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
		t.Fatal(err)
	}
	if err = <-replaced; err != nil {
		t.Fatal(err)
	}
	if err = <-checked; err != nil {
		t.Fatal(err)
	}
	current, err := store.getSubmission(ctx, tenant, item.ID)
	if err != nil || current.QualityStatus != "unchecked" || current.Status != "pages_uploaded" {
		t.Fatalf("new page must remain unchecked: %+v %v", current, err)
	}
	if _, err = store.UpdateStatus(ctx, tenant, item.ID, actor, "ready_for_ocr", current.Revision); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("new image bypassed gate: %v", err)
	}
	// 新图真正通过质量检查后仍可继续正常冻结。
	if _, err = db.ExecContext(ctx, `UPDATE submission_page SET quality_status='passed' WHERE id=$1`, page.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunQualityCheck(ctx, tenant, item.ID, actor); err != nil {
		t.Fatal(err)
	}
	current, err = store.getSubmission(ctx, tenant, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateStatus(ctx, tenant, item.ID, actor, "ready_for_ocr", current.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RunQualityCheck(ctx, tenant, item.ID, actor); !errors.Is(err, ErrSubmissionLocked) {
		t.Fatalf("frozen submission was rechecked: %v", err)
	}
}

func waitQualityQueryLock(t *testing.T, ctx context.Context, db *sql.DB, appName, pattern string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE $2)`, appName, pattern).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("expected lock wait for %s: %v", pattern, ctx.Err())
		case <-ticker.C:
		}
	}
}

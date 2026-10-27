package outbox

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresExpiredFinalAttemptBecomesDeadLetter(t *testing.T) {
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
	schema := "outbox_lease_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, `CREATE TABLE event_outbox (LIKE public.event_outbox INCLUDING ALL)`); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	tenant := uuid.NewString()
	insert := func(attempt, max int, expiry string) string {
		t.Helper()
		id := uuid.NewString()
		if _, err := db.ExecContext(ctx, `INSERT INTO event_outbox(id,tenant_id,aggregate_type,aggregate_id,event_type,payload,attempt_count,max_attempts,locked_by,lock_expires_at) VALUES($1,$2,'synthetic',$3,'synthetic.event','{}',$4,$5,'old',now()+$6::interval)`, id, tenant, uuid.NewString(), attempt, max, expiry); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exhausted := insert(1, 1, "-1 second")
	active := insert(1, 1, "1 hour")
	retry := insert(1, 2, "-1 second")
	items, err := store.Claim(ctx, "new", 10, time.Minute)
	if err != nil || len(items) != 1 || items[0].ID != retry || items[0].AttemptCount != 2 {
		t.Fatalf("claim=%#v err=%v", items, err)
	}
	var dead bool
	var owner string
	if err := db.QueryRowContext(ctx, `SELECT dead_lettered_at IS NOT NULL,COALESCE(locked_by,'') FROM event_outbox WHERE id=$1`, exhausted).Scan(&dead, &owner); err != nil || !dead || owner != "" {
		t.Fatalf("expired final lease not finalized: %v %q %v", dead, owner, err)
	}
	if err := store.MarkPublished(ctx, exhausted, "old"); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("late owner confirmed dead letter: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT dead_lettered_at IS NOT NULL FROM event_outbox WHERE id=$1`, active).Scan(&dead); err != nil || dead {
		t.Fatalf("active lease terminated: %v %v", dead, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE event_outbox SET lock_expires_at=now()-interval '1 second' WHERE id IN ($1,$2)`, active, retry); err != nil {
		t.Fatal(err)
	}
	items, err = store.Claim(ctx, "recovery", 10, time.Minute)
	if err != nil || len(items) != 0 {
		t.Fatalf("exhausted events re-delivered: %#v %v", items, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM event_outbox WHERE dead_lettered_at IS NOT NULL AND published_at IS NULL AND locked_by IS NULL AND lock_expires_at IS NULL`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("dead letters=%d err=%v", count, err)
	}
}

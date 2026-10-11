package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/outbox"
)

func TestRequestIdempotencyWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL idempotency workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)

	var tenantID, actorID string
	if err := db.QueryRow(`
SELECT t.id::text,u.id::text
FROM tenant t
JOIN app_user u ON u.tenant_id=t.id
WHERE t.code='demo' AND u.username='tenant_admin'
LIMIT 1`).Scan(&tenantID, &actorID); err != nil {
		t.Fatalf("lookup idempotency test identity: %v", err)
	}

	store := idempotency.NewPostgresStore(db)
	input := idempotency.BeginInput{
		TenantID:    tenantID,
		ActorID:     actorID,
		Method:      "POST",
		Route:       "/api/v1/exams",
		Key:         "e2e-" + time.Now().UTC().Format("20060102150405.000000000"),
		RequestHash: "request-hash-a",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	assertConcurrentIdempotencyOwnership(t, ctx, store, input)

	body := []byte(`{"exam":{"id":"stable-result"}}`)
	if err := store.Complete(ctx, input, 201, map[string]string{"Content-Type": "application/json"}, body); err != nil {
		t.Fatalf("complete idempotent operation: %v", err)
	}
	record, execute, err := store.Begin(ctx, input)
	if err != nil || execute {
		t.Fatalf("expected completed replay, execute=%v err=%v", execute, err)
	}
	if record.ResponseStatus != 201 || string(record.ResponseBody) != string(body) {
		t.Fatalf("unexpected replay record: %#v", record)
	}

	conflict := input
	conflict.RequestHash = "request-hash-b"
	if _, _, err := store.Begin(ctx, conflict); !errors.Is(err, idempotency.ErrKeyConflict) {
		t.Fatalf("changed request with reused key must conflict, got %v", err)
	}

	recovery := input
	recovery.Key += "-recovery"
	if _, execute, err := store.Begin(ctx, recovery); err != nil || !execute {
		t.Fatalf("reserve recoverable operation: execute=%v err=%v", execute, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE idempotency_record SET updated_at=now()-interval '10 minutes'
	WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND method=$3 AND route=$4 AND idempotency_key=$5`, recovery.TenantID, recovery.ActorID, recovery.Method, recovery.Route, recovery.Key); err != nil {
		t.Fatalf("age interrupted reservation: %v", err)
	}
	recovery.AllowTakeover = true
	recovery.StaleBefore = time.Now().UTC().Add(-2 * time.Minute)
	if _, execute, err := store.Begin(ctx, recovery); err != nil || !execute {
		t.Fatalf("stale interrupted operation should be reclaimed: execute=%v err=%v", execute, err)
	}
}

// 完成回执前同时抢占同一请求，须只有一个执行者；等待 goroutine 结束后才读取结果。
func assertConcurrentIdempotencyOwnership(t *testing.T, ctx context.Context, store *idempotency.PostgresStore, input idempotency.BeginInput) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, execute, err := store.Begin(ctx, input)
			if err == nil && !execute {
				err = errors.New("unexpected replay before completion")
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var owners, inProgress int
	for err := range results {
		switch {
		case err == nil:
			owners++
		case errors.Is(err, idempotency.ErrInProgress):
			inProgress++
		default:
			t.Fatalf("unexpected concurrent begin result: %v", err)
		}
	}
	if owners != 1 || inProgress != 1 {
		t.Fatalf("expected one owner and one in-progress result, got owners=%d in_progress=%d", owners, inProgress)
	}

}

func TestOutboxLeaseRecoveryWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set; skipping PostgreSQL outbox workflow")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)

	var tenantID string
	if err := db.QueryRow(`SELECT id::text FROM tenant WHERE code='demo' LIMIT 1`).Scan(&tenantID); err != nil {
		t.Fatalf("lookup outbox test tenant: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM event_outbox`); err != nil {
		t.Fatalf("clear isolated outbox: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO event_outbox(tenant_id,aggregate_type,aggregate_id,event_type,payload,max_attempts)
VALUES($1::uuid,'exam',gen_random_uuid(),'e2e.created','{}',3)`, tenantID); err != nil {
		t.Fatalf("seed outbox event: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := outbox.NewPostgresStore(db)
	claimed, err := store.Claim(ctx, "consumer-a", 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim outbox event: count=%d err=%v", len(claimed), err)
	}
	if _, err := db.Exec(`UPDATE event_outbox SET lock_expires_at=now()-interval '1 second' WHERE id=$1::uuid`, claimed[0].ID); err != nil {
		t.Fatalf("expire interrupted claim: %v", err)
	}
	reclaimed, err := store.Claim(ctx, "consumer-b", 10, time.Minute)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].ID != claimed[0].ID {
		t.Fatalf("reclaim expired outbox lease: %#v err=%v", reclaimed, err)
	}
	if reclaimed[0].AttemptCount != 2 {
		t.Fatalf("reclaimed attempt count must advance, got %d", reclaimed[0].AttemptCount)
	}
	if err := store.MarkPublished(ctx, reclaimed[0].ID, "consumer-b"); err != nil {
		t.Fatalf("publish reclaimed event: %v", err)
	}
	remaining, err := store.Claim(ctx, "consumer-c", 10, time.Minute)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("published event must not be claimed again: %#v err=%v", remaining, err)
	}
}

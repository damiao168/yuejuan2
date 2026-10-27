package ocr

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func ocrCompletionRemediationDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "ocr_completion_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		if err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, table := range []string{"ocr_task", "ocr_result", "submission_page", "agent_worker_task", "agent_worker_task_attempt", "agent_worker_heartbeat"} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "ALTER TABLE ocr_task DROP COLUMN IF EXISTS completion_payload_hash CASCADE"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/000211_ocr_completion_payload_hash.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	return ctx, db
}

func TestPostgresCompletionReplaysOriginalPrecisionAndOrder(t *testing.T) {
	ctx, db := ocrCompletionRemediationDB(t)
	tenant, actor, submission, page, taskID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2,$3,$4,1,'accepted')`, page, tenant, submission, uuid.NewString())
	exec(`INSERT INTO ocr_task(id,tenant_id,submission_id,status,engine,engine_version,min_confidence,requested_by) VALUES($1,$2,$3,'processing','fixture','v1',.8,$4)`, taskID, tenant, submission, actor)
	input := completionRemediationInput()
	for i := range input.Results {
		input.Results[i].SubmissionPageID = page
	}
	store := NewPostgresStore(db)
	runtime := workerruntime.NewPostgresStore(db)
	_, err := runtime.CreateTask(ctx, tenant, actor, workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: "ocr", SourceType: "ocr_task", SourceID: taskID, IdempotencyKey: taskID, PayloadSchemaVersion: "fixture-v1"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "ocr", WorkerService: "ocr-worker", WorkerInstanceID: "synthetic", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	input.RuntimeTaskID, input.RuntimeLeaseToken = claimed[0].ID, claimed[0].LeaseToken
	coordinator := newAtomicRuntimeCoordinator(store, runtime)
	completed, err := coordinator.CompleteTask(ctx, tenant, taskID, input)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Results[0].Confidence != .9123 && completed.Results[0].Confidence != .9235 {
		t.Fatalf("fixture did not exercise numeric precision: %#v", completed.Results)
	}
	for i := 0; i < 2; i++ {
		if _, err := coordinator.CompleteTask(ctx, tenant, taskID, input); err != nil {
			t.Fatalf("original retry %d: %v", i, err)
		}
		input.Results[0], input.Results[1] = input.Results[1], input.Results[0]
	}
	changed := input
	changed.Results = append([]ResultInput(nil), input.Results...)
	changed.Results[0].Confidence += .000001
	if _, err := coordinator.CompleteTask(ctx, tenant, taskID, changed); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("new hash accepted subprecision change: %v", err)
	}
	// 强制反转数据库回读排序，证明幂等比较不依赖随机UUID；历史无指纹仍接受原载荷。
	exec(`UPDATE ocr_result SET created_at=CASE text WHEN 'first' THEN now()+interval '1 minute' ELSE now()-interval '1 minute' END WHERE ocr_task_id=$1`, taskID)
	if _, err := coordinator.CompleteTask(ctx, tenant, taskID, input); err != nil {
		t.Fatalf("reordered stored results: %v", err)
	}
	exec(`UPDATE ocr_task SET completion_payload_hash=NULL WHERE id=$1`, taskID)
	if _, err := coordinator.CompleteTask(ctx, tenant, taskID, input); err != nil {
		t.Fatalf("historical four-decimal replay: %v", err)
	}
	changed = input
	changed.Results = append([]ResultInput(nil), input.Results...)
	changed.Results[0].Text = "changed"
	if _, err := coordinator.CompleteTask(ctx, tenant, taskID, changed); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("historical task accepted changed text: %v", err)
	}
}

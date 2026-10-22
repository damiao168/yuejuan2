package orchestrator

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 使用审查专用的本机集群与唯一 schema；实际状态转换方法和 000009 的原始表约束都参与验证。
func TestPostgresConcurrentTransitionsPreserveAcceptedTerminalAndRunSummary(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55983 || cfg.Database != "review_root_round10_grading_test" {
		t.Fatal("refuse a database outside the isolated review fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "root20_orchestration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean isolated schema: %v", err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	cfg.RuntimeParams["application_name"] = schema[:48]
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	db.SetMaxOpenConns(8)
	if _, err := db.ExecContext(ctx, "CREATE TABLE tenant(id uuid PRIMARY KEY); CREATE TABLE app_user(id uuid PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000009_orchestrator.sql"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(raw), "CREATE TABLE IF NOT EXISTS orchestration_run")
	if start < 0 {
		t.Fatal("migration table definitions are missing")
	}
	if _, err := db.ExecContext(ctx, string(raw[start:])); err != nil {
		t.Fatal(err)
	}
	tenant, actor := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(ctx, "INSERT INTO tenant VALUES ($1)", tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO app_user VALUES ($1)", actor); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	newTask := func(runID string) Task {
		t.Helper()
		task, err := store.CreateTask(ctx, tenant, runID, actor, CreateTaskInput{AgentType: "objective_grading_agent", MaxAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		if task, err = store.StartTask(ctx, tenant, task.ID); err != nil {
			t.Fatal(err)
		}
		return task
	}
	newRun := func() Run {
		t.Helper()
		run, err := store.CreateRun(ctx, tenant, actor, CreateRunInput{WorkflowType: "grading_pipeline", TargetType: "exam", TargetID: uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	t.Run("complete versus fail", func(t *testing.T) {
		run := newRun()
		task := newTask(run.ID)
		results := raceOrchestrationCommands(t, ctx, db, cfg.RuntimeParams["application_name"], run.ID,
			func() error {
				_, err := store.CompleteTask(ctx, tenant, task.ID, CompleteTaskInput{OutputRef: map[string]any{"accepted": true}})
				return err
			},
			func() error { _, err := store.FailTask(ctx, tenant, task.ID, "synthetic failure"); return err })
		accepted, rejected := 0, 0
		for _, err := range results {
			if err == nil {
				accepted++
			} else if errors.Is(err, ErrInvalidTransition) {
				rejected++
			} else {
				t.Fatalf("unexpected transition error: %v", err)
			}
		}
		if accepted != 1 || rejected != 1 {
			t.Fatalf("mutually exclusive commands accepted=%d rejected=%d", accepted, rejected)
		}
		saved, err := store.GetRun(ctx, tenant, run.ID)
		if err != nil || len(saved.Tasks) != 1 || (saved.Tasks[0].Status == "succeeded" && saved.Status != "completed") || (saved.Tasks[0].Status == "failed" && saved.Status != "failed") {
			t.Fatalf("terminal state and aggregate disagree: %#v err=%v", saved, err)
		}
	})
	t.Run("retry increments once", func(t *testing.T) {
		run := newRun()
		task := newTask(run.ID)
		if _, err := store.FailTask(ctx, tenant, task.ID, "temporary failure"); err != nil {
			t.Fatal(err)
		}
		results := raceOrchestrationCommands(t, ctx, db, cfg.RuntimeParams["application_name"], run.ID,
			func() error { _, err := store.RetryTask(ctx, tenant, task.ID); return err },
			func() error { _, err := store.RetryTask(ctx, tenant, task.ID); return err })
		accepted := 0
		for _, err := range results {
			if err == nil {
				accepted++
			} else if !errors.Is(err, ErrRetryExhausted) {
				t.Fatal(err)
			}
		}
		saved, err := store.GetRun(ctx, tenant, run.ID)
		if err != nil || accepted != 1 || saved.Tasks[0].AttemptNo != 2 || saved.Tasks[0].Status != "queued" {
			t.Fatalf("retry changed twice: %#v accepted=%d err=%v", saved, accepted, err)
		}
	})
	t.Run("two tasks aggregate current commits", func(t *testing.T) {
		run := newRun()
		first, second := newTask(run.ID), newTask(run.ID)
		results := raceOrchestrationCommands(t, ctx, db, cfg.RuntimeParams["application_name"], run.ID,
			func() error { _, err := store.CompleteTask(ctx, tenant, first.ID, CompleteTaskInput{}); return err },
			func() error { _, err := store.CompleteTask(ctx, tenant, second.ID, CompleteTaskInput{}); return err })
		for _, err := range results {
			if err != nil {
				t.Fatal(err)
			}
		}
		saved, err := store.GetRun(ctx, tenant, run.ID)
		if err != nil || saved.Status != "completed" || len(saved.Tasks) != 2 {
			t.Fatalf("run lost one completion: %#v err=%v", saved, err)
		}
	})
}

func raceOrchestrationCommands(t *testing.T, ctx context.Context, db *sql.DB, appName, runID string, first, second func() error) []error {
	t.Helper()
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, "SELECT id FROM orchestration_run WHERE id=$1::uuid FOR UPDATE", runID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- first() }()
	go func() { results <- second() }()
	for {
		var waiting int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%FOR UPDATE OF r%'", appName).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		select {
		case err := <-results:
			t.Fatalf("command completed before both reached the held run lock: %v", err)
		case <-ctx.Done():
			t.Fatal("commands did not reach held lock")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	return []error{<-results, <-results}
}

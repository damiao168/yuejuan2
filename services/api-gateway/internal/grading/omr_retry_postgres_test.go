package grading

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

// 仅在审查专用本机库的独立 schema 执行生产方法；复制真实表的列/默认值/检查和索引，
// 不复制外键、触发器或 RLS，所以这里验证的是状态恢复和同事务回滚，不代表完整迁移验收。
func TestPostgresOMRAutomaticRetryCanCompleteAndRejectsStaleOrTerminalCallbacks(t *testing.T) {
	ctx, db := newOMRRetryFixture(t)
	store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	newRun := func(t *testing.T) (string, string, string, workerruntime.Task, workerruntime.Task) {
		t.Helper()
		mustExec := func(query string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, query, args...); err != nil {
				t.Fatal(err)
			}
		}
		tenant, actor, exam, question, segment, snapshot, submission, run, omr := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		mustExec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'1','single_choice',5,1,'draft')`, question, tenant, exam)
		mustExec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, tenant, exam, actor)
		mustExec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,crop_sha256) VALUES($1,$2,$3,$4,$5,'1','{"x":0,"y":0,"width":1,"height":1}','manual','accepted',$6)`, segment, tenant, submission, uuid.NewString(), question, strings.Repeat("a", 64))
		mustExec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'choice','[]','R1','{}','{}','{}','{}',$6)`, snapshot, tenant, exam, question, uuid.NewString(), strings.Repeat("b", 64))
		mustExec(`INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,'synthetic','processing',1,1,$4)`, run, tenant, exam, actor)
		task, err := runtime.CreateTask(ctx, tenant, actor, workerruntime.CreateTaskInput{TaskType: "omr_extract", QueueName: "page-processing", SourceType: "omr_run", SourceID: omr, PayloadSchemaVersion: "omr-task-v1", IdempotencyKey: "omr:" + omr, Payload: map[string]any{}, RetryBackoffSeconds: 1, MaxAttempts: 3})
		if err != nil {
			t.Fatal(err)
		}
		mustExec(`INSERT INTO omr_run(id,tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,status,runtime_task_id,auto_confirm_eligible,auto_confirm_reason) VALUES($1,$2,$3,$4,$5,$6,$5,$6,'fixture-v1','fixture-hash','processing',$7,false,'requires_review')`, omr, tenant, run, segment, uuid.NewString(), strings.Repeat("a", 64), task.ID)
		items, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "ocr-worker", WorkerInstanceID: "first", Limit: 1, LeaseSeconds: 300})
		if err != nil || len(items) != 1 {
			t.Fatalf("first claim items=%d err=%v", len(items), err)
		}
		return tenant, actor, omr, task, items[0]
	}
	inputFor := func(task workerruntime.Task) OMRResultInput {
		return OMRResultInput{TaskID: task.ID, LeaseToken: task.LeaseToken, ResultVersion: "fixture-result-v1", Decision: "selected", Confidence: .95, Selected: []string{"A"}, Measurements: []map[string]any{}, ProfileVersion: "fixture-v1", ProfileHash: "fixture-hash", OverlayFileAssetID: uuid.NewString()}
	}
	assertNoFacts := func(t *testing.T, tenant string) {
		t.Helper()
		for _, table := range []string{"answer_candidate", "answer_segment_answer", "review_task"} {
			var count int
			if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unexpected %s facts=%d err=%v", table, count, err)
			}
		}
	}
	t.Run("real failure then automatic reclaim succeeds", func(t *testing.T) {
		tenant, actor, omr, task, first := newRun(t)
		if _, err := runtime.Fail(ctx, tenant, task.ID, workerruntime.FailInput{LeaseToken: first.LeaseToken, Retryable: true, ErrorCode: "synthetic_worker_failure"}); err != nil {
			t.Fatal(err)
		}
		if failed, err := store.ApplyOMRFailure(ctx, tenant, omr, "synthetic_worker_failure", map[string]any{}, true); err != nil || failed.Status != "retryable_error" {
			t.Fatalf("retryable failure %#v err=%v", failed, err)
		}
		mustExec("UPDATE agent_worker_task SET not_before=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID)
		items, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "ocr-worker", WorkerInstanceID: "second", Limit: 1, LeaseSeconds: 300})
		if err != nil || len(items) != 1 || items[0].AttemptCount != 2 {
			t.Fatalf("reclaim items=%#v err=%v", items, err)
		}
		input := inputFor(items[0])
		stale := input
		stale.LeaseToken = first.LeaseToken
		if _, _, err := store.ApplyOMRResult(ctx, tenant, omr, actor, stale, nil); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
			t.Fatalf("old lease accepted: %v", err)
		}
		assertNoFacts(t, tenant)
		completed, grade, err := store.ApplyOMRResult(ctx, tenant, omr, actor, input, nil)
		if err != nil || completed.Status != "completed" || grade != nil {
			t.Fatalf("reclaimed result %#v grade=%#v err=%v", completed, grade, err)
		}
		if _, _, err := store.ApplyOMRResult(ctx, tenant, omr, actor, input, nil); err != nil {
			t.Fatalf("identical replay: %v", err)
		}
		var workerStatus, runStatus string
		var candidates, answers, reviews, succeededAttempts int
		if err := db.QueryRowContext(ctx, `SELECT t.status,s.status,(SELECT count(*) FROM answer_candidate WHERE tenant_id=$1),(SELECT count(*) FROM answer_segment_answer WHERE tenant_id=$1),(SELECT count(*) FROM review_task WHERE tenant_id=$1),(SELECT count(*) FROM agent_worker_task_attempt WHERE tenant_id=$1 AND status='succeeded') FROM agent_worker_task t JOIN omr_run o ON o.runtime_task_id=t.id JOIN scoring_run s ON s.id=o.scoring_run_id WHERE t.tenant_id=$1 AND t.id=$2`, tenant, task.ID).Scan(&workerStatus, &runStatus, &candidates, &answers, &reviews, &succeededAttempts); err != nil {
			t.Fatal(err)
		}
		if workerStatus != "succeeded" || runStatus != "needs_review" || candidates != 1 || answers != 1 || reviews != 1 || succeededAttempts != 1 {
			t.Fatalf("completion facts worker=%s run=%s candidates=%d answers=%d reviews=%d attempts=%d", workerStatus, runStatus, candidates, answers, reviews, succeededAttempts)
		}
	})
	for _, blocked := range []string{"cancelled scoring run", "terminal omr"} {
		t.Run(blocked, func(t *testing.T) {
			tenant, actor, omr, task, first := newRun(t)
			if blocked == "cancelled scoring run" {
				mustExec("UPDATE scoring_run SET status='cancelled' WHERE id=(SELECT scoring_run_id FROM omr_run WHERE id=$1)", omr)
			} else {
				mustExec("UPDATE omr_run SET status='terminal_error' WHERE id=$1", omr)
			}
			if _, _, err := store.ApplyOMRResult(ctx, tenant, omr, actor, inputFor(first), nil); !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("terminal callback accepted: %v", err)
			}
			assertNoFacts(t, tenant)
			current, err := runtime.Get(ctx, tenant, task.ID)
			if err != nil || current.Status != workerruntime.StatusLeased || current.LeaseToken != first.LeaseToken {
				t.Fatalf("rejected domain completed worker: %#v err=%v", current, err)
			}
		})
	}
}

func newOMRRetryFixture(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 55983 || cfg.Database != "review_root_round10_grading_test" {
		t.Fatal("refuse a database outside the isolated review fixture")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "root20_omr_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean isolated schema: %v", err)
		}
		admin.Close()
	})
	for _, table := range []string{"tenant", "question", "submission", "answer_segment", "exam_question_snapshot", "scoring_run", "omr_run", "omr_calibration_session", "answer_candidate", "answer_segment_answer", "review_task", "question_grade", "arbitration_task", "double_mark_session", "agent_worker_task", "agent_worker_task_attempt", "agent_worker_heartbeat"} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db
}

package grading

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// CI 只有 E2E 连接时先建随机迁移库，再交给现有 schema 夹具；本地绑定库仍可直接复用。
func segmentReprocessTestConfig(t *testing.T) *pgx.ConnConfig {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn != "" {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	owner, _, _ := gradingAIPlanTestDB(t)
	cfg, err := pgx.ParseConfig(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.QueryRow(`SELECT current_database()`).Scan(&cfg.Database); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func seedGenericScoreReprocessFixture(t *testing.T, cfg *pgx.ConnConfig) completedOMRFixture {
	t.Helper()
	f := seedCompletedOMRFixture(t, cfg)
	f.exec(t, `CREATE TABLE human_grade (LIKE public.human_grade INCLUDING ALL)`)
	// LIKE 不复制外键；恢复实际提交和新建任务会取得的片段 KEY SHARE，才能覆盖真实锁序。
	for _, table := range []string{"review_task", "human_grade", "final_grade", "question_grade"} {
		f.exec(t, `ALTER TABLE `+table+` ADD CONSTRAINT reprocess_segment_fk FOREIGN KEY(answer_segment_id) REFERENCES answer_segment(id)`)
	}
	f.exec(t, `ALTER TABLE human_grade ADD CONSTRAINT reprocess_review_task_fk FOREIGN KEY(review_task_id) REFERENCES review_task(id)`)
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
 SELECT exam_id,tenant_id,$2::uuid,'Synthetic generic reprocess','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$3::uuid FROM scoring_run WHERE id=$1::uuid`, f.run, uuid.NewString(), f.actor)
	f.exec(t, `UPDATE scoring_run SET status='completed',review_count=0 WHERE id=$1::uuid`, f.run)
	f.exec(t, `UPDATE answer_segment SET processing_status='completed'`)
	f.exec(t, `UPDATE review_task SET status='cancelled' WHERE id=$1::uuid`, f.review)
	f.exec(t, `UPDATE question_grade SET score=3 WHERE answer_segment_id=$1::uuid`, f.segment)
	return f
}

// 人工提交会清除草稿并完成父运行；这些历史事实仍必须挡住通用重算，且拒绝不能产生部分写入。
func TestPostgresSegmentReprocessRejectsSubmittedHumanAndFinalGrades(t *testing.T) {
	cfg := segmentReprocessTestConfig(t)
	for _, scenario := range []string{"human_current", "human_history", "review_submitted", "review_completed", "submitted_draft_deleted", "human_double_mark", "final_unlocked", "final_locked"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedGenericScoreReprocessFixture(t, cfg)
			switch scenario {
			case "human_current":
				f.exec(t, `UPDATE question_grade SET source='human'`)
			case "human_history":
				f.exec(t, `UPDATE question_grade SET source='human',is_current=false,status='superseded'`)
			case "review_submitted", "submitted_draft_deleted":
				f.exec(t, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, f.review)
				if scenario == "submitted_draft_deleted" {
					f.exec(t, `INSERT INTO review_draft(tenant_id,review_task_id,reviewer_id,comments,deleted_at) VALUES($1,$2,$3,'submitted teacher draft',now())`, f.tenant, f.review, f.actor)
				}
			case "review_completed":
				f.exec(t, `UPDATE review_task SET status='completed' WHERE id=$1::uuid`, f.review)
			case "human_double_mark":
				f.exec(t, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1,$2,$3,$4,3,5,'first_mark')`, f.tenant, f.review, f.segment, f.actor)
			case "final_unlocked", "final_locked":
				f.exec(t, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,locked,status,created_by)
 SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,3,5,'single_review',$2,'pending_confirmation',created_by FROM review_task WHERE id=$1::uuid`, f.review, scenario == "final_locked")
			}
			var beforeCurrent bool
			if err := f.db.QueryRowContext(f.ctx, `SELECT is_current FROM question_grade WHERE answer_segment_id=$1::uuid`, f.segment).Scan(&beforeCurrent); err != nil {
				t.Fatal(err)
			}
			_, err := f.store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "protected-reprocess"})
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("protected reprocess error=%v", err)
			}
			var candidates, runs, invalidated int
			var current bool
			var score float64
			if err := f.db.QueryRowContext(f.ctx, `SELECT g.is_current,g.score::float8,
 (SELECT count(*) FROM answer_candidate WHERE answer_segment_id=$1::uuid AND is_current),
 (SELECT count(*) FROM scoring_run),
 (SELECT count(*) FROM question_grade WHERE status='invalidated') FROM question_grade g WHERE g.answer_segment_id=$1::uuid`, f.segment).Scan(&current, &score, &candidates, &runs, &invalidated); err != nil || current != beforeCurrent || score != 3 || candidates != 1 || runs != 1 || invalidated != 0 {
				t.Fatalf("rejected reprocess changed facts: current=%v score=%v candidates=%d runs=%d invalidated=%d err=%v", current, score, candidates, runs, invalidated, err)
			}
		})
	}
}

func TestPostgresSegmentReprocessStillAcceptsUnfinalizedAutomaticGrade(t *testing.T) {
	f := seedGenericScoreReprocessFixture(t, segmentReprocessTestConfig(t))
	var exam string
	if err := f.db.QueryRowContext(f.ctx, `SELECT exam_id::text FROM scoring_run WHERE id=$1::uuid`, f.run).Scan(&exam); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = validateNewScoringRunTx(f.ctx, tx, f.tenant, exam, f.segment); err != nil {
		t.Fatal(err)
	}
	if err = invalidateScoringSegmentCurrentTx(f.ctx, tx, f.tenant, f.segment); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var candidates, currentGrades int
	if err = f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM answer_candidate WHERE answer_segment_id=$1::uuid AND is_current),(SELECT count(*) FROM question_grade WHERE answer_segment_id=$1::uuid AND is_current)`, f.segment).Scan(&candidates, &currentGrades); err != nil || candidates != 0 || currentGrades != 0 {
		t.Fatalf("automatic reprocess did not invalidate old projections: candidates=%d grades=%d err=%v", candidates, currentGrades, err)
	}
	// 用已接受的运行回执模拟丢失响应；之后出现人工工作，重放不能再次失效当前成果。
	f.exec(t, `UPDATE scoring_run SET idempotency_key=$2,command_request_hash=$3 WHERE id=$1::uuid`, f.run, "segment-reprocess:"+f.segment+":automatic-reprocess", scoringCommandHash(exam, f.segment))
	f.exec(t, `UPDATE question_grade SET source='human',status='confirmed',is_current=true WHERE answer_segment_id=$1::uuid`, f.segment)
	replayed, err := f.store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "automatic-reprocess"})
	var current bool
	if queryErr := f.db.QueryRowContext(f.ctx, `SELECT is_current FROM question_grade WHERE answer_segment_id=$1::uuid`, f.segment).Scan(&current); queryErr != nil || err != nil || replayed.ID != f.run || !current {
		t.Fatalf("replay changed accepted facts: run=%q replay=%q current=%v err=%v query=%v", f.run, replayed.ID, current, err, queryErr)
	}
}

func TestPostgresSegmentReprocessWaitsForExamFinalizationLock(t *testing.T) {
	f := seedGenericScoreReprocessFixture(t, segmentReprocessTestConfig(t))
	var exam string
	if err := f.db.QueryRowContext(f.ctx, `SELECT exam_id::text FROM scoring_run WHERE id=$1::uuid`, f.run).Scan(&exam); err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(f.ctx, `SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, f.tenant, exam); err != nil {
		t.Fatal(err)
	}
	var blocker int
	if err = tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "concurrent-finalize"})
		result <- err
	}()
	waitForSegmentLock(t, f, blocker, "pg_advisory_xact_lock", result)
	if _, err = tx.ExecContext(f.ctx, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,locked,status,created_by)
 SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,3,5,'single_review',false,'pending_confirmation',created_by FROM review_task WHERE id=$1::uuid`, f.review); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reprocess accepted concurrently finalized grade: %v", err)
	}
	assertSegmentReprocessProjectionsUnchanged(t, f)
}

type segmentProtectionBarrier struct {
	entered chan int
	release chan struct{}
}

func (b *segmentProtectionBarrier) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "EXISTS(SELECT 1 FROM review_task") && strings.Contains(data.SQL, "EXISTS(SELECT 1 FROM human_grade") {
		b.entered <- int(conn.PgConn().PID())
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*segmentProtectionBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func tracedSegmentReprocessStore(t *testing.T, f completedOMRFixture, cfg *pgx.ConnConfig, barrier *segmentProtectionBarrier) *PostgresStore {
	t.Helper()
	tracedConfig := cfg.Copy()
	var searchPath string
	if err := f.db.QueryRowContext(f.ctx, `SHOW search_path`).Scan(&searchPath); err != nil {
		t.Fatal(err)
	}
	tracedConfig.RuntimeParams["search_path"] = searchPath
	tracedConfig.Tracer = barrier
	db := stdlib.OpenDB(*tracedConfig)
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() {
		select {
		case <-barrier.release:
		default:
			close(barrier.release)
		}
	})
	return NewPostgresStore(db)
}

func insertSubmittedHumanGrade(f completedOMRFixture, tx *sql.Tx) error {
	if _, err := tx.ExecContext(f.ctx, `INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1,$2,$3,$4,3,5,'single')`, f.tenant, f.review, f.segment, f.actor); err != nil {
		return err
	}
	if _, err := tx.ExecContext(f.ctx, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, f.review); err != nil {
		return err
	}
	return nil
}

// 旧人工任务没有父运行锁：覆盖 task→human_grade 外键的真实 KEY SHARE，不能用修改题分来源代替。
func TestPostgresSegmentReprocessHumanForeignKeyLockOrder(t *testing.T) {
	cfg := segmentReprocessTestConfig(t)
	for _, first := range []string{"reprocess_segment_lock", "human_grade_insert"} {
		t.Run(first, func(t *testing.T) {
			f := seedGenericScoreReprocessFixture(t, cfg)
			f.exec(t, `UPDATE review_task SET status='in_progress',scoring_run_id=NULL,assigned_to=$2::uuid WHERE id=$1::uuid`, f.review, f.actor)
			tx, err := f.db.BeginTx(f.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			var task, blocker int
			if err = tx.QueryRowContext(f.ctx, `SELECT 1 FROM review_task WHERE id=$1::uuid FOR UPDATE`, f.review).Scan(&task); err != nil {
				t.Fatal(err)
			}
			if err = tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			if first == "reprocess_segment_lock" {
				barrier := &segmentProtectionBarrier{entered: make(chan int, 1), release: make(chan struct{})}
				store := tracedSegmentReprocessStore(t, f, cfg, barrier)
				go func() {
					_, err := store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "human-fk-race"})
					result <- err
				}()
				select {
				case blocker = <-barrier.entered:
				case err := <-result:
					t.Fatalf("protection barrier not reached: %v", err)
				case <-f.ctx.Done():
					t.Fatal(f.ctx.Err())
				}
				submitted := make(chan error, 1)
				go func() {
					err := insertSubmittedHumanGrade(f, tx)
					if err == nil {
						err = tx.Commit()
					}
					submitted <- err
				}()
				waitForSegmentLock(t, f, blocker, "INSERT INTO human_grade", submitted)
				close(barrier.release)
				if err = <-result; !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("reprocess did not reject active manual task: %v", err)
				}
				if err = <-submitted; err != nil {
					t.Fatalf("human submit failed after reprocess released segment: %v", err)
				}
			} else {
				if err = insertSubmittedHumanGrade(f, tx); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := f.store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "human-fk-first"})
					result <- err
				}()
				waitForSegmentLock(t, f, blocker, "FOR UPDATE OF seg", result)
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if err = <-result; !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("reprocess accepted committed human grade: %v", err)
				}
			}
			var humanGrades int
			if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM human_grade WHERE answer_segment_id=$1::uuid AND score=3`, f.segment).Scan(&humanGrades); err != nil || humanGrades != 1 {
				t.Fatalf("submitted human grade lost: count=%d err=%v", humanGrades, err)
			}
			assertSegmentReprocessProjectionsUnchanged(t, f)
		})
	}
}

func TestPostgresSegmentReprocessNewTaskForeignKeyBoundary(t *testing.T) {
	f := seedGenericScoreReprocessFixture(t, segmentReprocessTestConfig(t))
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(f.ctx, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,created_by)
 SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,'manual_sample','assigned',$2::uuid,created_by FROM review_task WHERE id=$1::uuid`, f.review, f.actor); err != nil {
		t.Fatal(err)
	}
	var blocker int
	if err = tx.QueryRowContext(f.ctx, `SELECT pg_backend_pid()`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.store.ReprocessSegmentScore(f.ctx, f.tenant, f.segment, f.actor, StartScoringRunInput{IdempotencyKey: "new-manual-task"})
		result <- err
	}()
	waitForSegmentLock(t, f, blocker, "FOR UPDATE OF seg", result)
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-result; !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reprocess missed task committed while waiting for segment: %v", err)
	}
	assertSegmentReprocessProjectionsUnchanged(t, f)
}

// 先从数据库确认目标操作已等在指定锁上，再放行另一事务；不靠短超时推断并发分支已执行。
func waitForSegmentLock(t *testing.T, f completedOMRFixture, blocker int, fragment string, result <-chan error) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		if err := f.db.QueryRowContext(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)) AND wait_event_type='Lock' AND strpos(query,$2)>0)`, blocker, fragment).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("operation finished before waiting for %s: %v", fragment, err)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertSegmentReprocessProjectionsUnchanged(t *testing.T, f completedOMRFixture) {
	t.Helper()
	var candidates, grades, runs int
	if err := f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM answer_candidate WHERE answer_segment_id=$1::uuid AND is_current),(SELECT count(*) FROM question_grade WHERE answer_segment_id=$1::uuid AND is_current AND score=3),(SELECT count(*) FROM scoring_run)`, f.segment).Scan(&candidates, &grades, &runs); err != nil || candidates != 1 || grades != 1 || runs != 1 {
		t.Fatalf("concurrent rejection changed facts: candidates=%d grades=%d runs=%d err=%v", candidates, grades, runs, err)
	}
}

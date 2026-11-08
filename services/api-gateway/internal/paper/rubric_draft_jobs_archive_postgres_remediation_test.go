package paper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestPostgresRubricDraftArchivePreviewAndGuardIncludeActiveSuggestions(t *testing.T) {
	for _, parent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_%t", parent), func(t *testing.T) {
			ctx, db, _, migrate := newRubricDraftArchivePostgresFixture(t)
			migrate()
			record, kind, target := seedRubricDraftArchivePostgresExam(t, ctx, db, parent)
			store, exams := NewPostgresStore(db), exam.NewPostgresStore(db)
			if _, err := store.createRubricDraftJob(ctx, record, false); err != nil {
				t.Fatal(err)
			}
			item, err := exams.GetManagementExam(ctx, record.Scope, kind, target)
			if err != nil {
				t.Fatal(err)
			}
			assertBlocked := func(status string) {
				t.Helper()
				check, err := exams.CheckManagementArchive(ctx, record.Scope, kind, target)
				if err != nil || check.Allowed {
					t.Fatalf("%s suggestion omitted from real archive preview: %#v %v", status, check, err)
				}
				found := false
				for _, blocker := range check.Blockers {
					found = found || blocker.Code == "unfinished_work"
				}
				if !found {
					t.Fatalf("active suggestion was not an unfinished-work blocker: %#v", check)
				}
				if _, err := exams.ArchiveManagementExam(ctx, record.Scope, record.ActorID, kind, target, rubricArchiveInput(item)); !errors.Is(err, exam.ErrArchiveBlocked) {
					t.Fatalf("actual archive crossed %s suggestion: %v", status, err)
				}
			}
			assertBlocked("queued")
			claimed, err := store.claimRubricDraftJob(ctx, time.Minute, record.Job.ID)
			if err != nil || claimed == nil {
				t.Fatalf("claim: %#v %v", claimed, err)
			}
			assertBlocked("running")
			if _, err := store.cancelRubricDraftJob(ctx, record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID); err != nil {
				t.Fatal(err)
			}
			assertBlocked("cancelling")
			if err := store.finishRubricDraftJob(ctx, *claimed, "succeeded", &MathRubricDraftResponse{}, "", time.Time{}); err != nil {
				t.Fatal(err)
			}
			archived, err := exams.ArchiveManagementExam(ctx, record.Scope, record.ActorID, kind, target, rubricArchiveInput(item))
			if err != nil || archived.ArchivedAt == nil {
				t.Fatalf("finished cancellation must permit archive: %#v %v", archived, err)
			}
			next := record
			next.Job.ID, next.Job.RequestID = uuid.NewString(), uuid.NewString()
			if _, err := store.createRubricDraftJob(ctx, next, false); !errors.Is(err, ErrExamFrozen) {
				t.Fatalf("archived draft business stage allowed new paid work: %v", err)
			}
			if claimed, err := store.claimRubricDraftJob(ctx, time.Minute, record.Job.ID); err != nil || claimed != nil {
				t.Fatalf("archived work reached execution: %#v %v", claimed, err)
			}
			current, err := store.getRubricDraftJob(ctx, record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID)
			if err != nil || current.Job.Status != "stale" || current.Job.Result != nil {
				t.Fatalf("archive result projection exposed a usable suggestion: %#v %v", current.Job, err)
			}
			_, err = db.ExecContext(ctx, `UPDATE question_rubric_draft_job SET status='succeeded' WHERE id=$1`, record.Job.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "55000" {
				t.Fatalf("formal migration guard allowed a direct late result: %v", err)
			}
			if _, err := exams.RestoreManagementExam(ctx, record.Scope, record.ActorID, kind, target, rubricArchiveInput(archived)); err != nil {
				t.Fatal(err)
			}
			if _, err := store.createRubricDraftJob(ctx, next, false); err != nil {
				t.Fatalf("explicit restore did not reopen task creation: %v", err)
			}
		})
	}
}

func TestPostgresRubricDraftCreateWaitsForRealArchiveAndRechecksAfterLock(t *testing.T) {
	ctx, db, application, migrate := newRubricDraftArchivePostgresFixture(t)
	migrate()
	record, kind, target := seedRubricDraftArchivePostgresExam(t, ctx, db, false)
	exams, store := exam.NewPostgresStore(db), NewPostgresStore(db)
	item, err := exams.GetManagementExam(ctx, record.Scope, kind, target)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1),hashtext($2))`, record.TenantID, record.Job.ExamID); err != nil {
		t.Fatal(err)
	}
	archived, created := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := exams.ArchiveManagementExam(ctx, record.Scope, record.ActorID, kind, target, rubricArchiveInput(item))
		archived <- err
	}()
	waitRubricArchiveAdvisory(t, ctx, db, application, 1)
	go func() {
		_, err := store.createRubricDraftJob(ctx, record, false)
		created <- err
	}()
	waitRubricArchiveAdvisory(t, ctx, db, application, 2)
	if err := blocker.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-archived; err != nil {
		t.Fatalf("real archive did not win its lock order: %v", err)
	}
	if err := <-created; !errors.Is(err, ErrExamFrozen) {
		t.Fatalf("waiting create did not reread committed archive: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM question_rubric_draft_job`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("archive/create race left queued work: count=%d err=%v", count, err)
	}
}

func TestPostgresRubricDraftArchiveMigrationStopsLegacyPendingWork(t *testing.T) {
	ctx, db, _, migrate := newRubricDraftArchivePostgresFixture(t)
	record, _, _ := seedRubricDraftArchivePostgresExam(t, ctx, db, true)
	store := NewPostgresStore(db)
	if _, err := store.createRubricDraftJob(ctx, record, false); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.claimRubricDraftJob(ctx, time.Minute, record.Job.ID)
	if err != nil || claimed == nil {
		t.Fatalf("claim legacy active fixture: %#v %v", claimed, err)
	}
	// 模拟 189 已建队列、215 尚未补防护的历史父场次归档，随后执行正式升级迁移。
	if _, err := db.ExecContext(ctx, `UPDATE exam_session SET archived_at=now() WHERE id=(SELECT exam_session_id FROM exam WHERE id=$1)`, record.Job.ExamID); err != nil {
		t.Fatal(err)
	}
	if err := store.finishRubricDraftJob(ctx, *claimed, "succeeded", &MathRubricDraftResponse{}, "", time.Time{}); !errors.Is(err, ErrExamFrozen) {
		t.Fatalf("in-flight result wrote across the parent archive: %v", err)
	}
	migrate()
	var status, token string
	var result []byte
	if err := db.QueryRowContext(ctx, `SELECT status,lease_token,payload->'Job'->'result' FROM question_rubric_draft_job WHERE id=$1`, record.Job.ID).Scan(&status, &token, &result); err != nil || status != "stale" || token != "" || string(result) != "null" {
		t.Fatalf("migration retained runnable archived work: status=%s token=%q result=%s err=%v", status, token, result, err)
	}
	if claimed, err := store.claimRubricDraftJob(ctx, time.Minute, record.Job.ID); err != nil || claimed != nil {
		t.Fatalf("legacy archived queue reached inference: %#v %v", claimed, err)
	}
}

func waitRubricArchiveAdvisory(t *testing.T, ctx context.Context, db *sql.DB, application string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND wait_event='advisory'`, application).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("did not observe %d real PostgreSQL advisory waiters", want)
}

func seedRubricDraftArchivePostgresExam(t *testing.T, ctx context.Context, db *sql.DB, parent bool) (rubricDraftJobRecord, string, string) {
	t.Helper()
	record := rubricDraftJobFixture()
	if _, err := db.ExecContext(ctx, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,appeal_enabled,publish_policy,created_by)
VALUES($1,$2,$3,'synthetic','math','midterm_exam',100,'draft','ai_assisted',false,'manual',$4)`, record.Job.ExamID, record.TenantID, uuid.NewString(), record.ActorID); err != nil {
		t.Fatal(err)
	}
	kind, target := "standalone", record.Job.ExamID
	if parent {
		kind, target = "session", uuid.NewString()
		if _, err := db.ExecContext(ctx, `INSERT INTO exam_session(id,tenant_id,school_id,grade_id,name,exam_type,grading_mode,appeal_enabled,publish_policy,created_by)
VALUES($1,$2,$3,$4,'synthetic','midterm_exam','ai_assisted',false,'manual',$5)`, target, record.TenantID, uuid.NewString(), uuid.NewString(), record.ActorID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `UPDATE exam SET exam_session_id=$2 WHERE id=$1`, record.Job.ExamID, target); err != nil {
			t.Fatal(err)
		}
	}
	return record, kind, target
}

func newRubricDraftArchivePostgresFixture(t *testing.T) (context.Context, *sql.DB, string, func()) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_RUBRIC_JOBS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_RUBRIC_JOBS_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "remediation_rubric_archive_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"], cfg.RuntimeParams["application_name"] = schema, schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remove synthetic archive schema: %v", err)
		}
		admin.Close()
	})
	// 复制实际查询用到的正式列/约束和 183 的保护函数；没有复制其他业务触发器、RLS 或历史业务数据。
	for _, table := range []string{
		"exam", "exam_session", "school_class", "exam_class", "exam_readiness_snapshot",
		// 归档预览复用考试列表的阅卷完成查询。即使没有答卷，SQL 解析也需要这些表。
		"exam_question_snapshot", "exam_candidate_snapshot", "submission", "submission_grade", "final_grade", "answer_segment",
		"score_release_current", "score_release", "capture_batch", "capture_upload_session", "scoring_run",
		"review_task", "paper_import_job", "ocr_task", "arbitration_task", "double_mark_session", "regrade_job",
		"question_appeal", "appeal", "operational_exception", "agent_worker_task", "subjective_grading_run", "subjective_grading_batch",
		"orchestration_run", "agent_task", "omr_run", "page_registration_run", "page_template_match_run",
		"question_rubric_draft_job", "exam_management_command", "exam_archive_guarded_table",
	} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"exam_archive_row_exams", "exam_archive_require_mutable", "reject_archived_exam_business_mutation"} {
		var definition string
		if err := admin.QueryRowContext(ctx, `SELECT pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname=$1`, name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		definition = strings.Replace(definition, "FUNCTION public."+name+"(", "FUNCTION "+schema+"."+name+"(", 1)
		if _, err := db.ExecContext(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db, schema, func() {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000215_rubric_draft_job_archive_guard.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

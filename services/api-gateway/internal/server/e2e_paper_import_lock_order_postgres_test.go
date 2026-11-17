package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// 完整迁移保留作业更新时的正式归档触发器。人为占住考试行，让来源变更停在可观察的锁等待点。
// 此时它必须已经持有作业锁，旧人工核对随后等待同一作业，不能反向占锁形成 40P01。
func TestPaperImportSourceChangesSerializeReviewWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for paper import lock order test")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	var tenant, actor, exam, file string
	err := db.QueryRowContext(t.Context(), `WITH actor AS (
 SELECT u.tenant_id,u.id FROM app_user u JOIN tenant t ON t.id=u.tenant_id WHERE t.code='demo' AND u.username='teacher'
), school_row AS (
 INSERT INTO school(tenant_id,name,code,status) SELECT tenant_id,'Synthetic import locks',$1,'active' FROM actor RETURNING tenant_id,id
), exam_row AS (
 INSERT INTO exam(tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by)
 SELECT a.tenant_id,s.id,'Synthetic import locks','mathematics','quiz',1,'draft','ai_assisted','manual_after_confirmation',a.id
 FROM actor a JOIN school_row s ON s.tenant_id=a.tenant_id RETURNING tenant_id,id,created_by
), file_row AS (
 INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
 SELECT tenant_id,id,'exam',id,'synthetic.pdf','application/pdf',1,repeat('a',64),'synthetic',$1,'tenant',created_by FROM exam_row RETURNING id
)
SELECT e.tenant_id::text,e.created_by::text,e.id::text,f.id::text FROM exam_row e CROSS JOIN file_row f`, uuid.NewString()).Scan(&tenant, &actor, &exam, &file)
	if err != nil {
		t.Fatal(err)
	}
	var addedFile string
	if err := db.QueryRowContext(t.Context(), `INSERT INTO file_asset(tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by)
VALUES($1,$2,'exam',$2,'synthetic-answer.pdf','application/pdf',1,repeat('b',64),'synthetic',$3,'tenant',$4) RETURNING id::text`, tenant, exam, uuid.NewString(), actor).Scan(&addedFile); err != nil {
		t.Fatal(err)
	}
	var databaseName string
	if err := db.QueryRowContext(t.Context(), `SELECT current_database()`).Scan(&databaseName); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"add", "replace"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			store := paper.NewPostgresStore(db)
			job, err := store.CreatePaperImport(ctx, tenant, exam, actor, paper.CreatePaperImportInput{
				Subject: "mathematics", Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: file, DocumentIndex: 0, RoleHint: "question"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			// 本项只验证核对与换代的并发，预置解析已进入人工核对的合成事实。
			if _, err := db.ExecContext(ctx, `UPDATE paper_import_job SET status='review_required' WHERE tenant_id=$1 AND id=$2`, tenant, job.ID); err != nil {
				t.Fatal(err)
			}
			job, err = store.GetPaperImport(ctx, tenant, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			openActor := func(application string) *sql.DB {
				t.Helper()
				cfg, err := pgx.ParseConfig(dsn)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Database = databaseName
				cfg.RuntimeParams["application_name"] = application
				pool := stdlib.OpenDB(*cfg)
				t.Cleanup(func() { _ = pool.Close() })
				return pool
			}
			sourceApplication, reviewApplication := "import-source-"+uuid.NewString(), "import-review-"+uuid.NewString()
			sources := paper.NewPostgresStore(openActor(sourceApplication))
			reviews := paper.NewPostgresStore(openActor(reviewApplication))
			blocker, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer blocker.Rollback()
			var blockerPID int
			if err := blocker.QueryRowContext(ctx, `SELECT pg_backend_pid() FROM exam WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenant, exam).Scan(&blockerPID); err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				job paper.PaperImportJob
				err error
			}
			changed, reviewed := make(chan outcome, 1), make(chan outcome, 1)
			go func() {
				var next paper.PaperImportJob
				var err error
				if operation == "add" {
					next, err = sources.AddPaperImportSources(ctx, tenant, job.ID, actor, paper.AddPaperImportSourcesInput{
						CommandID: uuid.NewString(), ExpectedGeneration: job.Generation, Sources: []paper.CreatePaperImportSourceInput{{FileAssetID: addedFile, DocumentIndex: 1, RoleHint: "answer"}},
					})
				} else {
					next, err = sources.ReplacePaperImportSources(ctx, tenant, job.ID, actor, paper.ReplacePaperImportSourcesInput{
						CommandID: uuid.NewString(), ExpectedGeneration: job.Generation, Sources: []paper.ReplacePaperImportSourceInput{{ID: job.Sources[0].ID, DocumentIndex: 0, RoleHint: "question"}},
					})
				}
				changed <- outcome{next, err}
			}()
			waitForLock := func(application, queryFragment string, blockedBy int, finished <-chan outcome) int {
				t.Helper()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					var pid int
					err := db.QueryRowContext(ctx, `SELECT pid FROM pg_stat_activity WHERE datname=current_database() AND application_name=$1 AND wait_event_type='Lock' AND query LIKE $2 AND $3=ANY(pg_blocking_pids(pid))`, application, "%"+queryFragment+"%", blockedBy).Scan(&pid)
					if err == nil {
						return pid
					}
					if !errors.Is(err, sql.ErrNoRows) {
						t.Fatal(err)
					}
					select {
					case result := <-finished:
						t.Fatalf("operation finished before required lock wait: %+v %v", result.job, result.err)
					case <-ctx.Done():
						t.Fatalf("did not observe lock wait for %s: %v", queryFragment, ctx.Err())
					case <-ticker.C:
					}
				}
			}
			sourcePID := waitForLock(sourceApplication, "SELECT status FROM exam", blockerPID, changed)
			// NOWAIT 在第三个事务检查真实作业锁；旧实现仍能锁住此行，因而会明确失败。
			probe, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, lockErr := probe.ExecContext(ctx, `SELECT id FROM paper_import_job WHERE tenant_id=$1 AND id=$2 FOR UPDATE NOWAIT`, tenant, job.ID)
			_ = probe.Rollback()
			var pgErr *pgconn.PgError
			if !errors.As(lockErr, &pgErr) || pgErr.Code != "55P03" {
				t.Fatalf("source change waited for exam before locking its job: %v", lockErr)
			}
			go func() {
				updated, err := reviews.SavePaperImportReview(ctx, tenant, job.ID, actor, paper.ReviewPaperImportInput{
					ExpectedGeneration: job.Generation, ExpectedUpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano), Questions: job.Questions,
				})
				reviewed <- outcome{updated, err}
			}()
			waitForLock(reviewApplication, "SELECT current_generation,status,updated_at FROM paper_import_job", sourcePID, reviewed)
			if err := blocker.Commit(); err != nil {
				t.Fatal(err)
			}
			next, saved := <-changed, <-reviewed
			if next.err != nil || next.job.Generation != job.Generation+1 {
				t.Fatalf("source change generation=%d, want %d: %v", next.job.Generation, job.Generation+1, next.err)
			}
			if !errors.Is(saved.err, paper.ErrConflict) || saved.job.ID != "" {
				t.Fatalf("stale review returned job=%q, want conflict with no result: %v", saved.job.ID, saved.err)
			}
			current, err := store.GetPaperImport(ctx, tenant, job.ID)
			if err != nil || current.Generation != next.job.Generation || current.RunID != next.job.RunID || current.Status != "processing" {
				t.Fatalf("stale review changed the new import generation: %+v %v", current, err)
			}
		})
	}
}

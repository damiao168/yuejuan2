package paper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type rubricDraftReplicaTestStore struct {
	rubricDraftJobStore
	replicas []rubricDraftJobStore
	next     atomic.Uint32
}

func (s *rubricDraftReplicaTestStore) claimRubricDraftJob(ctx context.Context, lease time.Duration, id string) (*rubricDraftJobRecord, error) {
	return s.replicas[int(s.next.Add(1))%len(s.replicas)].claimRubricDraftJob(ctx, lease, id)
}

// 正式迁移在唯一 schema 中执行，再切换到无 BYPASSRLS 权限的临时角色。
// 两个独立连接池共同领取不同租户的任务，验证数据库共享锁，而不是进程内互斥。
func TestRubricDraftJobsPostgresReplicasShareFiveSlotsWithRLS(t *testing.T) {
	left, right, prepare := newRubricDraftPostgresFixture(t)
	store := &rubricDraftReplicaTestStore{rubricDraftJobStore: left, replicas: []rubricDraftJobStore{left, right}}
	assertRubricDraftJobConcurrency(t, store, prepare)
}

func TestRubricDraftJobsPostgresLeaseExpiryDoesNotReplayAndRejectsLateResults(t *testing.T) {
	left, right, prepare := newRubricDraftPostgresFixture(t)
	for _, cancelling := range []bool{false, true} {
		record := rubricDraftJobFixture()
		prepare(record)
		seedRubricDraftJob(t, left, record)
		claimed, err := right.claimRubricDraftJob(t.Context(), time.Minute, record.Job.ID)
		if err != nil || claimed == nil {
			t.Fatalf("claim fixture: %#v err=%v", claimed, err)
		}
		if cancelling {
			if _, err = left.cancelRubricDraftJob(t.Context(), record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err = left.db.ExecContext(t.Context(), `UPDATE question_rubric_draft_job SET lease_until=now()-interval '1 second' WHERE id=$1`, record.Job.ID); err != nil {
			t.Fatal(err)
		}
		if err = left.finishRubricDraftJob(t.Context(), *claimed, "succeeded", &MathRubricDraftResponse{}, "", time.Time{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("expired executor published before cleanup: %v", err)
		}
		if next, err := right.claimRubricDraftJob(t.Context(), time.Minute, record.Job.ID); err != nil || next != nil {
			t.Fatalf("expired upstream call was replayed: %#v err=%v", next, err)
		}
		current, err := left.getRubricDraftJob(t.Context(), record.TenantID, record.ActorID, record.Job.ExamID, record.Job.ID)
		want := "failed"
		if cancelling {
			want = "cancelled"
		}
		if err != nil || current.Job.Status != want || current.Job.Result != nil || current.Job.Attempt != 1 || current.LeaseToken != "" || !current.LeaseUntil.IsZero() {
			t.Fatalf("interrupted call retained live lease/result: %#v err=%v", current, err)
		}
		if current.Job.Error == "" && !cancelling {
			t.Fatal("failure recovery lost the warning about possible upstream charges")
		}
		if err = right.finishRubricDraftJob(t.Context(), *claimed, "succeeded", &MathRubricDraftResponse{}, "", time.Time{}); !errors.Is(err, ErrConflict) {
			t.Fatalf("late executor overwrote recovered status: %v", err)
		}
	}
}

func TestRubricDraftJobsPostgresConcurrentDuplicateClicksCreateOneRound(t *testing.T) {
	left, right, prepare := newRubricDraftPostgresFixture(t)
	record := rubricDraftJobFixture()
	prepare(record)
	results := make(chan RubricDraftJob, 20)
	failures := make(chan error, 20)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		store := left
		if i%2 != 0 {
			store = right
		}
		next := record
		next.Job.ID, next.Job.RequestID = uuid.NewString(), uuid.NewString()
		workers.Add(1)
		go func(store *PostgresStore, next rubricDraftJobRecord) {
			defer workers.Done()
			job, err := store.createRubricDraftJob(t.Context(), next, false)
			if err != nil {
				failures <- err
			} else {
				results <- job
			}
		}(store, next)
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Errorf("concurrent duplicate creation: %v", err)
	}
	ids := map[string]bool{}
	for job := range results {
		ids[job.ID] = true
		if job.Status != "queued" || job.Attempt != 0 {
			t.Fatalf("creation executed a model call: %#v", job)
		}
	}
	var count int
	if err := left.db.QueryRowContext(t.Context(), `SELECT count(*) FROM question_rubric_draft_job`).Scan(&count); err != nil || len(ids) != 1 || count != 1 {
		t.Fatalf("cross-replica duplicate clicks created ids=%d rows=%d err=%v", len(ids), count, err)
	}
}

func newRubricDraftPostgresFixture(t *testing.T) (*PostgresStore, *PostgresStore, func(rubricDraftJobRecord)) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_RUBRIC_JOBS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_RUBRIC_JOBS_TEST_DATABASE_URL is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	suffix := time.Now().UnixNano()
	schema, role := fmt.Sprintf("review_rubric_jobs_%d", suffix), fmt.Sprintf("review_rubric_role_%d", suffix)
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("remove rubric fixture schema: %v", err)
		}
		if _, err := admin.ExecContext(cleanupCtx, `DROP ROLE IF EXISTS `+role); err != nil {
			t.Errorf("remove rubric fixture role: %v", err)
		}
	})
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		t.Fatal("test database requires a PostgreSQL URL")
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	setup, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = setup.Close() })
	for _, ddl := range []string{
		`CREATE TABLE tenant (id uuid PRIMARY KEY)`,
		`CREATE TABLE exam (id uuid PRIMARY KEY)`,
		`CREATE TABLE question (id uuid PRIMARY KEY)`,
		`CREATE FUNCTION edugrade_tenant_matches(candidate uuid) RETURNS boolean LANGUAGE sql STABLE AS $$ SELECT current_setting('edugrade.tenant_id',true) IN ('maintenance',candidate::text) $$`,
	} {
		if _, err = setup.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000189_question_rubric_draft_jobs.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = setup.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err = admin.ExecContext(ctx, `CREATE ROLE `+role+` NOLOGIN NOSUPERUSER NOBYPASSRLS`); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`GRANT USAGE ON SCHEMA ` + schema + ` TO ` + role,
		`GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA ` + schema + ` TO ` + role,
	} {
		if _, err = admin.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	// 这里只验证正式表的 RLS 与领取事务。应用按 context 绑定租户的连接包装器另有独立测试。
	query.Set("role", role)
	query.Set("edugrade.tenant_id", "maintenance")
	parsed.RawQuery = query.Encode()
	openReplica := func() *PostgresStore {
		database, err := sql.Open("pgx", parsed.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		database.SetMaxOpenConns(10)
		var current string
		var superuser, bypass bool
		if err = database.QueryRowContext(ctx, `SELECT current_user,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user`).Scan(&current, &superuser, &bypass); err != nil || current != role || superuser || bypass {
			t.Fatalf("fixture did not enable real RLS identity: user=%s superuser=%t bypass=%t err=%v", current, superuser, bypass, err)
		}
		return NewPostgresStore(database)
	}
	prepare := func(record rubricDraftJobRecord) {
		t.Helper()
		for _, item := range []struct{ table, id string }{{"tenant", record.TenantID}, {"exam", record.Job.ExamID}, {"question", record.Job.QuestionID}} {
			if _, err := setup.ExecContext(t.Context(), `INSERT INTO `+item.table+`(id) VALUES($1) ON CONFLICT DO NOTHING`, item.id); err != nil {
				t.Fatal(err)
			}
		}
	}
	return openReplica(), openReplica(), prepare
}

package opiniondraft

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

type privateDraftPGFixture struct {
	db                                                      *sql.DB
	store                                                   *PostgresStore
	tenant, actor, other, resource, exam, question, release string
}

// 随机schema建立触发器所需父资源列，并真实应用218；验证实际SQL与锁，不冒充完整学校业务流程。
func newPrivateDraftPGFixture(t *testing.T) privateDraftPGFixture {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "private_draft_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE tenant(id uuid PRIMARY KEY);
CREATE TABLE app_user(id uuid,tenant_id uuid,status text,deleted_at timestamptz,UNIQUE(tenant_id,id));
CREATE TABLE role(id uuid,tenant_id uuid,deleted_at timestamptz);
CREATE TABLE permission(id uuid,tenant_id uuid,code text,deleted_at timestamptz);
CREATE TABLE user_role(user_id uuid,tenant_id uuid,role_id uuid,deleted_at timestamptz);
CREATE TABLE role_permission(role_id uuid,tenant_id uuid,permission_id uuid,deleted_at timestamptz);
CREATE TABLE question_appeal(id uuid,tenant_id uuid,source_release_id uuid,revision bigint,status text,assigned_to uuid);
CREATE TABLE question_appeal_event(tenant_id uuid,appeal_id uuid,actor_id uuid,event_type text);
CREATE TABLE appeal(id uuid,tenant_id uuid,exam_id uuid,final_grade_id uuid,submission_grade_id uuid,revision bigint,status text,assigned_to uuid,deleted_at timestamptz,closed_at timestamptz,closed_by uuid,reviewed_at timestamptz,reviewed_by uuid,teacher_recommendation_at timestamptz,teacher_recommendation_by uuid);
CREATE TABLE arbitration_task(id uuid,tenant_id uuid,exam_id uuid,question_id uuid,revision bigint,status text,assigned_to uuid);`)
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000218_private_opinion_drafts.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(string(migration))
	f := privateDraftPGFixture{db: db, store: NewPostgresStore(db), tenant: uuid.NewString(), actor: uuid.NewString(), other: uuid.NewString(), resource: uuid.NewString(), exam: uuid.NewString(), question: uuid.NewString(), release: uuid.NewString()}
	exec(`INSERT INTO tenant VALUES($1)`, f.tenant)
	exec(`INSERT INTO app_user VALUES($1,$3,'active',NULL),($2,$3,'active',NULL)`, f.actor, f.other, f.tenant)
	role := uuid.NewString()
	exec(`INSERT INTO role VALUES($1,$2,NULL);`, role, f.tenant)
	exec(`INSERT INTO user_role VALUES($1,$2,$3,NULL)`, f.actor, f.tenant, role)
	for _, code := range []string{"appeal:work", "arbitration:work"} {
		permission := uuid.NewString()
		exec(`INSERT INTO permission VALUES($1,$2,$3,NULL)`, permission, f.tenant, code)
		exec(`INSERT INTO role_permission VALUES($1,$2,$3,NULL)`, role, f.tenant, permission)
	}
	return f
}

func (f privateDraftPGFixture) seed(t *testing.T, kind string) string {
	t.Helper()
	var query, source string
	args := []any{f.resource, f.tenant, f.actor, f.exam, f.question}
	switch kind {
	case "question-appeal":
		query = `INSERT INTO question_appeal VALUES($1,$2,$4,1,'under_review',$3)`
		source = f.exam
		args = args[:4]
	case "legacy-appeal":
		query = `INSERT INTO appeal(id,tenant_id,assigned_to,exam_id,final_grade_id,revision,status) VALUES($1,$2,$3,$4,$5,1,'under_review')`
		source = f.exam + ":" + f.question
	case "arbitration":
		query = `INSERT INTO arbitration_task VALUES($1,$2,$4,$5,1,'assigned',$3)`
		source = f.exam + ":" + f.question
	}
	if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
	return source
}

func draftFor(source string, revision int64) *Draft {
	now := time.Now().UTC()
	return &Draft{Source: source, SourceRevision: revision, Values: map[string]any{"reason": "synthetic private opinion"}, UpdatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
}

func (f privateDraftPGFixture) submit(t *testing.T, tx *sql.Tx, kind string) {
	t.Helper()
	var query string
	args := []any{f.tenant, f.resource, f.actor}
	switch kind {
	case "question-appeal":
		if _, err := tx.ExecContext(t.Context(), `UPDATE question_appeal SET revision=revision+1 WHERE tenant_id=$1 AND id=$2`, f.tenant, f.resource); err != nil {
			t.Fatal(err)
		}
		query = `INSERT INTO question_appeal_event VALUES($1,$2,$3,'review_opinion_submitted')`
	case "legacy-appeal":
		query = `UPDATE appeal SET revision=revision+1,reviewed_at=now(),reviewed_by=$3 WHERE tenant_id=$1 AND id=$2`
	case "arbitration":
		query = `UPDATE arbitration_task SET revision=revision+1,status='submitted' WHERE tenant_id=$1 AND id=$2`
		args = args[:2]
	}
	if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPrivateDraftSubmissionClearsOnlyActorAndRejectsLateSave(t *testing.T) {
	for _, kind := range []string{"question-appeal", "legacy-appeal", "arbitration"} {
		t.Run(kind, func(t *testing.T) {
			f := newPrivateDraftPGFixture(t)
			source := f.seed(t, kind)
			first, err := f.store.Put(t.Context(), f.tenant, f.actor, kind, f.resource, 0, draftFor(source, 1))
			if err != nil {
				t.Fatal(err)
			}
			if other, err := f.store.Get(t.Context(), f.tenant, f.other, kind, f.resource); err != nil || other.Draft != nil || other.Revision != 0 {
				t.Fatalf("other actor=%+v error=%v", other, err)
			}
			if _, err := f.store.Put(t.Context(), f.tenant, f.actor, kind, f.resource, 0, draftFor(source, 1)); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale CAS=%v", err)
			}
			tx, err := f.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			f.submit(t, tx, kind)
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
			after, err := f.store.Get(t.Context(), f.tenant, f.actor, kind, f.resource)
			if err != nil || after.Draft != nil || after.Revision <= first.Revision {
				t.Fatalf("submitted draft=%+v error=%v", after, err)
			}
			if _, err := f.store.Put(t.Context(), f.tenant, f.actor, kind, f.resource, after.Revision, draftFor(source, 1)); !errors.Is(err, ErrConflict) {
				t.Fatalf("late save resurrected source: %v", err)
			}
		})
	}
}

func TestPostgresPrivateDraftFormalCommitBeforeFirstSaveCannotResurrect(t *testing.T) {
	f := newPrivateDraftPGFixture(t)
	source := f.seed(t, "arbitration")
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	f.submit(t, tx, "arbitration")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := f.store.Put(ctx, f.tenant, f.actor, "arbitration", f.resource, 0, draftFor(source, 1))
		finished <- err
	}()
	// 父资源仍未提交时保存必须等待；不能先创建空expected=0的旧稿越过清理。
	select {
	case err := <-finished:
		t.Fatalf("save bypassed parent lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; !errors.Is(err, ErrConflict) {
		t.Fatalf("late first save=%v", err)
	}
	item, err := f.store.Get(t.Context(), f.tenant, f.actor, "arbitration", f.resource)
	if err != nil || item.Draft != nil || item.Revision != 0 {
		t.Fatalf("resurrected=%+v error=%v", item, err)
	}
}

func TestPostgresPrivateDraftSaveBeforeFormalCommitIsCleared(t *testing.T) {
	f := newPrivateDraftPGFixture(t)
	source := f.seed(t, "arbitration")
	tx, err := f.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 与Store相同UPSERT在独立事务中保留父锁，验证正式更新等待后清稿。
	if _, err = tx.ExecContext(t.Context(), `INSERT INTO private_opinion_draft(tenant_id,actor_id,kind,record_id,revision,draft_json) VALUES($1,$2,'arbitration',$3,1,jsonb_build_object('source',$4::text,'source_revision',1,'values','{}'::jsonb,'updated_at',now(),'expires_at',now()+interval '7 days'))`, f.tenant, f.actor, f.resource, source); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := f.db.ExecContext(ctx, `UPDATE arbitration_task SET revision=revision+1,status='submitted' WHERE tenant_id=$1 AND id=$2`, f.tenant, f.resource)
		finished <- err
	}()
	select {
	case err := <-finished:
		t.Fatalf("formal submit bypassed parent lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	item, err := f.store.Get(t.Context(), f.tenant, f.actor, "arbitration", f.resource)
	if err != nil || item.Draft != nil || item.Revision != 2 {
		t.Fatalf("formal cleanup=%+v error=%v", item, err)
	}
}

package exam

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// publishedReleaseTestDB 在 EDUGRADE_E2E_DATABASE_URL 指定的测试实例中新建随机库并应用正式迁移，结束后删除；
// 不在 DSN 指向的库内写数据。未设置 DSN 时跳过。
func publishedReleaseTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for isolated score-release PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	name := "published_release_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	config.Database = name
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		_ = admin.Close()
	})
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(t.Context(), string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	return db
}

// 只有当前指针指向已正式发布版本才算已发布：exam.status、草稿和其他租户的发布都不能让考试显示为已发布。
func TestPublishedExamIDsWithPostgresUsesCurrentStudentRelease(t *testing.T) {
	db := publishedReleaseTestDB(t)
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var tenant, actor string
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='teacher'`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	otherTenant, otherActor := uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1::uuid,$1::uuid,'Synthetic release tenant',$1::text,'active')`, otherTenant)
	exec(`INSERT INTO app_user(id,tenant_id,username,display_name,password_hash,status) VALUES($1::uuid,$2::uuid,'release-teacher','Synthetic teacher','synthetic-hash','active')`, otherActor, otherTenant)
	schools := map[string]string{tenant: uuid.NewString(), otherTenant: uuid.NewString()}
	for tenantID, schoolID := range schools {
		exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic release school',$1::text,'active')`, schoolID, tenantID)
	}
	newExam := func(tenantID, actorID, status string) string {
		t.Helper()
		id := uuid.NewString()
		exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic release exam','physics','quiz',10,'draft','human_review_required','manual_after_confirmation',$4)`, id, tenantID, schools[tenantID], actorID)
		exec(`UPDATE exam SET status=$2 WHERE id=$1::uuid`, id, status)
		return id
	}
	release := func(tenantID, examID, actorID string, version int, published bool) string {
		t.Helper()
		id := uuid.NewString()
		status, publishedBy, publishedAt := "draft", any(nil), any(nil)
		if published {
			status, publishedBy, publishedAt = "published", actorID, time.Now().UTC()
		}
		exec(`INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,status,idempotency_key,created_by,published_by,published_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'initial','synthetic release',$5,$6,$7::uuid,$8::uuid,$9)`,
			id, tenantID, examID, version, status, uuid.NewString(), actorID, publishedBy, publishedAt)
		return id
	}
	makeCurrent := func(tenantID, examID, releaseID string) {
		t.Helper()
		exec(`INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, tenantID, examID, releaseID)
	}

	// 学生端发布后考试仍停在 finalized；之后新建的草稿版本不能遮住当前已发布版本。
	released := newExam(tenant, actor, "finalized")
	makeCurrent(tenant, released, release(tenant, released, actor, 1, true))
	release(tenant, released, actor, 2, false)
	draftOnly := newExam(tenant, actor, "finalized")
	release(tenant, draftOnly, actor, 1, false)
	// 只改状态文字没有发布记录，不能被当成学生已能看到成绩。
	legacyStatus := newExam(tenant, actor, "published")
	notRequested := newExam(tenant, actor, "grading")
	makeCurrent(tenant, notRequested, release(tenant, notRequested, actor, 1, true))
	otherReleased := newExam(otherTenant, otherActor, "finalized")
	makeCurrent(otherTenant, otherReleased, release(otherTenant, otherReleased, otherActor, 1, true))

	store := NewPostgresStore(db)
	got, err := store.PublishedExamIDs(ctx, tenant, []string{released, draftOnly, legacyStatus, otherReleased})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]bool{released: true}) {
		t.Fatalf("published=%v, want only the current release of the requested tenant exam %s", got, released)
	}
	// 同一夹具按所属租户读取可见，说明上面没有返回是租户条件生效，而不是夹具无效。
	if got, err := store.PublishedExamIDs(ctx, otherTenant, []string{otherReleased}); err != nil || !got[otherReleased] {
		t.Fatalf("other tenant release=%v err=%v", got, err)
	}
	if got, err := store.PublishedExamIDs(ctx, tenant, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty request=%v err=%v", got, err)
	}
}

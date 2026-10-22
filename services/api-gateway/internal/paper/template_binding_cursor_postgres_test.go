package paper

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// 显式指定测试数据库后才创建唯一 schema；所有应用表和测试写入都在该 schema 内。
// 父表使用最小夹具，绑定及游标表执行正式迁移；这不代替完整数据库的迁移验收。
func TestPostgresTemplateBindingCursorSurvivesConcurrentRecreation(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("review_binding_cursor_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, cleanupErr := admin.ExecContext(cleanupCtx, `DROP SCHEMA `+schema+` CASCADE`); cleanupErr != nil {
			t.Errorf("remove fixture schema: %v", cleanupErr)
		}
	}()
	parsedDSN, err := url.Parse(dsn)
	if err != nil || parsedDSN.Scheme == "" {
		t.Fatalf("fixture requires a PostgreSQL URL: %v", err)
	}
	query := parsedDSN.Query()
	query.Set("search_path", schema)
	parsedDSN.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsedDSN.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	for _, ddl := range []string{
		`CREATE TABLE exam (tenant_id uuid,id uuid,UNIQUE(tenant_id,id))`,
		`CREATE TABLE app_user (tenant_id uuid,id uuid,UNIQUE(tenant_id,id))`,
		`CREATE TABLE answer_sheet_template (tenant_id uuid,id uuid,exam_id uuid,content_hash text,status text,deleted_at timestamptz,layout jsonb NOT NULL DEFAULT '{"pages":[]}',UNIQUE(tenant_id,id))`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	legacy, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000131_exam_answer_sheet_template_binding.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, strings.Split(string(legacy), "ALTER TABLE page_registration_run")[0]); err != nil {
		t.Fatal(err)
	}
	const tenant = "00000000-0000-0000-0000-000000000101"
	const exam = "00000000-0000-0000-0000-000000000102"
	const legacyExam = "00000000-0000-0000-0000-000000000103"
	const actor = "00000000-0000-0000-0000-000000000104"
	const templateID = "00000000-0000-0000-0000-000000000105"
	const legacyTemplate = "00000000-0000-0000-0000-000000000106"
	for _, examID := range []string{exam, legacyExam} {
		if _, err = db.ExecContext(ctx, `INSERT INTO exam VALUES ($1::uuid,$2::uuid)`, tenant, examID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO app_user VALUES ($1::uuid,$2::uuid)`, tenant, actor); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{exam, templateID}, {legacyExam, legacyTemplate}} {
		if _, err = db.ExecContext(ctx, `INSERT INTO answer_sheet_template (tenant_id,id,exam_id,content_hash,status,deleted_at) VALUES ($1::uuid,$2::uuid,$3::uuid,'sha256:fixture','locked',NULL)`, tenant, pair[1], pair[0]); err != nil {
			t.Fatal(err)
		}
	}
	// 迁移不能把已存在的修订号重新设为 0；应用先读到 7，解绑和重建后应依次得到 8、9。
	if _, err = db.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding (tenant_id,exam_id,template_id,template_content_hash,revision,bound_by) VALUES ($1::uuid,$2::uuid,$3::uuid,'sha256:fixture',7,$4::uuid)`, tenant, legacyExam, legacyTemplate, actor); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000186_exam_template_binding_revision.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	assertTemplateBindingCursorLifecycle(t, store, tenant, exam, actor, templateID)
	state, err := store.GetExamTemplateBindingState(ctx, tenant, legacyExam)
	if err != nil || state.Revision != 7 || state.Binding == nil || state.Binding.Revision != 7 {
		t.Fatalf("migration backfill: %#v err=%v", state, err)
	}
	if _, err = store.UnbindExamTemplate(ctx, tenant, legacyExam, UnbindExamTemplateInput{ExpectedRevision: state.Revision, Reason: "legacy replacement"}); err != nil {
		t.Fatal(err)
	}
	state, err = store.GetExamTemplateBindingState(ctx, tenant, legacyExam)
	if err != nil || state.Revision != 8 || state.Binding != nil {
		t.Fatalf("legacy unbind cursor: %#v err=%v", state, err)
	}
	rebound, err := store.BindExamTemplate(ctx, tenant, legacyExam, actor, BindExamTemplateInput{TemplateID: legacyTemplate, ExpectedRevision: state.Revision})
	if err != nil || rebound.Revision != 9 {
		t.Fatalf("legacy recreation: %#v err=%v", rebound, err)
	}
	other, err := store.GetExamTemplateBindingState(ctx, "00000000-0000-0000-0000-000000000999", exam)
	if err != nil || other.Binding != nil || other.Revision != 0 {
		t.Fatalf("another tenant must not see the cursor: %#v err=%v", other, err)
	}
	t.Log("migration backfill=7; legacy unbind=8 and recreate=9; concurrent recreate: one winner at revision=3; stale 0/1 rejected")
}

package capture

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 自动匹配也是绑定的写入口；本测试执行实际事务函数，检查它与管理员命令共享同一递增游标。
// 仅创建独立 schema 的最小父表和正式绑定迁移，不运行采集工作进程或完整页面配准链路。
func TestPostgresAutomaticTemplateBindingSharesRevisionCursor(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("review_auto_binding_%d", time.Now().UnixNano())
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
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		t.Fatalf("fixture requires a PostgreSQL URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("pgx", parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	for _, ddl := range []string{
		`CREATE TABLE exam (tenant_id uuid,id uuid,UNIQUE(tenant_id,id))`,
		`CREATE TABLE app_user (tenant_id uuid,id uuid,UNIQUE(tenant_id,id))`,
		`CREATE TABLE answer_sheet_template (tenant_id uuid,id uuid,exam_id uuid,content_hash text,status text,deleted_at timestamptz,UNIQUE(tenant_id,id))`,
	} {
		if _, err = db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, migrationName := range []string{"000131_exam_answer_sheet_template_binding.sql", "000186_exam_template_binding_revision.sql"} {
		raw, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", migrationName))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.ExecContext(ctx, strings.Split(string(raw), "ALTER TABLE page_registration_run")[0]); err != nil {
			t.Fatal(err)
		}
	}
	const tenant = "00000000-0000-0000-0000-000000000201"
	const exam = "00000000-0000-0000-0000-000000000202"
	const actor = "00000000-0000-0000-0000-000000000203"
	const templateID = "00000000-0000-0000-0000-000000000204"
	const competingTemplateID = "00000000-0000-0000-0000-000000000205"
	if _, err = db.ExecContext(ctx, `INSERT INTO exam VALUES ($1::uuid,$2::uuid)`, tenant, exam); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO app_user VALUES ($1::uuid,$2::uuid)`, tenant, actor); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO answer_sheet_template VALUES ($1::uuid,$2::uuid,$3::uuid,'sha256:auto','locked',NULL)`, tenant, templateID, exam); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO answer_sheet_template VALUES ($1::uuid,$2::uuid,$3::uuid,'sha256:other','locked',NULL)`, tenant, competingTemplateID, exam); err != nil {
		t.Fatal(err)
	}
	autoBind := func() {
		t.Helper()
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			t.Fatal(beginErr)
		}
		defer tx.Rollback()
		decision, bindErr := bindMatchedTemplateInTx(ctx, tx, tenant, TemplateMatchRun{ExamID: exam, CreatedBy: actor}, TemplateMatchResultInput{SelectedTemplateID: templateID, SelectedTemplateContentHash: "sha256:auto"})
		if bindErr != nil || decision != "matched" {
			t.Fatalf("automatic binding: decision=%s err=%v", decision, bindErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	store := paper.NewPostgresStore(db)
	autoBind()
	autoBind()
	state, err := store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || state.Binding == nil || state.Binding.Revision != 1 || state.Revision != 1 || state.Binding.Source != "automatic" {
		t.Fatalf("first auto binding/repeat: %#v err=%v", state, err)
	}
	manual, err := store.BindExamTemplate(ctx, tenant, exam, actor, paper.BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: state.Revision})
	if err != nil || manual.Revision != 2 {
		t.Fatalf("replace automatic binding: %#v err=%v", manual, err)
	}
	if _, err = store.UnbindExamTemplate(ctx, tenant, exam, paper.UnbindExamTemplateInput{ExpectedRevision: manual.Revision, Reason: "rematch next page"}); err != nil {
		t.Fatal(err)
	}
	autoBind()
	state, err = store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || state.Binding == nil || state.Revision != 4 || state.Binding.Revision != 4 {
		t.Fatalf("automatic recreation must use the retained cursor: %#v err=%v", state, err)
	}
	for _, stale := range []int{0, 1, 2} {
		if _, err = store.UnbindExamTemplate(ctx, tenant, exam, paper.UnbindExamTemplateInput{ExpectedRevision: stale, Reason: "old editor"}); !errors.Is(err, paper.ErrConflict) {
			t.Fatalf("stale removal %d after automatic recreation: %v", stale, err)
		}
	}
	t.Log("automatic first/repeat=1; manual replacement=2; unbind=3; automatic recreation=4; old revisions rejected")
	if _, err = store.UnbindExamTemplate(ctx, tenant, exam, paper.UnbindExamTemplateInput{ExpectedRevision: state.Revision, Reason: "test competing automatic and manual binding"}); err != nil {
		t.Fatal(err)
	}
	// 自动绑定失败时回滚绑定行和游标；管理员仍可使用解绑后读到的同一个版本提交。
	aborted, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if decision, bindErr := bindMatchedTemplateInTx(ctx, aborted, tenant, TemplateMatchRun{ExamID: exam, CreatedBy: actor}, TemplateMatchResultInput{SelectedTemplateID: competingTemplateID, SelectedTemplateContentHash: "sha256:other"}); bindErr != nil || decision != "matched" {
		aborted.Rollback()
		t.Fatalf("pre-rollback automatic binding: decision=%s err=%v", decision, bindErr)
	}
	if err = aborted.Rollback(); err != nil {
		t.Fatal(err)
	}
	state, err = store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || state.Binding != nil || state.Revision != 5 {
		t.Fatalf("rollback must preserve the unbound cursor: %#v err=%v", state, err)
	}
	type operationResult struct {
		kind     string
		decision string
		err      error
	}
	start := make(chan struct{})
	results := make(chan operationResult, 2)
	go func() {
		<-start
		_, bindErr := store.BindExamTemplate(ctx, tenant, exam, actor, paper.BindExamTemplateInput{TemplateID: templateID, ExpectedRevision: 5})
		results <- operationResult{kind: "manual", err: bindErr}
	}()
	go func() {
		<-start
		tx, beginErr := db.BeginTx(ctx, nil)
		if beginErr != nil {
			results <- operationResult{kind: "automatic", err: beginErr}
			return
		}
		defer tx.Rollback()
		decision, bindErr := bindMatchedTemplateInTx(ctx, tx, tenant, TemplateMatchRun{ExamID: exam, CreatedBy: actor}, TemplateMatchResultInput{SelectedTemplateID: competingTemplateID, SelectedTemplateContentHash: "sha256:other"})
		if bindErr == nil {
			bindErr = tx.Commit()
		}
		results <- operationResult{kind: "automatic", decision: decision, err: bindErr}
	}()
	// 两个入口同时争用空绑定，选中不同模板；后者只能报告冲突，不能覆盖先提交的版本。
	close(start)
	var manualResult, automaticResult operationResult
	for range 2 {
		result := <-results
		if result.kind == "manual" {
			manualResult = result
		} else {
			automaticResult = result
		}
	}
	if automaticResult.err != nil {
		t.Fatalf("competing automatic binding: %v", automaticResult.err)
	}
	state, err = store.GetExamTemplateBindingState(ctx, tenant, exam)
	if err != nil || state.Binding == nil || state.Binding.Revision != 6 || state.Revision != 6 {
		t.Fatalf("competing binding must consume exactly one revision: %#v err=%v", state, err)
	}
	switch {
	case manualResult.err == nil:
		if automaticResult.decision != "conflict" || state.Binding.TemplateID != templateID {
			t.Fatalf("automatic match overwrote manual winner: auto=%s state=%#v", automaticResult.decision, state)
		}
	case errors.Is(manualResult.err, paper.ErrConflict):
		if automaticResult.decision != "matched" || state.Binding.TemplateID != competingTemplateID {
			t.Fatalf("manual request overwrote automatic winner: auto=%s state=%#v", automaticResult.decision, state)
		}
	default:
		t.Fatalf("competing manual binding: %v", manualResult.err)
	}
	t.Logf("rolled-back automatic creation preserved cursor=5; concurrent automatic/manual different templates produced one binding at cursor=6, automatic decision=%s", automaticResult.decision)
}

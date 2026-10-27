package exam

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestTemplateListAndDetailUseOnePostgresConnection(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL test DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "template_pool_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for _, table := range []string{"exam_template", "exam_template_subject", "exam_template_section"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	tenant, first := uuid.NewString(), ""
	for _, code := range []string{"a", "b"} {
		id := uuid.NewString()
		if first == "" {
			first = id
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO exam_template(id,tenant_id,code,name,education_stage,source) VALUES($1,$2,$3,$3,'senior','school')`, id, tenant, code); err != nil {
			t.Fatal(err)
		}
		for index, subject := range []string{"math", "physics"} {
			subjectID := uuid.NewString()
			if _, err = db.ExecContext(ctx, `INSERT INTO exam_template_subject(id,tenant_id,exam_template_id,subject_code,total_score,duration_minutes,sort_order) VALUES($1,$2,$3,$4,100,90,$5)`, subjectID, tenant, id, subject, index); err != nil {
				t.Fatal(err)
			}
			for _, section := range []int{1, 2} {
				if _, err = db.ExecContext(ctx, `INSERT INTO exam_template_section(tenant_id,exam_template_subject_id,title,question_type,question_count,score_per_question,sort_order) VALUES($1,$2,'synthetic','short_answer',5,10,$3)`, tenant, subjectID, section); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	store := NewPostgresStore(db)
	scope := auth.AccessScope{TenantID: tenant, TenantWide: true}
	items, err := store.ListExamTemplates(ctx, scope, ExamTemplateFilter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("list=%#v err=%v", items, err)
	}
	for _, item := range items {
		if len(item.Subjects) != 2 || len(item.Subjects[0].Sections) != 2 || len(item.Subjects[1].Sections) != 2 {
			t.Fatalf("descendants missing: %#v", item)
		}
	}
	item, err := store.GetExamTemplate(ctx, scope, first)
	if err != nil || item.ID != first || len(item.Subjects) != 2 {
		t.Fatalf("detail=%#v err=%v", item, err)
	}
	if db.Stats().WaitCount != 0 {
		t.Fatalf("nested reads still wait for own pool: %#v", db.Stats())
	}
}

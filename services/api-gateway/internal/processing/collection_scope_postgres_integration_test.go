package processing

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

// 最小合成表运行真实列表及投影时间 SQL；只创建本轮自有集群中的唯一 schema。
func TestPostgresExceptionCollectionUsesSameSchoolClassExamScopeForProjection(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("set YUEJUAN_REVIEW_PG_DSN for the isolated review PostgreSQL")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 55983 || cfg.User != "review_actor" || !strings.HasPrefix(cfg.Database, "review_") {
		t.Fatal("integration fixture requires the owned review cluster and database")
	}
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "review_processing_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, ddl := range []string{
		`CREATE TABLE exam(tenant_id uuid,id uuid,school_id uuid,subject text,deleted_at timestamptz)`,
		`CREATE TABLE exam_class(tenant_id uuid,exam_id uuid,class_id uuid,deleted_at timestamptz)`,
		`CREATE TABLE operational_exception(tenant_id uuid,id uuid,exam_id uuid,page_id uuid,source_type text,source_id uuid,code text,severity text,blocking bool,status text,assigned_to uuid,details_json jsonb,created_at timestamptz,updated_at timestamptz,resolved_at timestamptz,resolution text)`,
		`CREATE TABLE submission_page_processing_state(tenant_id uuid,page_id uuid,exam_id uuid,current_stage text)`,
		`CREATE TABLE app_user(tenant_id uuid,id uuid,school_id uuid,display_name text,username text,deleted_at timestamptz)`,
		`CREATE TABLE submission_page(tenant_id uuid,id uuid,submission_id uuid,page_no integer,deleted_at timestamptz)`,
		`CREATE TABLE submission(tenant_id uuid,id uuid,exam_id uuid,candidate_no text,deleted_at timestamptz)`,
		`CREATE TABLE processing_projection_cursor(tenant_id uuid,exam_id uuid,projected_at timestamptz)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	tenant, schoolA, schoolB := uuid.NewString(), uuid.NewString(), uuid.NewString()
	classA, classB, classC := uuid.NewString(), uuid.NewString(), uuid.NewString()
	exams := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	base := time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC)
	for i, school := range []string{schoolA, schoolA, schoolB} {
		exam, page := exams[i], uuid.NewString()
		at := base.Add(time.Duration(i) * time.Hour)
		for _, statement := range []struct {
			query string
			args  []any
		}{
			{`INSERT INTO exam VALUES($1,$2,$3,'mathematics',NULL)`, []any{tenant, exam, school}},
			{`INSERT INTO exam_class VALUES($1,$2,$3,NULL)`, []any{tenant, exam, []string{classA, classB, classC}[i]}},
			{`INSERT INTO operational_exception VALUES($1,$2,$3,$4,'submission_page',$4,'BLOCKED_LOW_IMAGE_QUALITY','P0',true,'open',NULL,'{}',$5,$5,NULL,NULL)`, []any{tenant, uuid.NewString(), exam, page, at}},
			{`INSERT INTO submission_page_processing_state VALUES($1,$2,$3,'QUALITY_CHECKED')`, []any{tenant, page, exam}},
			{`INSERT INTO processing_projection_cursor VALUES($1,$2,$3)`, []any{tenant, exam, at}},
		} {
			if _, err := db.ExecContext(ctx, statement.query, statement.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	schoolScope, err := auth.ResolveDeclaredAccessScope(auth.User{TenantID: tenant, ID: "school-admin", DataScope: map[string]any{"scope": "school", "school_id": schoolA}})
	if err != nil {
		t.Fatal(err)
	}
	classScope := auth.AccessScope{TenantID: tenant, ActorID: "teacher", SchoolIDs: []string{schoolA}, ClassIDs: []string{classA}}
	store := NewPostgresStore(db)
	for _, tc := range []struct {
		name  string
		scope auth.AccessScope
		exam  string
		want  string
		more  bool
		at    time.Time
	}{
		{"school", schoolScope, "", exams[1], true, base.Add(time.Hour)},
		{"class", classScope, "", exams[0], false, base},
		{"foreign-school-filter", schoolScope, exams[2], "", false, time.Time{}},
		{"sibling-class-filter", classScope, exams[1], "", false, time.Time{}},
		{"empty-exam-grants", auth.AccessScope{TenantID: tenant, ActorID: "none"}, "", "", false, time.Time{}},
		{"explicit-exam", auth.AccessScope{TenantID: tenant, ActorID: "exam", ExamIDs: []string{exams[2]}}, "", exams[2], false, base.Add(2 * time.Hour)},
		{"tenant", auth.AccessScope{TenantID: tenant, ActorID: "admin", TenantWide: true}, "", exams[2], true, base.Add(2 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			filter := ExceptionFilter{ExamID: tc.exam, Limit: 1, Access: auth.ExamCollectionAccess{Scope: &tc.scope}}
			out, err := store.ListExceptions(ctx, tenant, filter)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(out.Exceptions) != 0 || out.ProjectedAt != nil || out.NextCursor != "" || out.HasMore {
					t.Fatalf("scope leaked: %+v", out)
				}
				return
			}
			if len(out.Exceptions) != 1 || out.Exceptions[0].ExamID != tc.want || out.HasMore != tc.more || out.ProjectedAt == nil || !out.ProjectedAt.Equal(tc.at) {
				t.Fatalf("scope=%s result=%+v", tc.name, out)
			}
			if tc.more {
				filter.CursorAt, filter.CursorID = out.Exceptions[0].CreatedAt, out.Exceptions[0].ID
				next, err := store.ListExceptions(ctx, tenant, filter)
				if err != nil || len(next.Exceptions) != 1 || next.Exceptions[0].ExamID == tc.want {
					t.Fatalf("scoped next page: %+v %v", next, err)
				}
			}
		})
	}
	t.Log("pool=1 real SQL passed school-without-exam-whitelist, inherited-school class isolation, explicit-exam grant, empty grants and scoped projected_at/cursors")
}

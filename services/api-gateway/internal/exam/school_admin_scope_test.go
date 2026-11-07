package exam

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func schoolAdminReadScope(t *testing.T, tenant, actor, own, foreignExam string) auth.AccessScope {
	t.Helper()
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: actor, TenantID: tenant, Roles: []string{"school_admin", "grader"}, DataScope: map[string]any{
		"school_admin": map[string]any{"scope": "school", "school_id": own}, "grader": map[string]any{"scope": "exam_task", "exam_id": foreignExam},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟保留了旧导航的会话缓存；仓储仍须按学校硬边界过滤，而不是信任考试白名单。
	scope.ExamIDs = []string{foreignExam}
	scope.ClassIDs = []string{"foreign-class"}
	return scope
}

func TestSchoolAdministratorMemoryExamReadsIgnoreForeignAssignments(t *testing.T) {
	store := NewMemoryStore()
	for _, school := range []string{"own", "foreign"} {
		store.items[school] = Exam{ID: school, TenantID: "tenant", SchoolID: school, ClassIDs: []string{"foreign-class"}}
	}
	scope := schoolAdminReadScope(t, "tenant", "actor", "own", "foreign")
	items, err := store.ListExams(t.Context(), scope, ListFilter{})
	if err != nil || len(items) != 1 || items[0].ID != "own" {
		t.Fatalf("list=%#v %v", items, err)
	}
	if _, err := store.GetExam(t.Context(), scope, "foreign"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign detail=%v", err)
	}
	if _, err := store.GetExam(t.Context(), scope, "own"); err != nil {
		t.Fatalf("own detail=%v", err)
	}
	scope.TenantWide = true
	items, err = store.ListExams(t.Context(), scope, ListFilter{})
	if err != nil || len(items) != 2 {
		t.Fatalf("tenant scope changed: %#v %v", items, err)
	}
}

func TestSchoolAdministratorPostgresExamReadsFilterBeforePagination(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL requires migrated isolated test database")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Database, "edugrade_ci_shared_") && !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("exam scope fixture requires isolated test database")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "exam_school_read_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
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
	// 随机 schema 只复制结构与索引，不复制用户记录；主键保留真实 GROUP BY 语义。
	for _, table := range []string{"exam", "exam_class", "exam_session"} {
		exec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING DEFAULTS INCLUDING CONSTRAINTS INCLUDING INDEXES)")
	}
	tenant, actor, own, foreign, session := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	ownOld, ownNew, foreignExam := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for i, item := range []struct{ id, school string }{{ownOld, own}, {foreignExam, foreign}, {ownNew, own}} {
		exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by,created_at,exam_session_id) VALUES($1,$2,$3,'Scope fixture','math','quiz',10,'grading','ai_assisted','manual_after_confirmation',$4,$5,$6)`, item.id, tenant, item.school, actor, time.Date(2026, 10, 1, 0, i, 0, 0, time.UTC), session)
	}
	store := NewPostgresStore(db)
	scope := schoolAdminReadScope(t, tenant, actor, own, foreignExam)
	first, err := store.ListExams(t.Context(), scope, ListFilter{Limit: 1})
	if err != nil || len(first) != 1 || first[0].ID != ownNew {
		t.Fatalf("first page=%#v %v", first, err)
	}
	second, err := store.ListExams(t.Context(), scope, ListFilter{Limit: 1, CursorID: first[0].ID, CursorAt: first[0].CreatedAt})
	if err != nil || len(second) != 1 || second[0].ID != ownOld {
		t.Fatalf("second page=%#v %v", second, err)
	}
	if _, err := store.GetExam(t.Context(), scope, foreignExam); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign detail=%v", err)
	}
	if _, err := store.GetExam(t.Context(), scope, ownNew); err != nil {
		t.Fatalf("own detail=%v", err)
	}
	items, err := store.ListExams(t.Context(), scope, ListFilter{SchoolID: foreign})
	if err != nil || len(items) != 0 {
		t.Fatalf("explicit foreign filter expanded access: %#v %v", items, err)
	}
	items, err = store.ListSessionExams(t.Context(), scope, session)
	if err != nil || len(items) != 2 {
		t.Fatalf("session subjects=%#v %v", items, err)
	}
	for _, item := range items {
		if item.ID == foreignExam {
			t.Fatal("session subject navigation leaked foreign exam")
		}
	}
	ordinary := auth.AccessScope{TenantID: tenant, ActorID: actor, AssignedOnly: true, ExamIDs: []string{foreignExam}}
	if _, err := store.GetExam(t.Context(), ordinary, foreignExam); err != nil {
		t.Fatalf("ordinary grader lost assigned exam: %v", err)
	}
	scope.TenantWide = true
	items, err = store.ListExams(t.Context(), scope, ListFilter{})
	if err != nil || len(items) != 3 {
		t.Fatalf("tenant administrator read scope changed: %#v %v", items, err)
	}
}

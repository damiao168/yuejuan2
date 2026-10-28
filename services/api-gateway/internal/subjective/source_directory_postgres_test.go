package subjective

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"os"
	"strings"
	"testing"
)

// 隔离随机schema只建立目录查询需要的列，验证真实PostgreSQL关联与游标，不宣称覆盖生产触发器或完整评分门禁。
func TestPostgresGradingSourceDirectoryScopeAndCursor(t *testing.T) {
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN required")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "grading_source_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		if _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(t.Context(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE question(id uuid,tenant_id uuid,exam_id uuid,question_no text,question_type text,deleted_at timestamptz);
CREATE TABLE exam(id uuid,tenant_id uuid,school_id uuid,deleted_at timestamptz);
CREATE TABLE submission(id uuid,tenant_id uuid,exam_id uuid,deleted_at timestamptz);
CREATE TABLE answer_segment(id uuid,tenant_id uuid,question_id uuid,submission_id uuid,status text,deleted_at timestamptz);
CREATE TABLE answer_segment_answer(tenant_id uuid,answer_segment_id uuid,deleted_at timestamptz);
CREATE TABLE question_rubric(tenant_id uuid,question_id uuid,deleted_at timestamptz);
CREATE TABLE exam_question_snapshot(tenant_id uuid,exam_id uuid,question_id uuid);`)
	tenant, exam, otherTenant, otherExam := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	school := uuid.NewString()
	exec(`INSERT INTO exam VALUES($1,$2,$3,NULL),($4,$2,$3,NULL),($1,$5,$3,NULL)`, exam, tenant, school, otherExam, otherTenant)
	for _, fixture := range []struct {
		tenant, exam, segment string
		ready                 bool
	}{
		{tenant, exam, "00000000-0000-0000-0000-000000000001", true},
		{tenant, exam, "00000000-0000-0000-0000-000000000002", false},
		{tenant, otherExam, "00000000-0000-0000-0000-000000000003", true},
		{otherTenant, exam, "00000000-0000-0000-0000-000000000004", true},
	} {
		q, sub := uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO question VALUES($1,$2,$3,'2','essay',NULL)`, q, fixture.tenant, fixture.exam)
		exec(`INSERT INTO submission VALUES($1,$2,$3,NULL)`, sub, fixture.tenant, fixture.exam)
		exec(`INSERT INTO answer_segment VALUES($1,$2,$3,$4,'ready',NULL)`, fixture.segment, fixture.tenant, q, sub)
		exec(`INSERT INTO question_rubric VALUES($1,$2,NULL)`, fixture.tenant, q)
		exec(`INSERT INTO exam_question_snapshot VALUES($1,$2,$3)`, fixture.tenant, fixture.exam, q)
		if fixture.ready {
			exec(`INSERT INTO answer_segment_answer VALUES($1,$2,NULL)`, fixture.tenant, fixture.segment)
		}
	}
	store := NewPostgresStore(db)
	items, err := store.ListGradingSources(t.Context(), tenant, exam, GradingSourceFilter{Limit: 1, TenantWide: true})
	if err != nil || len(items) != 1 || !items[0].Ready {
		t.Fatalf("items=%+v error=%v", items, err)
	}
	next, err := store.ListGradingSources(t.Context(), tenant, exam, GradingSourceFilter{Limit: 100, Cursor: items[0].AnswerSegmentID, TenantWide: true})
	if err != nil || len(next) != 1 || next[0].Ready || next[0].UnavailableReason != "尚无已登记作答" {
		t.Fatalf("next=%+v error=%v", next, err)
	}
	blocked, err := store.ListGradingSources(t.Context(), tenant, exam, GradingSourceFilter{Limit: 100, SchoolIDs: []string{uuid.NewString()}})
	if err != nil || len(blocked) != 0 {
		t.Fatalf("unmanaged school sources=%+v error=%v", blocked, err)
	}
}

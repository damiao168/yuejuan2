package report

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 仅在本轮自有 PostgreSQL 上创建唯一 schema，合成表覆盖真实报告查询，不能替代全迁移测试。
func reportVisibilityPG(t *testing.T) (*sql.DB, *sql.DB) {
	t.Helper()
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
	schema := "review_report_scope_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(1)
	mutator := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		mutator.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
		admin.Close()
	})
	return db, mutator
}

func TestPostgresStudentReportVisibilityAndPinnedCurrentRelease(t *testing.T) {
	db, mutator := reportVisibilityPG(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, ddl := range []string{
		`CREATE TABLE exam(tenant_id uuid,id uuid,exam_session_id uuid,name text,subject text,exam_type text,deleted_at timestamptz)`,
		`CREATE TABLE exam_candidate_snapshot(tenant_id uuid,exam_id uuid,student_id uuid)`,
		`CREATE TABLE score_release_current(tenant_id uuid,exam_id uuid,release_id uuid)`,
		`CREATE TABLE score_release(tenant_id uuid,id uuid,exam_id uuid,status text,visibility_policy jsonb,published_at timestamptz,version integer DEFAULT 1,source text DEFAULT 'initial',reason text DEFAULT 'fixture',idempotency_key text DEFAULT 'fixture',appeal_window jsonb DEFAULT '{}',gate_snapshot jsonb DEFAULT '{}',source_release_id uuid,supersedes_release_id uuid,created_by uuid,created_at timestamptz DEFAULT clock_timestamp(),published_by uuid)`,
		`CREATE TABLE score_release_item(tenant_id uuid,release_id uuid,student_id uuid,submission_id uuid,total_score numeric,max_score numeric,status text DEFAULT 'confirmed',snapshot_hash text DEFAULT 'fixture')`,
		`CREATE TABLE score_release_question(tenant_id uuid,id uuid,release_id uuid,submission_id uuid,question_id uuid,question_no text,final_grade_id uuid,score numeric,max_score numeric,source_type text,source_id uuid,student_explanation jsonb)`,
		`CREATE TABLE submission(tenant_id uuid,id uuid,candidate_no text,deleted_at timestamptz)`,
		`CREATE TABLE student(tenant_id uuid,id uuid,class_id uuid,deleted_at timestamptz)`,
		`CREATE TABLE school_class(tenant_id uuid,id uuid,name text,deleted_at timestamptz)`,
		`CREATE TABLE final_grade(tenant_id uuid,id uuid,answer_segment_id uuid,deleted_at timestamptz,submission_id uuid,question_id uuid,question_no text,score numeric,max_score numeric,source text,exam_id uuid,status text,created_at timestamptz)`,
		`CREATE TABLE question(tenant_id uuid,id uuid,question_type text,knowledge_points jsonb,deleted_at timestamptz)`,
		`CREATE TABLE answer_segment_answer(tenant_id uuid,answer_segment_id uuid,answer_payload jsonb,created_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE ai_grade(tenant_id uuid,id uuid,answer_segment_id uuid,suggested_score numeric,status text,created_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE human_grade(tenant_id uuid,id uuid,score numeric,answer_segment_id uuid,created_at timestamptz,deleted_at timestamptz)`,
		`CREATE TABLE submission_grade(tenant_id uuid,exam_id uuid,submission_id uuid,student_id uuid,anonymous_code text,total_score numeric,max_score numeric,status text,locked boolean,published_at timestamptz,deleted_at timestamptz)`,
	} {
		if _, err := db.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	tenant, exam, student, submission, question, final, segment := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	first, second := uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO score_release_current VALUES($1,$2,$3)`, tenant, exam, first)
	exec(`INSERT INTO exam VALUES($1,$2,NULL,'fixture','mathematics','unit',NULL)`, tenant, exam)
	exec(`INSERT INTO submission VALUES($1,$2,'anon',NULL)`, tenant, submission)
	exec(`INSERT INTO student VALUES($1,$2,NULL,NULL)`, tenant, student)
	exec(`INSERT INTO question VALUES($1,$2,'short_answer','["private-knowledge"]',NULL)`, tenant, question)
	exec(`INSERT INTO final_grade(tenant_id,id,answer_segment_id,submission_id,question_id,question_no,score,max_score,source,exam_id,status,created_at) VALUES($1,$2,$3,$4,$5,'Q1',4,5,'single_review',$6,'published',clock_timestamp())`, tenant, final, segment, submission, question, exam)
	for i, release := range []string{first, second} {
		visible := i == 1
		policy, _ := json.Marshal(map[string]bool{"show_question_scores": visible, "show_knowledge_analysis": visible, "show_feedback": visible})
		exec(`INSERT INTO score_release(tenant_id,id,exam_id,status,visibility_policy,published_at,created_by,published_by) VALUES($1,$2,$3,'published',$4,clock_timestamp(),$1,$1)`, tenant, release, exam, policy)
		exec(`INSERT INTO score_release_item(tenant_id,release_id,student_id,submission_id,total_score,max_score) VALUES($1,$2,$3,$4,$5,5)`, tenant, release, student, submission, 4-i)
		exec(`INSERT INTO score_release_question VALUES($1,$2,$3,$4,$5,'Q1',$6,$7,5,'single_review',NULL,'{"feedback":"published feedback"}')`, tenant, uuid.NewString(), release, submission, question, final, 4-i)
	}
	store := NewPostgresStore(db)
	studentCtx := auth.WithUser(ctx, auth.User{ID: student, TenantID: tenant, Permissions: []string{"student:report:read"}, DataScope: map[string]any{"student_id": student}})
	request := func(callCtx context.Context) StudentReport {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "/api/v1/students/"+student+"/reports/"+exam, nil)
		r.SetPathValue("studentId", student)
		r.SetPathValue("examId", exam)
		w := httptest.NewRecorder()
		NewHandler(store, nil).StudentReport(w, r.WithContext(callCtx))
		var body struct {
			Report StudentReport `json:"report"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("report HTTP=%d %s", w.Code, w.Body.String())
		}
		return body.Report
	}
	hidden := request(studentCtx)
	newResult, err := scorerelease.NewPostgresStore(db, nil).StudentResult(studentCtx, tenant, exam, student)
	if err != nil || newResult.Questions != nil || newResult.TotalScore != hidden.TotalScore {
		t.Fatalf("new result and legacy report disagree on the hidden release: %+v %+v %v", newResult, hidden, err)
	}
	if hidden.TotalScore != 4 || len(hidden.Questions) != 0 || len(hidden.KnowledgeMastery) != 0 || len(hidden.TeacherFeedback) != 0 {
		t.Fatalf("hidden release leaked: %+v", hidden)
	}
	management := request(auth.WithUser(ctx, auth.User{ID: "admin", TenantID: tenant, Permissions: []string{"report:read"}}))
	if len(management.Questions) != 1 || len(management.KnowledgeMastery) != 1 || len(management.TeacherFeedback) != 1 {
		t.Fatalf("management lost authorized detail: %+v", management)
	}
	// 在首次解析 current 指针后提交另一个发布；真实后续 SQL 必须继续使用 first。
	queryer := &releaseSwitchQueryer{DB: db, switchCurrent: func() {
		if _, err := mutator.ExecContext(ctx, `UPDATE score_release_current SET release_id=$1 WHERE tenant_id=$2 AND exam_id=$3`, second, tenant, exam); err != nil {
			t.Fatal(err)
		}
	}}
	pinned, err := store.loadDataset(studentCtx, queryer, tenant, exam)
	if err != nil {
		t.Fatal(err)
	}
	oldReport := buildStudentReport(pinned, student)
	if !queryer.switched || oldReport.TotalScore != 4 || len(oldReport.Questions) != 0 || len(pinned.Grades) != 1 || pinned.Grades[0].Score != 4 {
		t.Fatalf("mixed two releases: %+v switched=%v", oldReport, queryer.switched)
	}
	visible := request(studentCtx)
	if visible.TotalScore != 3 || len(visible.Questions) != 1 || visible.Questions[0].Score != 3 || len(visible.KnowledgeMastery) != 1 || len(visible.TeacherFeedback) != 1 {
		t.Fatalf("new release policy: %+v", visible)
	}
	// 缺字段默认关闭；坏 current 指针必须拒绝，不能重用 legacy 分数或默认 true。
	exec(`UPDATE score_release SET visibility_policy='{}' WHERE id=$1`, second)
	if out := request(studentCtx); len(out.Questions) != 0 || len(out.KnowledgeMastery) != 0 || len(out.TeacherFeedback) != 0 {
		t.Fatalf("missing visibility keys opened details: %+v", out)
	}
	exec(`UPDATE score_release SET status='draft' WHERE id=$1`, second)
	if _, err := store.StudentReport(studentCtx, tenant, exam, student); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpublished current pointer: %v", err)
	}
	exec(`DELETE FROM score_release WHERE id=$1`, second)
	if _, err := store.StudentReport(studentCtx, tenant, exam, student); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing current release: %v", err)
	}
	// 没有 current 指针的旧路径才兼容已发布且锁定的 submission_grade。
	exec(`DELETE FROM score_release_current`)
	exec(`INSERT INTO submission_grade VALUES($1,$2,$3,$4,'legacy',4,5,'published',true,clock_timestamp(),NULL)`, tenant, exam, submission, student)
	legacy := request(studentCtx)
	if legacy.TotalScore != 4 || len(legacy.Questions) != 1 || len(legacy.KnowledgeMastery) != 1 || len(legacy.TeacherFeedback) != 0 {
		t.Fatalf("legacy published compatibility: %+v", legacy)
	}
	t.Logf("pool=1 hidden student, management detail, pinned old release=4, next release=3, missing keys/current denial and locked legacy compatibility passed")
}

type releaseSwitchQueryer struct {
	*sql.DB
	switchCurrent func()
	switched      bool
}

func (q *releaseSwitchQueryer) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := q.DB.QueryRowContext(ctx, query, args...)
	if !q.switched && strings.Contains(query, "FROM score_release_current") {
		q.switched = true
		q.switchCurrent()
	}
	return row
}

var _ reportQueryer = (*releaseSwitchQueryer)(nil)

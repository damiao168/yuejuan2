package scorerelease

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 在随机库应用正式迁移，验证发布约束、冻结名册查询及新旧版本。绝不向传入DSN的业务库写夹具。
func formalExportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for formal export PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	name := pgx.Identifier{"formal_export_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err = admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.Database = strings.Trim(name, `"`)
	database := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = database.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
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
		if _, err = database.ExecContext(t.Context(), string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	return database
}

func TestPostgresFormalExportPinsPublishedVersionsAndFrozenRoster(t *testing.T) {
	database := formalExportTestDB(t)
	ctx := t.Context()
	var tenant, actor string
	if err := database.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id AND u.username='teacher' WHERE t.code='demo'`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	school, class, grade, year, cohort := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'正式导出测试学校',$1,'active')`, school, tenant)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1::uuid,$2::uuid,$3::uuid,'2026学年',2026,2027,'2026-09-01','2027-08-31')`, year, tenant, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'senior',2026,2029,'2026级')`, cohort, tenant, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'高一','active',$4::uuid,$5::uuid)`, grade, tenant, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'当前班级',$1,'active',$5::uuid,$6::uuid)`, class, tenant, school, grade, year, cohort)
	session := uuid.NewString()
	student := uuid.NewString()
	exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'00123','正式导出测试学生','active')`, student, tenant, school, class)
	exec(`INSERT INTO exam_session(id,tenant_id,school_id,grade_id,name,exam_type,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'正式导出整场考试','midterm_exam','grading','human_review_required','manual_after_confirmation',$5::uuid)`, session, tenant, school, grade, actor)
	exams := []string{uuid.NewString(), uuid.NewString()}
	submissions := []string{uuid.NewString(), uuid.NewString()}
	for i, subject := range []string{"mathematics", "english"} {
		exec(`INSERT INTO exam(id,tenant_id,school_id,exam_session_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'单科考试',$5,'midterm_exam',100,'grading','human_review_required','manual_after_confirmation',$6::uuid)`, exams[i], tenant, school, session, subject, actor)
		exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,'00123','考试时姓名',$4::uuid,'考试时年级',$5::uuid,'考试时班级')`, tenant, exams[i], student, grade, class)
		exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,1,'passed',$5::uuid)`, submissions[i], tenant, exams[i], student, actor)
		exec(`INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$3,98,100,'pending_confirmation',$5::uuid)`, tenant, exams[i], submissions[i], student, actor)
	}
	publish := func(index, version int, score float64) string {
		t.Helper()
		release := uuid.NewString()
		exec(`INSERT INTO score_release(id,tenant_id,exam_id,version,source,reason,idempotency_key,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4,'initial','synthetic verified release',$1,$5::uuid)`, release, tenant, exams[index], version, actor)
		exec(`INSERT INTO score_release_item(tenant_id,release_id,student_id,submission_id,total_score,max_score,status,snapshot_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,100,'confirmed',repeat('a',64))`, tenant, release, student, submissions[index], score)
		exec(`UPDATE score_release SET status='published',published_by=$2::uuid,published_at=now() WHERE id=$1::uuid`, release, actor)
		exec(`INSERT INTO score_release_current(tenant_id,exam_id,release_id) VALUES($1::uuid,$2::uuid,$3::uuid) ON CONFLICT(tenant_id,exam_id) DO UPDATE SET release_id=EXCLUDED.release_id`, tenant, exams[index], release)
		return release
	}
	mathV1 := publish(0, 1, 95)
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: actor, TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(database, nil)
	if _, err = store.ExportCurrentPublishedCSV(ctx, scope, exams[0], time.Now()); !errors.Is(err, errFormalExportIncomplete) {
		t.Fatalf("unpublished English accepted: %v", err)
	}
	publish(1, 1, 0)
	now := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	first, err := store.ExportCurrentPublishedCSV(ctx, scope, exams[0], now)
	if err != nil {
		t.Fatal(err)
	}
	row := formalTestRows(t, first.Content)[0]
	if row["数学·成绩"] != "95.00" || row["英语·成绩"] != "0.00" || row["总分"] != "95.00" || row["数学·发布版本ID"] != mathV1 || row["姓名"] != "考试时姓名" || row["考试班级"] != "考试时班级" || row["学号"] != "00123" {
		t.Fatalf("pending score/live identity entered formal export: %#v", row)
	}
	// Excel沿用同一组发布快照和冻结班级；底部均分不得混入待确认98分，也不能丢掉英语零分。
	sheets, err := teacherScoreSheets(first.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(sheets) != 1 || !strings.Contains(sheets[0].Subtitle, "考试时班级") || sheets[0].Rows[0][0] != "00123" {
		t.Fatalf("teacher sheet lost frozen identity: %+v", sheets)
	}
	footer := sheets[0].Rows[len(sheets[0].Rows)-1]
	if footer[0] != "班级平均分" || footer[2] != float64(95) || footer[3] != float64(0) || footer[4] != float64(95) {
		t.Fatalf("teacher mean not from published scores: %v", footer)
	}
	narrow := auth.AccessScope{TenantID: tenant, ActorID: actor, SchoolIDs: []string{school}, ExamIDs: []string{exams[0]}}
	if _, err = store.ExportCurrentPublishedCSV(ctx, narrow, exams[0], now); !errors.Is(err, ErrForbidden) {
		t.Fatalf("assignment-only scope expanded to all subjects: %v", err)
	}
	mathV2 := publish(0, 2, 98)
	second, err := store.ExportCurrentPublishedCSV(ctx, scope, exams[0], now)
	if err != nil {
		t.Fatal(err)
	}
	if current := formalTestRows(t, second.Content)[0]; current["数学·发布版本ID"] != mathV2 || current["数学·成绩"] != "98.00" {
		t.Fatalf("new publication not selected: %#v", current)
	}
	var old float64
	if err = database.QueryRowContext(ctx, `SELECT total_score::float8 FROM score_release_item WHERE tenant_id=$1::uuid AND release_id=$2::uuid`, tenant, mathV1).Scan(&old); err != nil || old != 95 {
		t.Fatalf("old release lost: %v %v", old, err)
	}
	if original := formalTestRows(t, first.Content)[0]; original["数学·成绩"] != "95.00" || original["数学·发布版本ID"] != mathV1 {
		t.Fatal("previous export changed after publication")
	}
}

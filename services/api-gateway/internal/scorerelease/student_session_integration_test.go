package scorerelease

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 此专项执行真实 PostgreSQL 投影查询，最小表结构只覆盖该查询的列和作用域。
// 完整历史迁移、RLS 与 HTTP 发布流程由 server 集成测试负责，不能用本测试代替。
func TestStudentSessionProjectionWithPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for PostgreSQL session projection")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	name := pgx.Identifier{"edugrade_student_ux_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		cleanup, err := pgx.ConnectConfig(cleanupCtx, config)
		if err != nil {
			t.Error(err)
			return
		}
		defer cleanup.Close(cleanupCtx)
		if _, err := cleanup.Exec(cleanupCtx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	})
	testConfig := config.Copy()
	testConfig.Database = strings.Trim(name, `"`)
	testConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	db := stdlib.OpenDB(*testConfig)
	defer db.Close()
	fixture := seedStudentSessionProjection(t, ctx, db)
	assertStudentSessionProjection(t, ctx, db, fixture)
}

type studentSessionProjectionFixture struct {
	tenant, student, foreign string
	exams, releases          map[string]string
}

func createStudentSessionProjectionTables(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE exam(id uuid,tenant_id uuid,exam_session_id uuid,subject text,name text,deleted_at timestamptz)`,
		`CREATE TABLE exam_candidate_snapshot(tenant_id uuid,exam_id uuid,student_id uuid)`,
		`CREATE TABLE score_release(id uuid,tenant_id uuid,status text,published_at timestamptz,visibility_policy jsonb)`,
		`CREATE TABLE score_release_current(tenant_id uuid,exam_id uuid,release_id uuid)`,
		`CREATE TABLE score_release_item(tenant_id uuid,release_id uuid,student_id uuid,total_score numeric,max_score numeric)`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

// 同时准备未发布、统计隐藏、小样本、其他学生和其他租户；最后切换当前发布指针。
func seedStudentSessionProjection(t *testing.T, ctx context.Context, db *sql.DB) studentSessionProjectionFixture {
	t.Helper()
	createStudentSessionProjectionTables(t, ctx, db)
	tenant, student, session := uuid.NewString(), uuid.NewString(), uuid.NewString()
	foreign := uuid.NewString()
	exams := map[string]string{}
	releases := map[string]string{}
	fixture := studentSessionProjectionFixture{tenant: tenant, student: student, foreign: foreign, exams: exams, releases: releases}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, subject := range []string{"math", "english", "chinese", "physics", "other_student", "foreign_tenant"} {
		seedStudentSessionProjectionSubject(t, exec, subject, fixture, session)
	}
	// 主科当前指针指向更高分；传入的详情版本必须仍投影原冻结零分。
	later := uuid.NewString()
	exec(`INSERT INTO score_release VALUES($1,$2,'published',now(),'{"show_cohort_statistics":true}')`, later, tenant)
	exec(`INSERT INTO score_release_item VALUES($1,$2,$3,99,100)`, tenant, later, student)
	exec(`UPDATE score_release_current SET release_id=$1 WHERE tenant_id=$2 AND exam_id=$3`, later, tenant, exams["math"])
	return fixture
}

func seedStudentSessionProjectionSubject(t *testing.T, exec func(string, ...any), subject string, fixture studentSessionProjectionFixture, session string) {
	t.Helper()
	examID, releaseID := uuid.NewString(), uuid.NewString()
	fixture.exams[subject], fixture.releases[subject] = examID, releaseID
	scope := fixture.tenant
	if subject == "foreign_tenant" {
		scope = fixture.foreign
	}
	exec(`INSERT INTO exam VALUES($1,$2,$3,$4,$4,NULL)`, examID, scope, session, subject)
	candidate := fixture.student
	if subject == "other_student" {
		candidate = uuid.NewString()
	}
	exec(`INSERT INTO exam_candidate_snapshot VALUES($1,$2,$3)`, scope, examID, candidate)
	if subject == "physics" {
		return
	}
	visible := subject != "chinese"
	exec(`INSERT INTO score_release VALUES($1,$2,'published',now(),jsonb_build_object('show_cohort_statistics',$3::boolean))`, releaseID, scope, visible)
	exec(`INSERT INTO score_release_current VALUES($1,$2,$3)`, scope, examID, releaseID)
	score := 80
	if subject == "math" {
		score = 0
	}
	exec(`INSERT INTO score_release_item VALUES($1,$2,$3,$4,100)`, scope, releaseID, candidate, score)
	count := 10
	if subject == "english" {
		count = 9
	}
	for i := 1; i < count; i++ {
		exec(`INSERT INTO score_release_item VALUES($1,$2,$3,50,100)`, scope, releaseID, uuid.NewString())
	}
}

// 明确传入原冻结版本，零分、统计隐私及租户边界都必须来自该版本的事实。
func assertStudentSessionProjection(t *testing.T, ctx context.Context, db *sql.DB, fixture studentSessionProjectionFixture) {
	t.Helper()
	result, total, max, err := NewPostgresStore(db, nil).studentSessionSummary(ctx, fixture.tenant, fixture.exams["math"], fixture.student, fixture.releases["math"])
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpectedCount != 4 || result.PublishedCount != 3 || total != 160 || max != 300 {
		t.Fatalf("scope/partial total: %+v %v/%v", result, total, max)
	}
	bySubject := map[string]StudentSessionSubject{}
	for _, item := range result.Subjects {
		bySubject[item.Subject] = item
	}
	math, small, private, unpublished := bySubject["math"], bySubject["english"], bySubject["chinese"], bySubject["physics"]
	if math.Score == nil || *math.Score != 0 || !math.StatisticsAvailable || math.SampleSize != 10 {
		t.Fatalf("frozen zero: %+v", math)
	}
	if small.StatisticsReason != "small_cohort" || small.MeanScoreRate != nil || small.SampleSize != 0 {
		t.Fatalf("small cohort leak: %+v", small)
	}
	if private.StatisticsReason != "not_public" || private.MeanScoreRate != nil {
		t.Fatalf("private mean leak: %+v", private)
	}
	if unpublished.Status != "unpublished" || unpublished.Score != nil || unpublished.MeanScoreRate != nil {
		t.Fatalf("unpublished leak: %+v", unpublished)
	}
	outside, _, _, err := NewPostgresStore(db, nil).studentSessionSummary(ctx, fixture.foreign, fixture.exams["math"], fixture.student, fixture.releases["math"])
	if err != nil || outside.ExpectedCount != 0 {
		t.Fatalf("foreign tenant response: %+v %v", outside, err)
	}
}

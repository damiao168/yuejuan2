package score

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 每次在随机数据库应用正式迁移；夹具和清理均不修改传入 DSN 对应的业务库。
func confirmationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for isolated score PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	name := pgx.Identifier{"score_confirmation_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.Database = strings.Trim(name, `"`)
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
		// 删除完整迁移库会等待 PostgreSQL 的文件落盘；Windows 慢盘上不能用短超时误报业务回归。
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
		if _, err := db.ExecContext(t.Context(), string(raw)); err != nil {
			t.Fatalf("migration %s: %v", filepath.Base(path), err)
		}
	}
	return db
}

func TestPostgresConfirmationWaitsForCompleteRosterThenAllowsLateSubmission(t *testing.T) {
	db := confirmationTestDB(t)
	ctx := t.Context()
	var tenant, actor string
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='teacher'`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	exam, school, grade, class, profile, question := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	year, cohort := uuid.NewString(), uuid.NewString()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic confirmation school',$1,'active')`, school, tenant)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic year',2026,2027,'2026-09-01','2027-08-31')`, year, tenant, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'junior',2026,2029,'Synthetic cohort')`, cohort, tenant, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic grade','active',$4::uuid,$5::uuid)`, grade, tenant, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Synthetic class',$1,'active',$5::uuid,$6::uuid)`, class, tenant, school, grade, year, cohort)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic confirmation','physics','quiz',10,'grading','human_review_required','manual_after_confirmation',$4::uuid)`, exam, tenant, school, actor)
	exec(`INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,$1,'junior','physics',1)`, profile, tenant)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','calculation',10,1,'active')`, question, tenant, exam)
	exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'structured_steps','[]','R1','{}','{}','{}','{}',repeat('a',64))`, tenant, exam, question, profile)
	students := []string{uuid.NewString(), uuid.NewString()}
	for _, student := range students {
		exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$1,'Synthetic student','active')`, student, tenant, school, class)
		exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,$3,'Synthetic student',$4::uuid,'Synthetic grade',$5::uuid,'Synthetic class')`, tenant, exam, student, grade, class)
	}
	addSubmission := func(student string, pages int) string {
		t.Helper()
		submission := uuid.NewString()
		exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,$5,'passed',$6::uuid)`, submission, tenant, exam, student, pages, actor)
		return submission
	}
	addFinal := func(submission string) {
		t.Helper()
		file, page, segment := uuid.NewString(), uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, tenant, exam, actor)
		exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, tenant, submission, file)
		exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{}','manual','accepted')`, segment, tenant, submission, page, question)
		exec(`INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1',$4::uuid,$5::uuid,$5,8,10,'single_review','pending_confirmation',$6::uuid)`, tenant, exam, question, segment, submission, actor)
	}
	store, _, releases := newCompatibilityPostgresStore(db)
	first := addSubmission(students[0], 1)
	addFinal(first)
	finalize := func() {
		t.Helper()
		if _, err := store.FinalizeExam(ctx, tenant, exam, actor); err != nil {
			t.Fatal(err)
		}
	}
	assertBlocked := func(codes ...string) {
		t.Helper()
		quality, err := store.CheckQuality(ctx, tenant, exam, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range codes {
			if !hasIssue(quality.Issues, code) {
				t.Fatalf("confirmation is missing issue %s: %#v", code, quality)
			}
		}
		if _, err := store.ConfirmGrades(ctx, tenant, exam, actor, ConfirmInput{Reason: "synthetic"}); !errors.Is(err, ErrQualityGateFailed) {
			t.Fatalf("incomplete roster allowed confirmation: %v", err)
		}
		var changed int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission_grade WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND (status<>'pending_confirmation' OR confirmed_at IS NOT NULL)`, tenant, exam).Scan(&changed); err != nil || changed != 0 {
			t.Fatalf("rejected confirmation mutated existing grades: changed=%d err=%v", changed, err)
		}
	}
	assertPublicationBlocked := func() {
		t.Helper()
		if _, err := store.PublishGrades(ctx, tenant, exam, actor, PublishInput{Reason: "incomplete roster"}); !errors.Is(err, ErrQualityGateFailed) {
			t.Fatalf("compatibility publication bypassed incomplete roster: %v", err)
		}
	}
	// 先只有 A 的终评和总分；未结束导入时，B 尚无答卷不能被静默作为缺考发布。
	finalize()
	quality, err := store.CheckQuality(ctx, tenant, exam, false)
	if err != nil || !hasIssue(quality.Issues, "capture_collection_incomplete") {
		t.Fatalf("open collection must block silent absence: %#v %v", quality, err)
	}
	second := addSubmission(students[1], 0)
	assertBlocked("missing_pages_unresolved")
	assertPublicationBlocked()
	exec(`UPDATE submission SET actual_page_count=1 WHERE id=$1::uuid`, second)
	assertBlocked("missing_submission_grades", "question_coverage_incomplete")
	assertPublicationBlocked()
	addFinal(second)
	finalize()
	quality, err = store.CheckQuality(ctx, tenant, exam, false)
	if err != nil || !quality.Passed {
		t.Fatalf("complete pending grades must pass confirmation quality: %#v %v", quality, err)
	}
	quality, err = store.CheckQuality(ctx, tenant, exam, true)
	if err != nil || !quality.Passed {
		t.Fatalf("automatic-preparation preview should be ready: %#v %v", quality, err)
	}
	var publicVersions int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenant, exam).Scan(&publicVersions); err != nil || publicVersions != 0 {
		t.Fatalf("read-only preparation preview published grades: current=%d err=%v", publicVersions, err)
	}
	// 已有所有终分仍不代表总分是最新的；旧流程确认前必须拒绝过期汇总。
	exec(`UPDATE submission_grade SET total_score=total_score-1 WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenant, exam)
	assertBlocked("score_integrity_mismatch")
	finalize()
	assertAuditGradeRLSAndFileRetention(t, db, tenant, exam)
	confirmed, err := store.ConfirmGrades(ctx, tenant, exam, actor, ConfirmInput{Reason: "synthetic complete"})
	if err != nil || len(confirmed) != 2 {
		t.Fatalf("late submission prevented complete confirmation: %#v %v", confirmed, err)
	}
	published, err := store.PublishGrades(ctx, tenant, exam, actor, PublishInput{Reason: "synthetic complete"})
	if err != nil || published.Status != "published" || len(published.SubmissionGrades) != 2 || published.ScoreRelease == nil {
		t.Fatalf("complete confirmed grades failed publication: %#v %v", published, err)
	}
	current, err := releases.CurrentPublished(ctx, tenant, exam)
	if err != nil || current.Release.ID != published.ScoreRelease.ID || len(current.Items) != 2 || len(current.Questions) != 2 || current.Items[0].TotalScore != 8 || current.Items[1].TotalScore != 8 {
		t.Fatalf("late submission missing from immutable publication: %+v %v", current, err)
	}
	assertPublishedRegradeContinuity(t, db, tenant, exam, actor, question, []string{first, second})
}

package exam

import (
	"context"
	"database/sql"
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

// 在调用者指定的测试 PostgreSQL 中创建随机库并应用正式迁移；从不改写 DSN 所指数据库。
func managementGradingTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for isolated grading-progress PostgreSQL test")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.Database = "postgres"
	admin := stdlib.OpenDB(*config)
	name := pgx.Identifier{"management_grading_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	config.Database = strings.Trim(name, `"`)
	db := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		_ = db.Close()
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

func TestManagementGradingCompleteWithPostgresRequiresActualCoverageAndTasks(t *testing.T) {
	db := managementGradingTestDB(t)
	ctx := t.Context()
	var tenant, actor string
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id WHERE t.code='demo' AND u.username='teacher'`).Scan(&tenant, &actor); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	school, grade, class, year, cohort := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	examID, profile, question, student := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	submission, file, page, segment, task := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic progress school',$1::text,'active')`, school, tenant)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1,$2,$3,'Synthetic year',2026,2027,'2026-09-01','2027-08-31')`, year, tenant, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1,$2,$3,'junior',2026,2029,'Synthetic cohort')`, cohort, tenant, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1,$2,$3,'Synthetic grade','active',$4,$5)`, grade, tenant, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Synthetic class',$1::text,'active',$5::uuid,$6::uuid)`, class, tenant, school, grade, year, cohort)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic progress','physics','quiz',10,'draft','human_review_required','manual_after_confirmation',$4)`, examID, tenant, school, actor)
	exec(`INSERT INTO exam_class(tenant_id,exam_id,class_id) VALUES($1,$2,$3)`, tenant, examID, class)
	exec(`INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,$1::text,'junior','physics',1)`, profile, tenant)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'Q1','calculation',10,1,'active')`, question, tenant, examID)
	exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,1,'structured_steps','[]','R1','{}','{}','{}','{}',repeat('a',64))`, tenant, examID, question, profile)
	exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1,$2,$3,$4,'0001','Synthetic student','active')`, student, tenant, school, class)
	exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1,$2,$3,'0001','Synthetic student',$4,'Synthetic grade',$5,'Synthetic class')`, tenant, examID, student, grade, class)
	// 名册只在准备阶段可写；完成合成准备后再切阶段，不能在夹具里绕开正式冻结触发器。
	exec(`UPDATE exam SET status='finalized' WHERE id=$1`, examID)
	store := NewPostgresStore(db)
	scope := auth.AccessScope{TenantID: tenant, TenantWide: true}
	assertComplete := func(label string, complete bool) {
		t.Helper()
		item, err := store.GetManagementExam(ctx, scope, "standalone", examID)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if (item.Progress.GradingCompleteCount == 1) != complete {
			t.Fatalf("%s progress=%+v, want completion=%t", label, item.Progress, complete)
		}
		items, err := store.ListManagementExams(ctx, scope, ManagementListFilter{Status: "completed", Limit: 1})
		if err != nil || (len(items) == 1) != complete {
			t.Fatalf("%s filter result=%+v err=%v", label, items, err)
		}
	}
	assertComplete("manual finalized with no received paper", false)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1,$2,$3,$4,'0001','manual_import','quality_checked',1,1,'passed',$5)`, submission, tenant, examID, student, actor)
	exec(`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1::text,'tenant',$4::uuid)`, file, tenant, examID, actor)
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2,$3,$4,1,'accepted')`, page, tenant, submission, file)
	exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$4,$5,'Q1','{}','manual','accepted')`, segment, tenant, submission, page, question)
	assertComplete("paper with missing final grade", false)
	exec(`INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by) VALUES($1,$2,$3,'Q1',$4,$5,'0001',8,10,'single_review','pending_confirmation',$6)`, tenant, examID, question, segment, submission, actor)
	assertComplete("final exists but total is absent", false)
	exec(`INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1,$2,$3,$4,'0001',8,10,'pending_confirmation',$5)`, tenant, examID, submission, student, actor)
	assertComplete("all scoring complete while waiting for confirmation", true)
	exec(`INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1,$2,$3,$4,'Q1',$5,$6,'0001','manual_sample','in_progress',$7)`, task, tenant, examID, question, segment, submission, actor)
	assertComplete("live review remains after manual phase change", false)
	exec(`UPDATE review_task SET status='cancelled' WHERE id=$1`, task)
	assertComplete("cancelled review is historical", true)
	exec(`UPDATE submission_grade SET total_score=7 WHERE submission_id=$1`, submission)
	assertComplete("stale aggregate differs from latest final", false)
	exec(`UPDATE submission_grade SET total_score=8 WHERE submission_id=$1`, submission)
	exec(`UPDATE submission SET identity_status='unknown' WHERE id=$1`, submission)
	assertComplete("identity became unresolved", false)
	exec(`UPDATE submission SET identity_status='unassigned',actual_page_count=0 WHERE id=$1`, submission)
	assertComplete("received paper is missing pages", false)
	exec(`UPDATE submission SET actual_page_count=1 WHERE id=$1`, submission)
	assertComplete("complete again", true)
}

package score

import (
	"encoding/csv"
	"errors"
	"strconv"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"github.com/google/uuid"
)

func TestPostgresWholeExamReportUsesFrozenRosterAndRejectsPartialScope(t *testing.T) {
	db := confirmationTestDB(t)
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
	school, grade, class, year, cohort, session := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'报表测试学校',$1,'active')`, school, tenant)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1::uuid,$2::uuid,$3::uuid,'2026学年',2026,2027,'2026-09-01','2027-08-31')`, year, tenant, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'senior',2026,2029,'2026级')`, cohort, tenant, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'高一','active',$4::uuid,$5::uuid)`, grade, tenant, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'原考试班级',$1,'active',$5::uuid,$6::uuid)`, class, tenant, school, grade, year, cohort)
	exec(`INSERT INTO exam_session(id,tenant_id,school_id,grade_id,name,exam_type,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'期中考试','midterm_exam','grading','human_review_required','manual_after_confirmation',$5::uuid)`, session, tenant, school, grade, actor)
	students := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for i, student := range students {
		exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,'active')`, student, tenant, school, class, "00"+strconv.Itoa(i+1), "学生"+strconv.Itoa(i+1))
	}
	exams := []string{uuid.NewString(), uuid.NewString()}
	var staleGrade string
	for subjectIndex, code := range []string{"mathematics", "english"} {
		examID, question, profile := exams[subjectIndex], uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO exam(id,tenant_id,school_id,exam_session_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'单科名称',$5,'midterm_exam',10,'grading','human_review_required','manual_after_confirmation',$6::uuid)`, examID, tenant, school, session, code, actor)
		exec(`INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,$1,'senior',$3,1)`, profile, tenant, code)
		exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','calculation',10,1,'active')`, question, tenant, examID)
		exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'structured_steps','[]','R1','{}','{}','{}','{}',repeat('a',64))`, tenant, examID, question, profile)
		for i, student := range students {
			exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6::uuid,'高一',$7::uuid,'冻结考试班级')`, tenant, examID, student, "00"+strconv.Itoa(i+1), "学生"+strconv.Itoa(i+1), grade, class)
			if subjectIndex == 1 && i == 2 {
				continue
			}
			submission, file, page, segment := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			score := []float64{8, 8, 6, 5}[i]
			if subjectIndex == 1 {
				score = []float64{7, 7, 0, 6}[i]
			}
			exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,1,'passed',$5::uuid)`, submission, tenant, examID, student, actor)
			exec(`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, tenant, examID, actor)
			exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, tenant, submission, file)
			exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{}','manual','accepted')`, segment, tenant, submission, page, question)
			exec(`INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1',$4::uuid,$5::uuid,$5,$6,10,'single_review','pending_confirmation',$7::uuid)`, tenant, examID, question, segment, submission, score, actor)
			id := uuid.NewString()
			if subjectIndex == 0 && i == 3 {
				staleGrade = id
			}
			exec(`INSERT INTO submission_grade(id,tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$4,$6,10,'pending_confirmation',$7::uuid)`, id, tenant, examID, submission, student, score, actor)
		}
	}
	// 当前学籍或班名变动不能重写历史报表身份和比较范围。
	exec(`UPDATE school_class SET name='现在的新班名' WHERE id=$1::uuid`, class)
	scope, err := auth.ResolveDeclaredAccessScope(auth.User{ID: actor, TenantID: tenant, Roles: []string{"school_admin"}, DataScope: map[string]any{"school_admin": map[string]any{"scope": "school", "school_id": school}}})
	if err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(db)
	parse := func() map[string]map[string]string {
		t.Helper()
		result, err := store.ExportExamReport(ctx, scope, exams[0], actor)
		if err != nil {
			t.Fatal(err)
		}
		if result.RowCount != 4 {
			t.Fatalf("rows=%d", result.RowCount)
		}
		csvRows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(result.Content), "\ufeff"))).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]map[string]string{}
		for _, row := range csvRows[1:] {
			values := map[string]string{}
			for i, key := range csvRows[0] {
				values[key] = row[i]
			}
			out[values["学号"]] = values
		}
		return out
	}
	rows := parse()
	for _, no := range []string{"001", "002"} {
		if rows[no]["总分"] != "15.00" || rows[no]["总分年级排名"] != "1" || rows[no]["考试班级"] != "冻结考试班级" || rows[no]["入学届别"] != "2026级" {
			t.Fatalf("row %s=%#v", no, rows[no])
		}
	}
	if rows["004"]["总分年级排名"] != "3" || rows["003"]["总分"] != "" || rows["003"]["英语·状态"] != "缺考" || rows["001"]["数学·年级统计人数"] != "4" || rows["001"]["英语·年级统计人数"] != "3" {
		t.Fatalf("rank/absence/denominator mismatch: %#v", rows)
	}
	narrow := auth.AccessScope{TenantID: tenant, ActorID: actor, ExamIDs: []string{exams[0]}, SchoolIDs: []string{school}}
	if _, err = store.ExportExamReport(ctx, narrow, exams[0], actor); !errors.Is(err, errReportScope) {
		t.Fatalf("single-exam scope expanded to whole report: %v", err)
	}
	// 旧总分即使仍为待确认，也不能在题分已不同后参与任何排名和均分。
	exec(`UPDATE submission_grade SET total_score=4 WHERE id=$1::uuid`, staleGrade)
	rows = parse()
	if rows["004"]["数学·成绩"] != "" || rows["004"]["总分"] != "" || rows["001"]["数学·年级统计人数"] != "3" {
		t.Fatalf("stale totals entered ranking: %#v", rows)
	}
}

package score

import (
	"database/sql"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

func TestMemoryQualityCountsOnlyActiveReviewAndArbitrationTasks(t *testing.T) {
	store := NewMemoryStore()
	for range 48 {
		store.AddReviewTask(TaskSeed{ExamID: "exam", Status: "cancelled"})
		store.AddArbitrationTask(TaskSeed{ExamID: "exam", Status: "cancelled"})
	}
	for range 9 {
		store.AddReviewTask(TaskSeed{ExamID: "exam", Status: "submitted"})
		store.AddArbitrationTask(TaskSeed{ExamID: "exam", Status: "submitted"})
	}
	store.AddReviewTask(TaskSeed{ExamID: "exam", Status: "completed"})
	store.AddReviewTask(TaskSeed{ExamID: "other-exam", Status: "pending"})
	store.AddArbitrationTask(TaskSeed{ExamID: "other-exam", Status: "pending"})
	assertMemoryTaskCounts := func(review, arbitration int) {
		t.Helper()
		counts := map[string]int{}
		for _, issue := range store.taskAndFinalQualityIssuesLocked("exam") {
			counts[issue.Code] = issue.Count
		}
		if counts["unfinished_review_tasks"] != review || counts["unfinished_arbitration_tasks"] != arbitration {
			t.Fatalf("task counts=%v, want review=%d arbitration=%d", counts, review, arbitration)
		}
	}
	assertMemoryTaskCounts(0, 0)
	for index, status := range []string{"pending", "assigned", "in_progress", "returned"} {
		store.AddReviewTask(TaskSeed{ExamID: "exam", Status: status})
		assertMemoryTaskCounts(index+1, 0)
	}
	for index, status := range []string{"pending", "assigned"} {
		store.AddArbitrationTask(TaskSeed{ExamID: "exam", Status: status})
		assertMemoryTaskCounts(4, index+1)
	}
}

type cancelledTaskFixture struct {
	tenant, actor, secondReviewer, exam, question, submission, segment, session string
	db                                                                          *sql.DB
}

func newCancelledTaskFixture(t *testing.T, db *sql.DB) cancelledTaskFixture {
	t.Helper()
	f := cancelledTaskFixture{db: db, exam: uuid.NewString(), question: uuid.NewString(), submission: uuid.NewString(), segment: uuid.NewString(), session: uuid.NewString()}
	if err := db.QueryRowContext(t.Context(), `SELECT t.id::text,u.id::text,a.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id AND u.username='teacher' JOIN app_user a ON a.tenant_id=t.id AND a.username='arbitrator' WHERE t.code='demo'`).Scan(&f.tenant, &f.actor, &f.secondReviewer); err != nil {
		t.Fatal(err)
	}
	school, file, page := uuid.NewString(), uuid.NewString(), uuid.NewString()
	f.exec(t, `INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic task count school',$1,'active')`, school, f.tenant)
	f.exec(t, `INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic task count','math','quiz',10,'grading','human_review_required','manual_after_confirmation',$4::uuid)`, f.exam, f.tenant, school, f.actor)
	f.exec(t, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','calculation',10,1,'active')`, f.question, f.tenant, f.exam)
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$1,'manual_import','quality_checked',1,1,'passed',$4::uuid)`, f.submission, f.tenant, f.exam, f.actor)
	f.exec(t, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, f.tenant, f.exam, f.actor)
	f.exec(t, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, f.tenant, f.submission, file)
	f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{}','manual','accepted')`, f.segment, f.tenant, f.submission, page, f.question)
	first := f.addReview(t, "submitted", "manual_sample", "first_mark", false)
	second := f.addReview(t, "submitted", "manual_sample", "second_mark", false)
	f.exec(t, `INSERT INTO double_mark_session(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_review_task_id,second_review_task_id,first_reviewer_id,second_reviewer_id,threshold,resolution_strategy,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,$6,$7::uuid,$8::uuid,$9::uuid,$10::uuid,1,'average','needs_arbitration',$9::uuid)`, f.session, f.tenant, f.exam, f.question, f.segment, f.submission, first, second, f.actor, f.secondReviewer)
	return f
}

func (f cancelledTaskFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func (f cancelledTaskFixture) addReview(t *testing.T, status, source, round string, deleted bool) string {
	t.Helper()
	id := uuid.NewString()
	f.exec(t, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,grade_round,created_by,deleted_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,$6,$7,$8,$9,$10::uuid,CASE WHEN $11 THEN now() END)`, id, f.tenant, f.exam, f.question, f.segment, f.submission, source, status, round, f.actor, deleted)
	return id
}

func (f cancelledTaskFixture) addArbitration(t *testing.T, status string, deleted bool) {
	t.Helper()
	f.exec(t, `INSERT INTO arbitration_task(tenant_id,double_mark_session_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,first_reviewer_id,second_reviewer_id,first_score,second_score,score_difference,status,created_by,deleted_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,$6,$7::uuid,$8::uuid,6,8,2,$9,$7::uuid,CASE WHEN $10 THEN now() END)`, f.tenant, f.session, f.exam, f.question, f.segment, f.submission, f.actor, f.secondReviewer, status, deleted)
}

func TestPostgresPublishGatesIgnoreCancelledHistoryAndCountActiveTasks(t *testing.T) {
	// 独立迁移库重现取消后保留记录的场景；同时调用两套真实门禁，避免只测一份手写 SQL。
	db := confirmationTestDB(t)
	f := newCancelledTaskFixture(t, db)
	other := newCancelledTaskFixture(t, db)
	other.addReview(t, "pending", "manual_sample", "single", false)
	other.addArbitration(t, "pending", false)
	for range 48 {
		f.addReview(t, "cancelled", "manual_sample", "single", false)
		f.addArbitration(t, "cancelled", false)
	}
	for range 7 {
		f.addReview(t, "submitted", "manual_sample", "single", false)
	}
	f.addReview(t, "completed", "manual_sample", "single", false)
	f.addReview(t, "pending", "manual_sample", "single", true)
	f.addArbitration(t, "submitted", false)
	f.addArbitration(t, "pending", true)
	baseStore, releaseStore := NewPostgresStore(db), scorerelease.NewPostgresStore(db, nil)
	assertCounts := func(tenant string, review, arbitration int) {
		t.Helper()
		quality, err := baseStore.CheckQuality(t.Context(), tenant, f.exam, true)
		if err != nil {
			t.Fatal(err)
		}
		gate, err := releaseStore.Gate(t.Context(), tenant, f.exam)
		if err != nil {
			t.Fatal(err)
		}
		baseCounts, releaseCounts := map[string]int{}, map[string]int{}
		for _, issue := range quality.Issues {
			baseCounts[issue.Code] = issue.Count
		}
		for _, issue := range gate.Blocking {
			releaseCounts[issue.Code] = issue.Count
		}
		for name, counts := range map[string]map[string]int{"base": baseCounts, "portal": releaseCounts} {
			if counts["unfinished_review_tasks"] != review || counts["unfinished_arbitration_tasks"] != arbitration {
				t.Fatalf("%s counts=%v, want review=%d arbitration=%d", name, counts, review, arbitration)
			}
		}
	}
	assertCounts(f.tenant, 0, 0)
	for index, status := range []string{"pending", "assigned", "in_progress", "returned"} {
		f.addReview(t, status, []string{"manual_sample", "ai_low_confidence", "score_anomaly", "subjective_default_review"}[index], "single", false)
		assertCounts(f.tenant, index+1, 0)
	}
	for index, status := range []string{"pending", "assigned"} {
		f.addArbitration(t, status, false)
		assertCounts(f.tenant, 4, index+1)
	}
	assertCounts(uuid.NewString(), 0, 0)
	// 只结束当前有效任务，历史取消记录不删除；重新检查应立即消除这两项阻断。
	f.exec(t, `UPDATE review_task SET status='cancelled' WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL`, f.tenant, f.exam)
	f.exec(t, `UPDATE arbitration_task SET status='cancelled' WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status IN ('pending','assigned') AND deleted_at IS NULL`, f.tenant, f.exam)
	assertCounts(f.tenant, 0, 0)
}

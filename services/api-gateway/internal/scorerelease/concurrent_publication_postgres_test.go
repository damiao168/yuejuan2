package scorerelease

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

type concurrentPublicationFixture struct {
	tenantID, examID, actorID, submissionID string
	questionIDs                             [2]string
}

// 使用已迁移的独立库，构造一份有两道冻结题目且已确认的答卷，发布时仍执行正式门禁。
func seedConcurrentPublication(t *testing.T, db *sql.DB) concurrentPublicationFixture {
	t.Helper()
	ctx := t.Context()
	f := concurrentPublicationFixture{examID: uuid.NewString(), submissionID: uuid.NewString()}
	if err := db.QueryRowContext(ctx, `SELECT t.id::text,u.id::text FROM tenant t JOIN app_user u ON u.tenant_id=t.id AND u.username='teacher' WHERE t.code='demo'`).Scan(&f.tenantID, &f.actorID); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		result, err := db.ExecContext(ctx, query, args...)
		if err != nil {
			t.Fatalf("seed concurrent publication: %v\n%s", err, query)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatalf("fixture write affected %d rows: %v", count, err)
		}
	}
	school, year, cohort, grade, class, student, asset, page := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1,$2,'发布并发测试学校','SCORE-PUBLISH','active')`, school, f.tenantID)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1,$2,$3,'2026学年',2026,2027,'2026-09-01','2027-08-31')`, year, f.tenantID, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1,$2,$3,'senior',2026,2029,'2026级')`, cohort, f.tenantID, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1,$2,$3,'高一','active',$4,$5)`, grade, f.tenantID, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1,$2,$3,$4,'高一一班','SCORE-PUBLISH-1','active',$5,$6)`, class, f.tenantID, school, grade, year, cohort)
	exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1,$2,$3,$4,'001','合成学生','active')`, student, f.tenantID, school, class)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'发布并发测试','mathematics','midterm_exam',10,'grading','human_review_required','manual_after_confirmation',$4)`, f.examID, f.tenantID, school, f.actorID)
	exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1,$2,$3,'001','合成学生',$4,'高一',$5,'高一一班')`, f.tenantID, f.examID, student, grade, class)
	exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1,$2,$3,$4,'001','manual_import','quality_checked',1,1,'passed',$5)`, f.submissionID, f.tenantID, f.examID, student, f.actorID)
	exec(`INSERT INTO file_asset(id,tenant_id,school_id,exam_id,submission_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,$3,$4,$5,'answer_page',$5,'synthetic.png','image/png',1,repeat('a',64),'test','synthetic.png','private',$6)`, asset, f.tenantID, school, f.examID, f.submissionID, f.actorID)
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2,$3,$4,1,'accepted')`, page, f.tenantID, f.submissionID, asset)
	exec(`INSERT INTO submission_grade(tenant_id,exam_id,submission_id,student_id,anonymous_code,total_score,max_score,status,created_by) VALUES($1,$2,$3,$4,'001',4,10,'confirmed',$5)`, f.tenantID, f.examID, f.submissionID, student, f.actorID)
	for i := range f.questionIDs {
		question, segment := uuid.NewString(), uuid.NewString()
		f.questionIDs[i] = question
		no := strconv.Itoa(i + 1)
		exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,$4,'short_answer',5,$5,'active')`, question, f.tenantID, f.examID, no, i+1)
		exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash)
SELECT $1::uuid,$2::uuid,$3::uuid,id,version,'structured_steps','["text_span"]','R2','{}','{}','{"max_score":5}','{}',repeat('b',64) FROM subject_profile
WHERE tenant_id=$1::uuid AND subject_code='mathematics' AND education_stage='senior' LIMIT 1`, f.tenantID, f.examID, question)
		exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$4,$5,$6,'{"x":0,"y":0,"width":1,"height":1}','configured_answer_area','accepted')`, segment, f.tenantID, f.submissionID, page, question, no)
		exec(`INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,status,created_by) VALUES($1,$2,$3,$4,$5,$6,'001',2,5,'single_review','confirmed',$7)`, f.tenantID, f.examID, question, no, segment, f.submissionID, f.actorID)
	}
	return f
}

// 等到 PostgreSQL 观察到第二笔发布阻塞在考试锁，再让先到事务提交，避免用固定延时猜并发顺序。
func waitForConcurrentPublicationLock(ctx context.Context, db *sql.DB) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var waiting bool
		err := db.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid()
    AND wait_event_type='Lock' AND wait_event='advisory' AND query LIKE $1
)`, "%pg_advisory_xact_lock%").Scan(&waiting)
		if err != nil || waiting {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func TestPostgresConcurrentRegradePublicationPreservesBothCorrections(t *testing.T) {
	db := formalExportTestDB(t)
	f := seedConcurrentPublication(t, db)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	store := NewPostgresStore(db, nil)
	service := NewService(store)
	base, err := service.Create(ctx, f.tenantID, f.examID, f.actorID, CreateInput{Reason: "synthetic initial release", IdempotencyKey: "parallel-initial"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(ctx, f.tenantID, base.ID, f.actorID); err != nil {
		t.Fatal(err)
	}
	create := func(questionID, key string, score float64) Release {
		t.Helper()
		draft, err := service.CreateFromRegrade(ctx, f.tenantID, f.examID, f.actorID, CreateRegradeInput{
			SourceReleaseID: base.ID, QuestionID: questionID, Reason: "verified correction", IdempotencyKey: key,
			Changes: []RegradeChange{{SubmissionID: f.submissionID, QuestionID: questionID, Score: score, MaxScore: 5}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return draft
	}
	first, second := create(f.questionIDs[0], "parallel-first", 4), create(f.questionIDs[1], "parallel-second", 3)
	firstInTx, permitFirstCommit := make(chan struct{}), make(chan struct{})
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := store.PublishGuarded(ctx, f.tenantID, first.ID, f.actorID, func(ctx context.Context, _ *sql.Tx, _ Release, _ Gate) error {
			close(firstInTx)
			select {
			case <-permitFirstCommit:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		firstDone <- err
	}()
	select {
	case <-firstInTx:
	case err := <-firstDone:
		t.Fatalf("first publication did not reach its transaction guard: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() {
		_, err := service.Publish(ctx, f.tenantID, second.ID, f.actorID)
		secondDone <- err
	}()
	if err := waitForConcurrentPublicationLock(ctx, db); err != nil {
		t.Fatalf("second publication did not wait for the first to commit: %v", err)
	}
	close(permitFirstCommit)
	for _, done := range []<-chan error{firstDone, secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}

	// 重放先公布的草稿时必须返回其原版本，不能把已经合并的第二版指针切回去。
	retry, err := service.Publish(ctx, f.tenantID, first.ID, f.actorID)
	if err != nil || retry.ID != first.ID || retry.SupersedesReleaseID != base.ID {
		t.Fatalf("replay returned incorrect publication: %+v %v", retry, err)
	}
	for _, expected := range []struct {
		id, supersedes string
		scores         [2]float64
	}{
		{base.ID, "", [2]float64{2, 2}},
		{first.ID, base.ID, [2]float64{4, 2}},
		{second.ID, first.ID, [2]float64{4, 3}},
	} {
		detail, err := service.Get(ctx, f.tenantID, expected.id)
		if err != nil || detail.Release.SupersedesReleaseID != expected.supersedes || len(detail.Items) != 1 ||
			len(detail.Questions) != 2 || detail.Items[0].TotalScore != expected.scores[0]+expected.scores[1] ||
			detail.Questions[0].Score != expected.scores[0] || detail.Questions[1].Score != expected.scores[1] {
			t.Fatalf("wrong frozen scores for release %s: %+v %v", expected.id, detail, err)
		}
	}
	current, err := service.CurrentPublished(ctx, f.tenantID, f.examID)
	if err != nil || current.Release.ID != second.ID || current.Release.SourceReleaseID != base.ID {
		t.Fatalf("incorrect current publication lineage: %+v %v", current.Release, err)
	}
}

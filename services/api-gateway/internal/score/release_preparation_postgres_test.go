package score

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/scorerelease"
	"github.com/google/uuid"
)

func TestPostgresReleaseCreationPreparesGradesAtomically(t *testing.T) {
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
	exec(`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Synthetic automatic preparation',$1,'active')`, school, tenant)
	exec(`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic year',2026,2027,'2026-09-01','2027-08-31')`, year, tenant, school)
	exec(`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name) VALUES($1::uuid,$2::uuid,$3::uuid,'junior',2026,2029,'Synthetic cohort')`, cohort, tenant, school)
	exec(`INSERT INTO grade(id,tenant_id,school_id,name,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic grade','active',$4::uuid,$5::uuid)`, grade, tenant, school, year, cohort)
	exec(`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Synthetic class',$1,'active',$5::uuid,$6::uuid)`, class, tenant, school, grade, year, cohort)
	exec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,'Synthetic preparation','physics','',10,'grading','human_review_required','manual_after_confirmation',$4::uuid)`, exam, tenant, school, actor)
	exec(`INSERT INTO subject_profile(id,tenant_id,code,education_stage,subject_code,version) VALUES($1::uuid,$2::uuid,$1,'junior','physics',1)`, profile, tenant)
	exec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1::uuid,$2::uuid,$3::uuid,'Q1','calculation',10,1,'active')`, question, tenant, exam)
	exec(`INSERT INTO exam_question_snapshot(tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'structured_steps','[]','R1','{}','{}','{}','{}',repeat('a',64))`, tenant, exam, question, profile)
	student := uuid.NewString()
	for _, id := range []string{student, uuid.NewString()} {
		exec(`INSERT INTO student(id,tenant_id,school_id,class_id,student_no,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$1,'Synthetic student','active')`, id, tenant, school, class)
		exec(`INSERT INTO exam_candidate_snapshot(tenant_id,exam_id,student_id,student_no_snapshot,student_name_snapshot,grade_id_snapshot,grade_name_snapshot,class_id_snapshot,class_name_snapshot) VALUES($1::uuid,$2::uuid,$3::uuid,$3,'Synthetic student',$4::uuid,'Synthetic grade',$5::uuid,'Synthetic class')`, tenant, exam, id, grade, class)
	}
	submission, file, page, segment, task := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	exec(`INSERT INTO submission(id,tenant_id,exam_id,student_id,candidate_no,source_type,status,expected_page_count,actual_page_count,quality_status,collected_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$4,'manual_import','quality_checked',1,0,'passed',$5::uuid)`, submission, tenant, exam, student, actor)
	exec(`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2::uuid,$3::uuid,'exam',$3::uuid,'synthetic.png','image/png',1,repeat(replace($1::text,'-',''),2),'test',$1,'tenant',$4::uuid)`, file, tenant, exam, actor)
	exec(`INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'accepted')`, page, tenant, submission, file)
	exec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'Q1','{}','manual','accepted')`, segment, tenant, submission, page, question)
	exec(`INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,$6,'manual_sample','submitted',$7::uuid)`, task, tenant, exam, question, segment, submission, actor)
	exec(`INSERT INTO human_grade(tenant_id,review_task_id,answer_segment_id,reviewer_id,score,max_score,grade_round) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,8,10,'single')`, tenant, task, segment, actor)
	store := NewPostgresStore(db)
	releases := scorerelease.NewService(scorerelease.NewPostgresStore(db, nil).WithGradePreparer(store))
	input := func(key string) scorerelease.CreateInput {
		return scorerelease.CreateInput{Reason: "synthetic automatic preparation", IdempotencyKey: key}
	}
	assertNoPreparedFacts := func() {
		t.Helper()
		var count int
		if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM final_grade WHERE exam_id=$1)+(SELECT count(*) FROM submission_grade WHERE exam_id=$1)+(SELECT count(*) FROM score_release WHERE exam_id=$1)`, exam).Scan(&count); err != nil || count != 0 {
			t.Fatalf("blocked creation retained prepared facts: %d %v", count, err)
		}
	}
	t.Run("missing pages rolls back automatic totals and finals", func(t *testing.T) {
		if _, err := releases.Create(ctx, tenant, exam, actor, input("missing-pages")); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("missing pages allowed draft: %v", err)
		}
		assertNoPreparedFacts()
	})
	exec(`UPDATE submission SET actual_page_count=1 WHERE id=$1::uuid`, submission)
	t.Run("unidentified received paper is not hidden as absence", func(t *testing.T) {
		exec(`UPDATE submission SET student_id=NULL WHERE id=$1::uuid`, submission)
		if _, err := releases.Create(ctx, tenant, exam, actor, input("unknown-student")); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("unknown paper allowed draft: %v", err)
		}
		assertNoPreparedFacts()
		exec(`UPDATE submission SET student_id=$2::uuid WHERE id=$1::uuid`, submission, student)
	})
	t.Run("unfinished review still blocks and rolls back generated finals", func(t *testing.T) {
		pending := uuid.NewString()
		exec(`INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1',$5::uuid,$6::uuid,$6,'score_anomaly','pending',$7::uuid)`, pending, tenant, exam, question, segment, submission, actor)
		if _, err := releases.Create(ctx, tenant, exam, actor, input("unfinished-review")); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("unfinished review allowed draft: %v", err)
		}
		assertNoPreparedFacts()
		exec(`UPDATE review_task SET deleted_at=now() WHERE id=$1::uuid`, pending)
	})
	t.Run("unclosed collecting cannot silently treat a missing paper as absence", func(t *testing.T) {
		if _, err := releases.Create(ctx, tenant, exam, actor, input("collection-open")); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("open collection allowed automatic absence: %v", err)
		}
		gate, err := releases.Gate(ctx, tenant, exam)
		if err != nil || gate.Counts["capture_collection_incomplete"] != 1 {
			t.Fatalf("missing collection blocker: %+v %v", gate, err)
		}
		assertNoPreparedFacts()
		paper, template, batch, captureFile := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
		exec(`INSERT INTO exam_paper(id,tenant_id,exam_id,file_asset_id,version_no,status,uploaded_by) VALUES($1,$2,$3,$4,1,'ready',$5)`, paper, tenant, exam, file, actor)
		exec(`INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by) VALUES($1,$2,$3,$4,1,'synthetic template','locked',1,'{}',repeat('a',64),$5)`, template, tenant, exam, paper, actor)
		exec(`INSERT INTO capture_batch(id,tenant_id,exam_id,name,source_type,operator_id,status) VALUES($1,$2,$3,'synthetic batch','web_upload',$4,'ready')`, batch, tenant, exam, actor)
		exec(`INSERT INTO capture_file(id,tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,idempotency_key,uploaded_by,status,page_count) VALUES($1,$2,$3,$4,'synthetic.png','image/png',repeat('a',64),1,'synthetic-file',$5,'completed',1)`, captureFile, tenant, batch, file, actor)
		exec(`INSERT INTO capture_page(tenant_id,capture_batch_id,capture_file_id,source_index,sequence_no,decoded_file_asset_id,submission_id,submission_page_id,assigned_page_no,status) VALUES($1,$2,$3,1,1,$4,$5,$6,1,'ready')`, tenant, batch, captureFile, file, submission, page)
		exec(`UPDATE submission SET identity_status='matched' WHERE id=$1`, submission)
		collections := capture.NewPostgresStore(db)
		summary, err := collections.GetCollectionSummary(ctx, tenant, exam)
		if err != nil {
			t.Fatal(err)
		}
		closed, err := collections.CompleteCollection(ctx, tenant, exam, actor, capture.CompleteCollectionInput{Revision: summary.Revision, IdempotencyKey: "close-before-publication", Reason: "verified received papers and absent list"})
		if err != nil || !closed.Closed || closed.AbsentCount != 1 {
			t.Fatalf("finish collection through product command: %+v %v", closed, err)
		}
	})
	var initial scorerelease.Release
	t.Run("completed marking prepares once under concurrent retry and permits automatic absence", func(t *testing.T) {
		var workers sync.WaitGroup
		type outcome struct {
			release scorerelease.Release
			err     error
		}
		results := make(chan outcome, 3)
		for range 3 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				release, err := releases.Create(ctx, tenant, exam, actor, input("automatic-initial"))
				results <- outcome{release, err}
			}()
		}
		workers.Wait()
		close(results)
		for result := range results {
			if result.err != nil || initial.ID != "" && initial.ID != result.release.ID {
				t.Fatalf("concurrent command changed result: %+v %v", result.release, result.err)
			}
			initial = result.release
		}
		if initial.ID == "" {
			t.Fatal("missing release")
		}
		var status string
		var total float64
		if err := db.QueryRowContext(ctx, `SELECT status,total_score::float8 FROM submission_grade WHERE exam_id=$1`, exam).Scan(&status, &total); err != nil || status != "confirmed" || total != 8 {
			t.Fatalf("automatic preparation: %s %v %v", status, total, err)
		}
	})
	var revision int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM submission_grade WHERE exam_id=$1`, exam).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	t.Run("already confirmed grades are reused without recalculation", func(t *testing.T) {
		if _, err := releases.Create(ctx, tenant, exam, actor, input("confirmed-second")); err != nil {
			t.Fatal(err)
		}
		var next int64
		if err := db.QueryRowContext(ctx, `SELECT revision FROM submission_grade WHERE exam_id=$1`, exam).Scan(&next); err != nil || next != revision {
			t.Fatalf("confirmed total was rewritten: %d -> %d %v", revision, next, err)
		}
	})
	t.Run("confirmed mismatch remains blocked rather than silently fixed", func(t *testing.T) {
		exec(`UPDATE submission_grade SET total_score=7 WHERE exam_id=$1`, exam)
		if _, err := releases.Create(ctx, tenant, exam, actor, input("confirmed-mismatch")); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("confirmed mismatch: %v", err)
		}
		var score float64
		if err := db.QueryRowContext(ctx, `SELECT total_score::float8 FROM submission_grade WHERE exam_id=$1`, exam).Scan(&score); err != nil || score != 7 {
			t.Fatalf("confirmed mismatch rewritten: %v %v", score, err)
		}
		exec(`UPDATE submission_grade SET total_score=8 WHERE exam_id=$1`, exam)
	})
	t.Run("snapshot failure rolls back recalculation and confirmation", func(t *testing.T) {
		exec(`UPDATE submission_grade SET status='pending_confirmation',total_score=7,confirmed_at=NULL,confirmed_by=NULL WHERE exam_id=$1`, exam)
		exec(`CREATE FUNCTION fail_synthetic_release() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic snapshot insert failure'; END $$`)
		exec(`CREATE TRIGGER fail_synthetic_release BEFORE INSERT ON score_release FOR EACH ROW EXECUTE FUNCTION fail_synthetic_release()`)
		_, err := releases.Create(ctx, tenant, exam, actor, input("snapshot-insert-fails"))
		exec(`DROP TRIGGER fail_synthetic_release ON score_release`)
		exec(`DROP FUNCTION fail_synthetic_release()`)
		if err == nil {
			t.Fatal("expected snapshot failure")
		}
		var status string
		var score float64
		var confirmed sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT status,total_score::float8,confirmed_at FROM submission_grade WHERE exam_id=$1`, exam).Scan(&status, &score, &confirmed); err != nil || status != "pending_confirmation" || score != 7 || confirmed.Valid {
			t.Fatalf("partial preparation committed: %s %v %v %v", status, score, confirmed, err)
		}
	})
	t.Run("publication still rechecks marking and retains immutable old snapshot", func(t *testing.T) {
		if _, err := releases.Create(ctx, tenant, exam, actor, input("prepare-after-recovery")); err != nil {
			t.Fatal(err)
		}
		if _, err := releases.Publish(ctx, tenant, initial.ID, actor); err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE final_grade SET score=9 WHERE exam_id=$1`, exam)
		draft, err := releases.Create(ctx, tenant, exam, actor, input("after-published"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := releases.Publish(ctx, tenant, draft.ID, actor); !errors.Is(err, scorerelease.ErrGateBlocked) {
			t.Fatalf("stale total allowed publication: %v", err)
		}
		current, err := releases.CurrentPublished(ctx, tenant, exam)
		if err != nil || current.Release.ID != initial.ID || len(current.Items) != 1 || current.Items[0].TotalScore != 8 {
			t.Fatalf("published snapshot changed: %+v %v", current, err)
		}
	})
}

func TestPrepareForReleaseRejectsMissingTransaction(t *testing.T) {
	if _, err := NewPostgresStore(nil).PrepareForReleaseTx(context.Background(), nil, "tenant", "exam", "actor"); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}

package grading

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// 复现概览有待处理项、分配页却为空；使用隔离 schema 验证补建、真实分配和重复恢复。
func TestPostgresBlockedScoringCreatesAssignableManualTasks(t *testing.T) {
	ctx, db := newDurableScoringFixture(t)
	aiReviewExec(t, ctx, db, "CREATE TABLE ai_grade (LIKE public.ai_grade INCLUDING ALL)")
	tenant, actor, exam, submission, run := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	aiReviewExec(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic manual recovery',$2,'active')`, tenant, "blocked-review-"+tenant)
	aiReviewExec(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, tenant, exam, actor)
	aiReviewExec(t, ctx, db, `INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,'fixture','needs_review',6,0,$4)`, run, tenant, exam, actor)
	fill := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "14", "observe_rule")
	large := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "15", "image_ai")
	assigned := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "16", "image_ai")
	staleCrop := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "17", "image_ai")
	staleSnapshot := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "18", "image_ai")
	confirmed := seedBlockedReviewItem(t, ctx, db, tenant, actor, exam, submission, run, "19", "image_ai")
	aiReviewExec(t, ctx, db, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,reason_code,scoring_run_id,created_by) VALUES($1,$2,$3,'16',$4,$5,'synthetic','manual_sample','assigned',$6,'teacher_selected',$7,$6)`, tenant, exam, assigned.question, assigned.segment, submission, actor, run)
	aiReviewExec(t, ctx, db, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, staleCrop.segment, strings.Repeat("b", 64))
	aiReviewExec(t, ctx, db, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version) SELECT $2,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version+1 FROM exam_question_snapshot WHERE id=$1`, staleSnapshot.snapshot, uuid.NewString())
	aiReviewExec(t, ctx, db, `INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,source,status,score,max_score,confirmed_by) VALUES($1,$2,$3,$4,$5,$6,'human','confirmed',3,5,$7)`, tenant, exam, submission, confirmed.question, confirmed.segment, run, actor)
	store := NewPostgresStore(db)
	images := &coordinatorFixtureImages{db: db, runtime: workerruntime.NewPostgresStore(db)}
	coordinator := NewScoringCoordinator(store, images.runtime, images, time.Minute)
	for range 2 {
		if err := coordinator.resumePending(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if images.observations != 0 || images.schedules != 0 {
		t.Fatal("manual recovery invoked a model")
	}
	tasks, err := review.NewPostgresStore(db).ListTasks(ctx, tenant, review.ListFilter{ExamID: exam, Status: "active"})
	if err != nil || len(tasks) != 3 {
		t.Fatalf("assignable list contains %d tasks: %v", len(tasks), err)
	}
	bySegment := map[string]review.ReviewTask{}
	for _, task := range tasks {
		bySegment[task.AnswerSegmentID] = task
	}
	for _, item := range []aiReviewIdentity{fill, large} {
		task := bySegment[item.segment]
		if task.ID == "" || task.Status != "pending" || task.AssignedTo != "" || task.ReasonCode != "visual_model_unavailable" {
			t.Fatalf("blocked answer is not an unassigned manual task: %+v", task)
		}
		if _, err := review.NewPostgresStore(db).AssignTask(ctx, tenant, task.ID, actor, review.AssignTaskInput{AssignedTo: actor, ExpectedRevision: task.Revision}); err != nil {
			t.Fatal("recovered task cannot be assigned", err)
		}
	}
	if task := bySegment[assigned.segment]; task.AssignedTo != actor || task.ReasonCode != "teacher_selected" {
		t.Fatal("existing teacher task was changed")
	}
	var grades int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM question_grade`).Scan(&grades); err != nil || grades != 1 {
		t.Fatalf("manual recovery changed existing grades: %d, %v", grades, err)
	}
}

func seedBlockedReviewItem(t *testing.T, ctx context.Context, db *sql.DB, tenant, actor, exam, submission, run, no, route string) aiReviewIdentity {
	t.Helper()
	value := aiReviewIdentity{segment: uuid.NewString(), question: uuid.NewString(), snapshot: uuid.NewString(), input: uuid.NewString()}
	crop, hash := uuid.NewString(), strings.Repeat("a", 64)
	aiReviewExec(t, ctx, db, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2,$3,'answer_segment',$1::uuid,'fixture.png','image/png',1,$4,'fixture',$1::uuid::text,'private',$5)`, crop, tenant, exam, hash, actor)
	kind := "calculation"
	if route == "observe_rule" {
		kind = "fill_blank"
	}
	aiReviewExec(t, ctx, db, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status,answer_area,options) VALUES($1,$2,$3,$4,$5,5,1,'draft','{}','[]')`, value.question, tenant, exam, no, kind)
	aiReviewExec(t, ctx, db, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,normalized_bbox,source,status,processing_status,crop_file_asset_id,crop_sha256) VALUES($1,$2,$3,$4,$5,$6,'{"x":0,"y":0,"width":1,"height":1}','{}','manual','accepted','completed',$7,$8)`, value.segment, tenant, submission, uuid.NewString(), value.question, no, crop, hash)
	aiReviewExec(t, ctx, db, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'exact_text','[]','R2','{}','{}','{}','{"mode":"AI_ASSIST"}',$6)`, value.snapshot, tenant, exam, value.question, uuid.NewString(), hash)
	aiReviewExec(t, ctx, db, `INSERT INTO answer_input_version(id,tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,template_content_hash,answer_scope,members_json,input_hash) VALUES($1,$2,$3,$4,$5,$6,$6,'question','[{}]',$7)`, value.input, tenant, value.segment, submission, crop, hash, hash+value.input)
	// 即使已经有观察回执，配置阻塞的恢复也只能补人工任务，不能再次调度模型。
	aiReviewExec(t, ctx, db, `INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,stage,processing_status,decision_status,reason_code,observation_json) VALUES($1,$2,$3,$4,$5,$6,$7,'observe','blocked','undecided','visual_model_unavailable','{"fixture":true}')`, tenant, run, value.segment, value.question, value.input, value.snapshot, route)
	return value
}

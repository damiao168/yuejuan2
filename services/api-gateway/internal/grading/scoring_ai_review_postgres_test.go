package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// 使用独立 schema 复现“模型成功但缺正式任务”的恢复；不调用供应商，也不改公共业务数据。
func TestPostgresScoringAIReviewRecoveryKeepsVersionsAndTeacherWork(t *testing.T) {
	ctx, db := newDurableScoringFixture(t)
	for _, table := range []string{"ai_grade", "exam", "question_solution", "submission_page", "scoring_evidence"} {
		aiReviewExec(t, ctx, db, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	tenant, actor, exam, submission, run := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	aiReviewExec(t, ctx, db, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic AI review',$2,'active')`, tenant, "ai-review-"+tenant)
	aiReviewExec(t, ctx, db, `INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, tenant, exam, actor)
	aiReviewExec(t, ctx, db, `INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,'fixture','needs_review',5,0,$4)`, run, tenant, exam, actor)
	ready := seedAIReviewSuccess(t, ctx, db, tenant, actor, exam, submission, run, "15")
	assigned := seedAIReviewSuccess(t, ctx, db, tenant, actor, exam, submission, run, "16")
	staleCrop := seedAIReviewSuccess(t, ctx, db, tenant, actor, exam, submission, run, "17")
	failedGrade := seedAIReviewSuccess(t, ctx, db, tenant, actor, exam, submission, run, "18")
	staleSnapshot := seedAIReviewSuccess(t, ctx, db, tenant, actor, exam, submission, run, "19")
	aiReviewExec(t, ctx, db, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,assigned_to,reason_code,scoring_run_id,created_by) VALUES($1,$2,$3,'16',$4,$5,'synthetic','manual_sample','assigned',$6,'teacher_selected',$7,$6)`, tenant, exam, assigned.question, assigned.segment, submission, actor, run)
	aiReviewExec(t, ctx, db, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, staleCrop.segment, strings.Repeat("b", 64))
	aiReviewExec(t, ctx, db, `UPDATE ai_grade SET status='failed',failure_reason='synthetic failure' WHERE id=$1`, failedGrade.grade)
	aiReviewExec(t, ctx, db, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version) SELECT $2,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version+1 FROM exam_question_snapshot WHERE id=$1`, staleSnapshot.snapshot, uuid.NewString())
	store := NewPostgresStore(db)
	images := &coordinatorFixtureImages{db: db, runtime: workerruntime.NewPostgresStore(db)}
	coordinator := NewScoringCoordinator(store, images.runtime, images, time.Minute)
	for range 2 {
		if err := coordinator.resumePending(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if images.observations != 0 || images.schedules != 0 {
		t.Fatal("review recovery repeated model work")
	}
	var tasks, grades int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM review_task),(SELECT count(*) FROM question_grade)`).Scan(&tasks, &grades); err != nil || tasks != 2 || grades != 0 {
		t.Fatalf("review tasks=%d confirmed grades=%d err=%v", tasks, grades, err)
	}
	var taskID string
	if err := db.QueryRowContext(ctx, `SELECT id::text FROM review_task WHERE answer_segment_id=$1 AND source='subjective_default_review' AND status='pending' AND assigned_to IS NULL AND reason_code='ai_suggestion_ready' AND scoring_run_id=$2`, ready.segment, run).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	assertAIReviewContext(t, ctx, db, tenant, taskID, ready)
	assertAIReviewNullListProjection(t, ctx, db, tenant, taskID, ready.grade)
	var reason, status, owner string
	if err := db.QueryRowContext(ctx, `SELECT reason_code,status,assigned_to::text FROM review_task WHERE answer_segment_id=$1`, assigned.segment).Scan(&reason, &status, &owner); err != nil || reason != "teacher_selected" || status != "assigned" || owner != actor {
		t.Fatalf("teacher task changed: %s %s %s %v", reason, status, owner, err)
	}
	// 当前裁图改变后，任务仍保留；旧图像建议不能继续出现在可采纳上下文。
	aiReviewExec(t, ctx, db, `UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, ready.segment, strings.Repeat("c", 64))
	value, err := review.NewPostgresStore(db).GetTaskContext(ctx, tenant, taskID)
	if err != nil || value.AISecondOpinion != nil {
		t.Fatalf("stale image suggestion remained available: available=%t err=%v", value.AISecondOpinion != nil, err)
	}
}

type aiReviewIdentity struct{ segment, question, snapshot, input, grade string }

func seedAIReviewSuccess(t *testing.T, ctx context.Context, db *sql.DB, tenant, actor, exam, submission, run, no string) aiReviewIdentity {
	t.Helper()
	value := aiReviewIdentity{segment: uuid.NewString(), question: uuid.NewString(), snapshot: uuid.NewString(), input: uuid.NewString(), grade: uuid.NewString()}
	crop, aiRun, hash := uuid.NewString(), uuid.NewString(), strings.Repeat("a", 64)
	aiReviewExec(t, ctx, db, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2,$3,'answer_segment',$1::uuid,'fixture.png','image/png',1,$4,'fixture',$1::uuid::text,'private',$5)`, crop, tenant, exam, hash, actor)
	aiReviewExec(t, ctx, db, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status,answer_area,options) VALUES($1,$2,$3,$4,'calculation',5,1,'draft','{}','[]')`, value.question, tenant, exam, no)
	aiReviewExec(t, ctx, db, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,normalized_bbox,source,status,processing_status,crop_file_asset_id,crop_sha256) VALUES($1,$2,$3,$4,$5,$6,'{"x":0,"y":0,"width":1,"height":1}','{}','manual','accepted','completed',$7,$8)`, value.segment, tenant, submission, uuid.NewString(), value.question, no, crop, hash)
	aiReviewExec(t, ctx, db, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'exact_text','[]','R2','{}','{}','{"version":"fixture-v1","status":"published","points":[],"deductions":[],"examples":[]}','{"mode":"AI_ASSIST"}',$6)`, value.snapshot, tenant, exam, value.question, uuid.NewString(), hash)
	aiReviewExec(t, ctx, db, `INSERT INTO answer_input_version(id,tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,template_content_hash,answer_scope,members_json,input_hash) VALUES($1,$2,$3,$4,$5,$6,$6,'question','[{}]',$7)`, value.input, tenant, value.segment, submission, crop, hash, hash+value.input)
	aiReviewExec(t, ctx, db, `INSERT INTO subjective_grading_run(id,tenant_id,answer_segment_id,answer_version,question_id,exam_question_snapshot_id,rubric_version,model_version,prompt_version,request_id,status,grade_id,input_version_id,input_mode,input_protocol,image_context_json) VALUES($1,$2,$3,$4::uuid::text,$5,$6,'fixture-v1','fixture-v1','fixture-v1',$1::uuid::text,'succeeded',$7,$4::uuid,'image','grading-image-v3','{}')`, aiRun, tenant, value.segment, value.input, value.question, value.snapshot, value.grade)
	raw, _ := json.Marshal(map[string]any{"schema_version": "grading-image-v3", "input_version_id": value.input, "assessment_snapshot_id": value.snapshot, "observation": map[string]any{"input_version_id": value.input, "crop_sha256": hash, "members": []any{map[string]any{"question_id": value.question}}}, "judgments": []any{}})
	aiReviewExec(t, ctx, db, `INSERT INTO ai_grade(id,tenant_id,answer_segment_id,question_id,question_no,question_type,answer_version,exam_question_snapshot_id,grader_type,rule_version,suggested_score,max_score,confidence,needs_human_review,raw_output,created_by,status,rubric_version,delivery_mode,subjective_grading_run_id) VALUES($1,$2,$3,$4,$5,'calculation',$6,$7,'llm_subjective','fixture-v1',4,5,0.98,true,$8,$9,'succeeded','fixture-v1','teacher_suggestion',$10)`, value.grade, tenant, value.segment, value.question, no, value.input, value.snapshot, raw, actor, aiRun)
	aiReviewExec(t, ctx, db, `INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,stage,processing_status,decision_status,subjective_run_id) VALUES($1,$2,$3,$4,$5,$6,'image_ai','done','succeeded','suggestion_ready',$7)`, tenant, run, value.segment, value.question, value.input, value.snapshot, aiRun)
	return value
}

func assertAIReviewContext(t *testing.T, ctx context.Context, db *sql.DB, tenant, taskID string, identity aiReviewIdentity) {
	t.Helper()
	value, err := review.NewPostgresStore(db).GetTaskContext(ctx, tenant, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if value.Task.ReasonCode != "ai_suggestion_ready" || value.AISecondOpinion == nil || value.AISecondOpinion.Metadata["id"] != identity.grade || value.AISecondOpinion.Metadata["answer_version"] != identity.input {
		t.Fatalf("current image suggestion missing from task: reason=%s available=%t", value.Task.ReasonCode, value.AISecondOpinion != nil)
	}
	binding, ok := value.AISecondOpinion.Metadata["input_binding"].(map[string]any)
	if !ok || binding["input_version_id"] != identity.input || binding["assessment_snapshot_id"] != identity.snapshot || binding["question_id"] != identity.question {
		t.Fatal("image suggestion lost its trusted binding")
	}
}

func assertAIReviewNullListProjection(t *testing.T, ctx context.Context, db *sql.DB, tenant, taskID, gradeID string) {
	t.Helper()
	// 只在夹具中复现旧 nil 列表；投影不得把对象、字符串等损坏数据伪装成合法空数组。
	aiReviewExec(t, ctx, db, `UPDATE ai_grade SET matched_points='null',missing_points='null',evidence='null',risk_flags='null' WHERE id=$1`, gradeID)
	value, err := review.NewPostgresStore(db).GetTaskContext(ctx, tenant, taskID)
	if err != nil || value.AISecondOpinion == nil {
		t.Fatalf("null-list suggestion unavailable: %v", err)
	}
	for _, field := range []string{"matched_points", "missing_points", "evidence", "risk_flags"} {
		list, ok := value.AISecondOpinion.Metadata[field].([]any)
		if !ok || len(list) != 0 {
			t.Fatalf("%s JSON null was not projected as an empty array", field)
		}
	}
	aiReviewExec(t, ctx, db, `UPDATE ai_grade SET matched_points='{}',missing_points='"invalid"',evidence='1',risk_flags='true' WHERE id=$1`, gradeID)
	value, err = review.NewPostgresStore(db).GetTaskContext(ctx, tenant, taskID)
	if err != nil || value.AISecondOpinion == nil {
		t.Fatalf("invalid-list projection unavailable: %v", err)
	}
	for field, expected := range map[string]string{"matched_points": "{}", "missing_points": `"invalid"`, "evidence": "1", "risk_flags": "true"} {
		raw, err := json.Marshal(value.AISecondOpinion.Metadata[field])
		if err != nil || string(raw) != expected {
			t.Fatalf("%s invalid type was changed: %s, %v", field, raw, err)
		}
	}
}

func aiReviewExec(t *testing.T, ctx context.Context, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

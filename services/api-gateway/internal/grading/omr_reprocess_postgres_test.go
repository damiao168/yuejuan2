package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

type completedOMRFixture struct {
	ctx                                                         context.Context
	db                                                          *sql.DB
	store                                                       *PostgresStore
	tenant, actor, run, omr, segment, other, review, crop, task string
}

func seedCompletedOMRFixture(t *testing.T) completedOMRFixture {
	t.Helper()
	ctx, db := newDurableScoringFixture(t)
	f := completedOMRFixture{ctx: ctx, db: db, store: NewPostgresStore(db), tenant: uuid.NewString(), actor: uuid.NewString(), run: uuid.NewString(), omr: uuid.NewString(), segment: uuid.NewString(), other: uuid.NewString(), review: uuid.NewString(), crop: uuid.NewString()}
	for _, table := range []string{"final_grade", "submission_page", "page_registration_run", "ai_grade", "omr_calibration_session", "answer_sheet_template"} {
		f.exec(t, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)")
	}
	exam, submission, template := uuid.NewString(), uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	f.exec(t, `INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic OMR reprocess',$2,'active')`, f.tenant, f.tenant)
	f.exec(t, `INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, f.tenant, exam, f.actor)
	f.exec(t, `INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1,$2,$3,'answer_segment',$4,'fixture.png','image/png',1,$5,'fixture',$6::uuid::text,'private',$6::uuid)`, f.crop, f.tenant, exam, f.segment, hash, f.actor)
	f.exec(t, `INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,review_count,auto_confirmed_count,started_by) VALUES($1,$2,$3,'fixture','needs_review',2,1,1,$4)`, f.run, f.tenant, exam, f.actor)
	for i, segment := range []string{f.segment, f.other} {
		question, snapshot, inputID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		pageID := uuid.NewString()
		f.exec(t, `INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,$4,'single_choice',5,1,'draft')`, question, f.tenant, exam, []string{"1", "17"}[i])
		f.exec(t, `INSERT INTO submission_page(id,tenant_id,submission_id,file_asset_id,page_no,status) VALUES($1,$2,$3,$4,$5,'accepted')`, pageID, f.tenant, submission, f.crop, i+1)
		f.exec(t, `INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,crop_file_asset_id,crop_sha256,template_id,template_content_hash) VALUES($1,$2,$3,$4,$5,$6,'{}','manual','accepted',$7,$8,$9,$8)`, segment, f.tenant, submission, pageID, question, []string{"1", "17"}[i], f.crop, hash, template)
		f.exec(t, `INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'choice','[]','R1','{}','{}','{}','{}',$6)`, snapshot, f.tenant, exam, question, uuid.NewString(), hash)
		f.exec(t, `INSERT INTO answer_input_version(id,tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,template_content_hash,answer_scope,members_json,input_hash) VALUES($1,$2,$3,$4,$5,$6,$6,'question','[{}]',$7)`, inputID, f.tenant, segment, submission, f.crop, hash, strings.Repeat([]string{"b", "c"}[i], 64))
		if i == 1 {
			f.exec(t, `INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,stage,processing_status,decision_status,recognition_completed,model_invoked,observation_json,revision) VALUES($1,$2,$3,$4,$5,$6,'image_ai','done','succeeded','suggestion_ready',true,true,'{"saved_observation":true}',7)`, f.tenant, f.run, segment, question, inputID, snapshot)
			continue
		}
		runtime := workerruntime.NewPostgresStore(db)
		task, err := runtime.CreateTask(ctx, f.tenant, f.actor, workerruntime.CreateTaskInput{TaskType: "omr_extract", QueueName: "page-processing", SourceType: "omr_run", SourceID: f.omr, PayloadSchemaVersion: "omr-task-v2", IdempotencyKey: "omr:" + f.omr, Payload: map[string]any{"exam_id": exam, "answer_segment_id": segment, "omr_run_id": f.omr, "profile_hash": "frozen-profile", "option_regions": []map[string]any{{"label": "A", "x": .1}}}, MaxAttempts: 3, RetryBackoffSeconds: 5})
		if err != nil {
			t.Fatal(err)
		}
		f.task = task.ID
		f.exec(t, `UPDATE agent_worker_task SET status='succeeded',completed_at=now() WHERE id=$1::uuid`, task.ID)
		f.exec(t, `INSERT INTO omr_run(id,tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,status,runtime_task_id,decision,confidence,measurements,selected_options,result_version,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$6,'fixture-v1','frozen-profile','completed',$8,'ambiguous',.6,'[{"old":true}]','["A"]','old-result',now())`, f.omr, f.tenant, f.run, segment, f.crop, hash, template, task.ID)
		f.exec(t, `INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,stage,processing_status,decision_status,runtime_task_id,recognition_completed) VALUES($1,$2,$3,$4,$5,$6,'omr','done','succeeded','review_needed',$7,true)`, f.tenant, f.run, segment, question, inputID, snapshot, task.ID)
		f.exec(t, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,source,decision,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1,$2,$3,'omr','ambiguous','fixture','fixture',$4,true,$5)`, f.tenant, segment, f.run, hash, f.actor)
		f.exec(t, `INSERT INTO question_grade(tenant_id,exam_id,submission_id,question_id,answer_segment_id,scoring_run_id,source,status,score,max_score,confirmed_by) VALUES($1,$2,$3,$4,$5,$6,'rule_confirmed','confirmed',0,5,$7)`, f.tenant, exam, submission, question, segment, f.run, f.actor)
		f.exec(t, `INSERT INTO review_task(id,tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by,scoring_run_id) VALUES($1,$2,$3,$4,'1',$5,$6,'synthetic','omr_ambiguous','pending',$7,$8)`, f.review, f.tenant, exam, question, segment, submission, f.actor, f.run)
	}
	return f
}

func (f completedOMRFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresReprocessCompletedOMRPreservesOtherObservationAndReplays(t *testing.T) {
	f := seedCompletedOMRFixture(t)
	created, err := f.store.ReprocessCompletedOMR(f.ctx, f.tenant, f.run, f.omr, f.actor)
	if err != nil || created.ID == f.omr || created.Status != "queued" || created.ScoringRunID != f.run {
		t.Fatalf("rebuild completed OMR: %#v err=%v", created, err)
	}
	var oldStatus, decision, review, item, parent string
	var history, candidates, grades, queued, reviews int
	var recognition bool
	err = f.db.QueryRowContext(f.ctx, `SELECT o.status,o.decision,jsonb_array_length(o.measurements),rt.status,item.processing_status,item.recognition_completed,sr.status,sr.queued_count,sr.review_count,(SELECT count(*) FROM answer_candidate WHERE is_current),(SELECT count(*) FROM question_grade WHERE is_current) FROM omr_run o JOIN scoring_run sr ON sr.id=o.scoring_run_id JOIN review_task rt ON rt.id=$2::uuid JOIN scoring_run_item item ON item.scoring_run_id=sr.id AND item.answer_segment_id=o.answer_segment_id WHERE o.id=$1::uuid`, f.omr, f.review).Scan(&oldStatus, &decision, &history, &review, &item, &recognition, &parent, &queued, &reviews, &candidates, &grades)
	if err != nil || oldStatus != "invalidated" || decision != "ambiguous" || history != 1 || review != "cancelled" || item != "queued" || recognition || parent != "processing" || queued != 1 || reviews != 1 || candidates != 0 || grades != 0 {
		t.Fatalf("rebuild history/current projection: old=%s decision=%s history=%d review=%s item=%s recognized=%v parent=%s queued=%d reviews=%d candidates=%d grades=%d err=%v", oldStatus, decision, history, review, item, recognition, parent, queued, reviews, candidates, grades, err)
	}
	var raw []byte
	var revision int
	if err = f.db.QueryRowContext(f.ctx, `SELECT observation_json,revision FROM scoring_run_item WHERE answer_segment_id=$1::uuid`, f.other).Scan(&raw, &revision); err != nil || revision != 7 || !strings.Contains(string(raw), "saved_observation") {
		t.Fatalf("successful paid observation changed: raw=%s revision=%d err=%v", raw, revision, err)
	}
	var preserved bool
	if err = f.db.QueryRowContext(f.ctx, `SELECT newer.payload-'omr_run_id'=older.payload-'omr_run_id' AND newer.payload->>'omr_run_id'=$3 FROM agent_worker_task newer JOIN agent_worker_task older ON older.id=$2::uuid WHERE newer.id=$1::uuid`, created.RuntimeTaskID, f.task, created.ID).Scan(&preserved); err != nil || !preserved {
		t.Fatalf("frozen worker payload changed: %v", err)
	}
	detail, err := f.store.GetScoringRunDetail(f.ctx, f.tenant, f.run)
	if err != nil || len(detail.Items) != 2 {
		t.Fatalf("history duplicated detail items: count=%d err=%v", len(detail.Items), err)
	}
	for _, detailItem := range detail.Items {
		if detailItem.AnswerSegmentID == f.segment && (detailItem.OMRRunID != created.ID || detailItem.RecognitionDecision != "") {
			t.Fatalf("detail used invalidated OMR/candidate: %#v", detailItem)
		}
	}
	// 新任务必须经过正式领取与完成，旧回调不能重新写回当前候选。
	if _, _, err = f.store.ApplyOMRResult(f.ctx, f.tenant, f.omr, f.actor, OMRResultInput{TaskID: f.task, ResultVersion: "old-result", Decision: "ambiguous", Confidence: .6, ProfileVersion: "fixture-v1", ProfileHash: "frozen-profile", OverlayFileAssetID: uuid.NewString()}, nil); err == nil {
		t.Fatal("invalidated OMR accepted old callback")
	}
	claimed, err := workerruntime.NewPostgresStore(f.db).Claim(f.ctx, f.tenant, workerruntime.ClaimInput{QueueName: "page-processing", WorkerService: "page-processing", WorkerInstanceID: "reprocess-fixture", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].ID != created.RuntimeTaskID {
		t.Fatalf("new OMR cannot be claimed: %#v err=%v", claimed, err)
	}
	completed, _, err := f.store.ApplyOMRResult(f.ctx, f.tenant, created.ID, f.actor, OMRResultInput{TaskID: claimed[0].ID, LeaseToken: claimed[0].LeaseToken, ResultVersion: "new-result", Decision: "ambiguous", Confidence: .8, Selected: []string{}, Measurements: []map[string]any{}, ProfileVersion: "fixture-v1", ProfileHash: "frozen-profile", OverlayFileAssetID: uuid.NewString()}, nil)
	if err != nil || completed.Status != "completed" {
		t.Fatalf("rebuilt OMR cannot complete: %#v err=%v", completed, err)
	}
	// 接受后再出现教师活动，命令重放只返回原回执，不能再作废一次成果。
	f.exec(t, `UPDATE review_task SET status='assigned',assigned_to=$2::uuid WHERE answer_segment_id=$1::uuid AND status='pending'`, f.segment, f.actor)
	replayed, err := f.store.ReprocessCompletedOMR(f.ctx, f.tenant, f.run, f.omr, f.actor)
	var count int
	if queryErr := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM omr_run`).Scan(&count); queryErr != nil || count != 2 || err != nil || replayed.ID != created.ID {
		t.Fatalf("replay duplicated OMR: count=%d replay=%q err=%v query=%v", count, replayed.ID, err, queryErr)
	}
}

func TestPostgresReprocessCompletedOMRRejectsTeacherWorkAndChangedInput(t *testing.T) {
	for _, scenario := range []string{"assigned", "opened", "draft", "human_grade", "final_grade", "other_review_source", "review_submitted", "crop_changed", "cancelled", "wrong_tenant"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedCompletedOMRFixture(t)
			switch scenario {
			case "assigned":
				f.exec(t, `UPDATE review_task SET assigned_to=$2::uuid WHERE id=$1::uuid`, f.review, f.actor)
			case "opened":
				f.exec(t, `UPDATE review_task SET last_opened_at=now() WHERE id=$1::uuid`, f.review)
			case "other_review_source":
				f.exec(t, `UPDATE review_task SET source='manual_sample' WHERE id=$1::uuid`, f.review)
			case "draft":
				f.exec(t, `INSERT INTO review_draft(tenant_id,review_task_id,reviewer_id,comments) VALUES($1,$2,$3,'teacher work')`, f.tenant, f.review, f.actor)
			case "human_grade":
				f.exec(t, `UPDATE question_grade SET source='human'`)
			case "final_grade":
				f.exec(t, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,locked,status,created_by) SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,1,5,'single_review',true,'locked',created_by FROM review_task WHERE id=$1::uuid`, f.review)
			case "review_submitted":
				f.exec(t, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, f.review)
			case "crop_changed":
				f.exec(t, `UPDATE answer_segment SET crop_sha256=repeat('d',64) WHERE id=$1::uuid`, f.segment)
			case "cancelled":
				f.exec(t, `UPDATE scoring_run SET status='cancelled' WHERE id=$1::uuid`, f.run)
			}
			tenant := f.tenant
			if scenario == "wrong_tenant" {
				tenant = uuid.NewString()
			}
			_, err := f.store.ReprocessCompletedOMR(f.ctx, tenant, f.run, f.omr, f.actor)
			if err == nil || !(errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrNotFound)) {
				t.Fatalf("protected work accepted: %v", err)
			}
			var old string
			var count, current int
			if queryErr := f.db.QueryRowContext(f.ctx, `SELECT status,(SELECT count(*) FROM omr_run),(SELECT count(*) FROM answer_candidate WHERE is_current) FROM omr_run WHERE id=$1::uuid`, f.omr).Scan(&old, &count, &current); queryErr != nil || old != "completed" || count != 1 || current != 1 {
				t.Fatalf("rejected command changed facts: old=%s count=%d current=%d err=%v", old, count, current, queryErr)
			}
		})
	}
}

func TestPostgresReprocessCompletedOMRRollsBackTaskFailure(t *testing.T) {
	f := seedCompletedOMRFixture(t)
	f.exec(t, `ALTER TABLE agent_worker_task ADD CONSTRAINT fixture_reject_reprocess CHECK(idempotency_key NOT LIKE 'omr-reprocess:%')`)
	_, err := f.store.ReprocessCompletedOMR(f.ctx, f.tenant, f.run, f.omr, f.actor)
	if err == nil || !strings.Contains(err.Error(), "fixture_reject_reprocess") {
		t.Fatalf("did not reach runtime fault: %v", err)
	}
	var facts []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT jsonb_build_array((SELECT count(*) FROM omr_run),(SELECT count(*) FROM agent_worker_task),(SELECT count(*) FROM answer_candidate WHERE is_current),(SELECT count(*) FROM review_task WHERE status='pending'),(SELECT count(*) FROM question_grade WHERE is_current))`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	var counts []int
	if json.Unmarshal(facts, &counts) != nil || string(facts) != "[1, 1, 1, 1, 1]" {
		t.Fatalf("partial command survived rollback: %s", facts)
	}
}

func TestPostgresReprocessDefaultOMRRefreshesOnlyNewRunEligibilityAndPreservesWorkerInput(t *testing.T) {
	for _, scenario := range []string{"matching locked template", "template unlocked", "template hash changed"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedCompletedOMRFixture(t)
			layout := paper.TemplateLayout{OMRProfile: paper.DefaultTemplateOMRProfile()}
			policy := paper.OMRAutoConfirmPolicyForTemplate(layout, "locked", "template", "template")
			layoutRaw, _ := json.Marshal(layout)
			profileRaw, _ := json.Marshal(policy.RuntimeProfile)
			f.exec(t, `INSERT INTO answer_sheet_template(id,tenant_id,exam_id,exam_paper_id,version_no,name,status,page_count,layout,content_hash,created_by)
 SELECT o.template_id,o.tenant_id,run.exam_id,$3::uuid,1,'Synthetic default OMR','locked',1,$4::jsonb,o.template_content_hash,$5::uuid FROM omr_run o JOIN scoring_run run ON run.tenant_id=o.tenant_id AND run.id=o.scoring_run_id WHERE o.tenant_id=$1::uuid AND o.id=$2::uuid`, f.tenant, f.omr, uuid.NewString(), layoutRaw, f.actor)
			f.exec(t, `UPDATE omr_run SET profile_version=$2,profile_hash=$3,auto_confirm_eligible=false,auto_confirm_reason=$4,auto_confirm_min_confidence=.9 WHERE id=$1::uuid`, f.omr, paper.OMRProfileVersionOpenCVFillV1, policy.ProfileHash, paper.OMRAutoConfirmReasonManualOnlyProfile)
			f.exec(t, `UPDATE agent_worker_task SET payload=payload || jsonb_build_object('profile_hash',$2::text,'profile',$3::jsonb) WHERE id=$1::uuid`, f.task, policy.ProfileHash, profileRaw)
			if scenario == "template unlocked" {
				f.exec(t, `UPDATE answer_sheet_template SET status='draft'`)
			} else if scenario == "template hash changed" {
				f.exec(t, `UPDATE answer_sheet_template SET content_hash=repeat('d',64)`)
			}
			created, err := f.store.ReprocessCompletedOMR(f.ctx, f.tenant, f.run, f.omr, f.actor)
			if err != nil {
				t.Fatal(err)
			}
			wantEligible := scenario == "matching locked template"
			wantMin, wantReason := .9, paper.OMRAutoConfirmReasonManualOnlyProfile
			if wantEligible {
				wantMin, wantReason = paper.DefaultOMRAutoConfirmMinimumConfidence, paper.OMRAutoConfirmReasonLockedTemplateOMR
			}
			if created.AutoConfirmEligible != wantEligible || created.AutoConfirmMinConfidence != wantMin || created.AutoConfirmReason != wantReason {
				t.Fatalf("new-run policy did not match current immutable template: %#v", created)
			}
			// 新资格不回写历史，也不改变选项坐标、裁图、参考或被冻结的 Worker 载荷。
			var preserved bool
			if err := f.db.QueryRowContext(f.ctx, `SELECT NOT old.auto_confirm_eligible AND old.auto_confirm_min_confidence=.9 AND old.auto_confirm_reason=$4
 AND old.status='invalidated' AND old.decision='ambiguous' AND old.confidence=.6
 AND fresh.profile_hash=old.profile_hash AND fresh.crop_sha256=old.crop_sha256 AND fresh.template_content_hash=old.template_content_hash
 AND newer.payload-'omr_run_id'=older.payload-'omr_run_id'
 FROM omr_run old JOIN omr_run fresh ON fresh.id=$2::uuid JOIN agent_worker_task newer ON newer.id=fresh.runtime_task_id JOIN agent_worker_task older ON older.id=$3::uuid WHERE old.id=$1::uuid`, f.omr, created.ID, f.task, paper.OMRAutoConfirmReasonManualOnlyProfile).Scan(&preserved); err != nil || !preserved {
				t.Fatalf("reprocess changed immutable history or worker inputs: preserved=%v err=%v", preserved, err)
			}
		})
	}
}

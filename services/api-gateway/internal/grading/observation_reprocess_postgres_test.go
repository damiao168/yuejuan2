package grading

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

type completedObservationFixture struct {
	completedOMRFixture
	item, input, question, rule string
	oldTask                     workerruntime.Task
	oldInput                    ScoringObservationInput
	oldRaw                      json.RawMessage
}

func seedCompletedObservationFixture(t *testing.T) completedObservationFixture {
	t.Helper()
	f := completedObservationFixture{completedOMRFixture: seedCompletedOMRFixture(t), input: uuid.NewString(), rule: uuid.NewString()}
	var exam, snapshot, submission string
	if err := f.db.QueryRowContext(f.ctx, `SELECT item.id::text,item.question_id::text,item.assessment_snapshot_id::text,sr.exam_id::text,seg.submission_id::text FROM scoring_run_item item JOIN scoring_run sr ON sr.id=item.scoring_run_id JOIN answer_segment seg ON seg.id=item.answer_segment_id WHERE item.answer_segment_id=$1::uuid`, f.segment).Scan(&f.item, &f.question, &snapshot, &exam, &submission); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE question SET question_type='fill_blank' WHERE id=$1::uuid`, f.question)
	f.exec(t, `UPDATE exam_question_snapshot SET scoring_policy_snapshot_json='{"mode":"RULE_AUTO"}' WHERE id=$1::uuid`, snapshot)
	_, ruleHash, err := marshalRule("fill_blank", map[string]any{"answer": "1/2", "max_score": 5.0})
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO question_answer_key(tenant_id,question_id,standard_answer,equivalent_answers,tolerance,answer_version,created_by) VALUES($1,$2,'"1/2"','[]','{}','fixture-v1',$3)`, f.tenant, f.question, f.actor)
	f.exec(t, `INSERT INTO scoring_rule(id,tenant_id,exam_id,question_id,version,rule_type,config,status,content_hash,created_by,published_by,published_at) VALUES($1,$2,$3,$4,1,'fill_blank','{"answer":"1/2","max_score":5}','published',$5,$6,$6,now())`, f.rule, f.tenant, exam, f.question, ruleHash, f.actor)
	members := []ObservationMember{{SegmentID: f.segment, QuestionID: f.question, QuestionNo: "14", QuestionType: "fill_blank"}}
	membersRaw, _ := json.Marshal(members)
	f.exec(t, `INSERT INTO answer_input_version(id,tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,template_content_hash,answer_scope,members_json,input_hash) VALUES($1,$2,$3,$4,$5,repeat('a',64),repeat('a',64),'question',$6::jsonb,repeat('d',64))`, f.input, f.tenant, f.segment, submission, f.crop, membersRaw)
	runtime := workerruntime.NewPostgresStore(f.db)
	f.oldTask, err = runtime.CreateTask(f.ctx, f.tenant, f.actor, workerruntime.CreateTaskInput{TaskType: "ocr", QueueName: scoringCoordinatorQueue, SourceType: "answer_input_version", SourceID: f.input, PayloadSchemaVersion: "scoring-observation-task-v1", Payload: map[string]any{"scoring_run_id": f.run, "input_version_id": f.input}, IdempotencyKey: "fixture-observation:" + f.input, MaxAttempts: 3, RetryBackoffSeconds: 30})
	if err != nil {
		t.Fatal(err)
	}
	f.oldInput = ScoringObservationInput{RequestID: f.oldTask.ID, ScoringRunID: f.run, InputVersionID: f.input, SegmentID: f.segment, CropSHA256: strings.Repeat("a", 64), Members: members}
	images := &coordinatorFixtureImages{db: f.db, runtime: runtime}
	f.oldRaw, err = images.Observe(f.ctx, f.tenant, f.oldInput)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(f.oldRaw, &result); err != nil {
		t.Fatal(err)
	}
	resultHash, _ := workerruntime.ResultPayloadHash("scoring-observation-result-v1", result)
	f.oldTask.LeaseToken = "fixture-old-lease"
	f.exec(t, `UPDATE agent_worker_task SET status='succeeded',lease_token=$2,result=$3::jsonb,result_schema_version='scoring-observation-result-v1',result_payload_hash=$4,completed_at=now() WHERE id=$1::uuid`, f.oldTask.ID, f.oldTask.LeaseToken, []byte(f.oldRaw), resultHash)
	f.exec(t, `UPDATE omr_run SET status='invalidated' WHERE id=$1::uuid`, f.omr)
	f.exec(t, `UPDATE answer_segment SET processing_status='completed' WHERE id=$1::uuid`, f.segment)
	f.exec(t, `UPDATE scoring_run_item SET input_version_id=$2::uuid,frozen_rule_id=$3::uuid,route='observe_rule',observation_task_id=$4::uuid,runtime_task_id=$4::uuid,observation_json=$5::jsonb,decision_status='auto_confirmed',model_invoked=true WHERE id=$1::uuid`, f.item, f.input, f.rule, f.oldTask.ID, []byte(f.oldRaw))
	f.exec(t, `UPDATE answer_candidate SET source='ocr',input_version_id=$2::uuid,decision='confirmed',display_text='old OCR',presence='present',association_status='matched' WHERE answer_segment_id=$1::uuid`, f.segment, f.input)
	f.exec(t, `INSERT INTO answer_segment_answer(tenant_id,answer_segment_id,answer_text,answer_payload,source,confidence,recorded_by) VALUES($1,$2,'old OCR',jsonb_build_object('observation_task_id',$3::text),'ocr_text',.98,$4)`, f.tenant, f.segment, f.oldTask.ID, f.actor)
	f.exec(t, `UPDATE review_task SET source='rule_review_required' WHERE id=$1::uuid`, f.review)
	f.exec(t, `INSERT INTO review_task(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,created_by,scoring_run_id) SELECT tenant_id,$2::uuid,question_id,'17',id,submission_id,'other','subjective_default_review','pending',$3::uuid,$4::uuid FROM answer_segment WHERE id=$1::uuid`, f.other, exam, f.actor, f.run)
	tx, err := f.db.BeginTx(f.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = freezeScoringRunRulesTx(f.ctx, tx, f.tenant, exam, f.run); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return f
}

// 使用隔离 schema 的真实事务、runtime 和规则确认；观察 transport 保持离线，不验证模型识别准确性。
func TestPostgresReprocessCompletedObservationPreservesPlanAndCompletes(t *testing.T) {
	f := seedCompletedObservationFixture(t)
	var otherBefore, otherAfter []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT jsonb_build_array(to_jsonb(item),(SELECT jsonb_agg(to_jsonb(rt)) FROM review_task rt WHERE rt.answer_segment_id=item.answer_segment_id)) FROM scoring_run_item item WHERE item.answer_segment_id=$1::uuid`, f.other).Scan(&otherBefore); err != nil {
		t.Fatal(err)
	}
	created, err := f.store.ReprocessCompletedObservation(f.ctx, f.tenant, f.run, f.item, f.oldTask.ID, f.actor)
	if err != nil || created.ID == f.oldTask.ID || created.Status != "queued" || created.SourceID != f.input {
		t.Fatalf("reprocess observation: %#v err=%v", created, err)
	}
	var queued, reviews, total, runs, grades, candidates, answers int
	var status string
	if err = f.db.QueryRowContext(f.ctx, `SELECT status,total_count,queued_count,review_count,(SELECT count(*) FROM scoring_run),(SELECT count(*) FROM question_grade WHERE is_current),(SELECT count(*) FROM answer_candidate WHERE is_current),(SELECT count(*) FROM answer_segment_answer WHERE deleted_at IS NULL) FROM scoring_run WHERE id=$1::uuid`, f.run).Scan(&status, &total, &queued, &reviews, &runs, &grades, &candidates, &answers); err != nil || status != "processing" || total != 2 || queued != 1 || reviews != 1 || runs != 1 || grades != 0 || candidates != 0 || answers != 0 {
		t.Fatalf("replacement facts status=%s total=%d queued=%d reviews=%d runs=%d grades=%d candidates=%d answers=%d err=%v", status, total, queued, reviews, runs, grades, candidates, answers, err)
	}
	// 已缓存的旧输入也只能重放旧任务回执，当前 item 的 task ID 已切换，不能写回旧观察。
	if err = f.store.completeScoringObservation(f.ctx, f.oldTask, f.oldInput, f.run, f.actor, f.oldRaw); err != nil {
		t.Fatal(err)
	}
	var untouched bool
	if err = f.db.QueryRowContext(f.ctx, `SELECT observation_json IS NULL AND NOT recognition_completed AND processing_status='queued' AND observation_task_id=$2::uuid FROM scoring_run_item WHERE id=$1::uuid`, f.item, created.ID).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("late observation changed replacement: untouched=%v err=%v", untouched, err)
	}
	if _, _, _, err = f.store.loadScoringObservationInput(f.ctx, f.oldTask); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old task still loads current input: %v", err)
	}
	runtime := workerruntime.NewPostgresStore(f.db)
	images := &coordinatorFixtureImages{db: f.db, runtime: runtime}
	if err = NewScoringCoordinator(f.store, runtime, images, time.Minute).RunOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	var score float64
	if err = f.db.QueryRowContext(f.ctx, `SELECT grade.score::float8,item.processing_status,item.decision_status FROM scoring_run_item item JOIN question_grade grade ON grade.answer_segment_id=item.answer_segment_id AND grade.is_current WHERE item.id=$1::uuid`, f.item).Scan(&score, &status, &created.Status); err != nil || score != 5 || status != "succeeded" || created.Status != "auto_confirmed" || images.observations != 1 || images.schedules != 0 {
		t.Fatalf("official completion score=%v processing=%s decision=%s observations=%d schedules=%d err=%v", score, status, created.Status, images.observations, images.schedules, err)
	}
	if err = f.db.QueryRowContext(f.ctx, `SELECT jsonb_build_array(to_jsonb(item),(SELECT jsonb_agg(to_jsonb(rt)) FROM review_task rt WHERE rt.answer_segment_id=item.answer_segment_id)) FROM scoring_run_item item WHERE item.answer_segment_id=$1::uuid`, f.other).Scan(&otherAfter); err != nil || string(otherBefore) != string(otherAfter) {
		t.Fatalf("other plan/review changed: before=%s after=%s err=%v", otherBefore, otherAfter, err)
	}
	// 接受后出现人工工作也只能重放同一任务；后来的成绩和候选不能被第二次失效。
	f.exec(t, `INSERT INTO review_draft(tenant_id,review_task_id,reviewer_id,comments) VALUES($1,$2,$3,'later teacher work')`, f.tenant, f.review, f.actor)
	replayed, err := f.store.ReprocessCompletedObservation(f.ctx, f.tenant, f.run, f.item, f.oldTask.ID, f.actor)
	if err != nil || replayed.ID != created.ID || replayed.Status != "succeeded" {
		t.Fatalf("replay created another task: %#v err=%v", replayed, err)
	}
	var history, current int
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*),count(*) FILTER(WHERE is_current AND deleted_at IS NULL) FROM answer_candidate`).Scan(&history, &current); err != nil || history != 2 || current != 1 {
		t.Fatalf("candidate history/current=%d/%d err=%v", history, current, err)
	}
}

func TestPostgresReprocessCompletedObservationRejectsProtectedOrChangedInput(t *testing.T) {
	for _, scenario := range []string{"assigned", "opened", "draft", "manual_answer", "human_grade", "final_grade", "review_submitted", "other_review_source", "crop_changed", "snapshot_changed", "shared_input", "task_not_succeeded", "wrong_task", "wrong_tenant", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			f := seedCompletedObservationFixture(t)
			switch scenario {
			case "assigned":
				f.exec(t, `UPDATE review_task SET assigned_to=$2::uuid WHERE id=$1::uuid`, f.review, f.actor)
			case "opened":
				f.exec(t, `UPDATE review_task SET last_opened_at=now() WHERE id=$1::uuid`, f.review)
			case "draft":
				f.exec(t, `INSERT INTO review_draft(tenant_id,review_task_id,reviewer_id,comments) VALUES($1,$2,$3,'teacher work')`, f.tenant, f.review, f.actor)
			case "manual_answer":
				f.exec(t, `UPDATE answer_segment_answer SET source='manual_entry'`)
			case "human_grade":
				f.exec(t, `UPDATE question_grade SET source='human'`)
			case "final_grade":
				f.exec(t, `INSERT INTO final_grade(tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,score,max_score,source,locked,status,created_by) SELECT tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,1,5,'single_review',true,'locked',created_by FROM review_task WHERE id=$1::uuid`, f.review)
			case "review_submitted":
				f.exec(t, `UPDATE review_task SET status='submitted' WHERE id=$1::uuid`, f.review)
			case "other_review_source":
				f.exec(t, `UPDATE review_task SET source='manual_sample' WHERE id=$1::uuid`, f.review)
			case "crop_changed":
				f.exec(t, `UPDATE answer_segment SET crop_sha256=repeat('e',64) WHERE id=$1::uuid`, f.segment)
			case "snapshot_changed":
				f.exec(t, `UPDATE scoring_run_item SET assessment_snapshot_id=(SELECT assessment_snapshot_id FROM scoring_run_item WHERE answer_segment_id=$2::uuid) WHERE id=$1::uuid`, f.item, f.other)
			case "shared_input":
				f.exec(t, `UPDATE scoring_run_item SET input_version_id=$2::uuid WHERE answer_segment_id=$1::uuid`, f.other, f.input)
			case "task_not_succeeded":
				f.exec(t, `UPDATE agent_worker_task SET status='queued' WHERE id=$1::uuid`, f.oldTask.ID)
			case "cancelled":
				f.exec(t, `UPDATE scoring_run SET status='cancelled' WHERE id=$1::uuid`, f.run)
			}
			tenant, oldTask := f.tenant, f.oldTask.ID
			if scenario == "wrong_tenant" {
				tenant = uuid.NewString()
			}
			if scenario == "wrong_task" {
				oldTask = uuid.NewString()
			}
			_, err := f.store.ReprocessCompletedObservation(f.ctx, tenant, f.run, f.item, oldTask, f.actor)
			if err == nil || !(errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrNotFound)) {
				t.Fatalf("protected input accepted: %v", err)
			}
			var current, tasks int
			if err = f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM answer_candidate WHERE is_current AND deleted_at IS NULL),(SELECT count(*) FROM agent_worker_task WHERE idempotency_key LIKE 'observation-reprocess:%')`).Scan(&current, &tasks); err != nil || current != 1 || tasks != 0 {
				t.Fatalf("rejection changed facts: current=%d tasks=%d err=%v", current, tasks, err)
			}
		})
	}
}

func TestPostgresReprocessCompletedObservationRollsBackTaskFailure(t *testing.T) {
	f := seedCompletedObservationFixture(t)
	f.exec(t, `ALTER TABLE agent_worker_task ADD CONSTRAINT fixture_reject_observation_reprocess CHECK(idempotency_key NOT LIKE 'observation-reprocess:%')`)
	_, err := f.store.ReprocessCompletedObservation(f.ctx, f.tenant, f.run, f.item, f.oldTask.ID, f.actor)
	if err == nil || !strings.Contains(err.Error(), "fixture_reject_observation_reprocess") {
		t.Fatalf("did not reach runtime fault: %v", err)
	}
	var intact bool
	if err = f.db.QueryRowContext(f.ctx, `SELECT item.observation_task_id=$2::uuid AND item.observation_json IS NOT NULL AND EXISTS(SELECT 1 FROM answer_candidate WHERE is_current AND deleted_at IS NULL) AND EXISTS(SELECT 1 FROM question_grade WHERE is_current) AND EXISTS(SELECT 1 FROM review_task WHERE id=$3::uuid AND status='pending') FROM scoring_run_item item WHERE item.id=$1::uuid`, f.item, f.oldTask.ID, f.review).Scan(&intact); err != nil || !intact {
		t.Fatalf("failed command left partial changes: intact=%v err=%v", intact, err)
	}
}

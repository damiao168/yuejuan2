package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

type coordinatorFixtureImages struct {
	db                      *sql.DB
	runtime                 workerruntime.Store
	observations, schedules int
}

func (f *coordinatorFixtureImages) Observe(_ context.Context, _ string, input ScoringObservationInput) (json.RawMessage, error) {
	f.observations++
	members := []scoringObservedMember{}
	evidence := []map[string]any{}
	for _, member := range input.Members {
		observed := scoringObservedMember{SegmentID: member.SegmentID, QuestionID: member.QuestionID, Presence: "present", Association: "matched", Confidence: .98, EvidenceIDs: []string{"fixture-" + member.QuestionNo}, Answer: &scoringObservedAnswer{Type: "text", Value: "1/2"}}
		if member.QuestionNo == "13" {
			observed.Presence = "uncertain"
			observed.Association = "unresolved"
			observed.Answer = nil
			observed.EvidenceIDs = nil
		}
		members = append(members, observed)
		if len(observed.EvidenceIDs) > 0 {
			evidence = append(evidence, map[string]any{"id": observed.EvidenceIDs[0], "question_id": member.QuestionID, "crop_sha256": input.CropSHA256})
		}
	}
	raw, err := json.Marshal(map[string]any{"input_version_id": input.InputVersionID, "members": members, "observations": evidence, "student_observation": map[string]any{"fixture": true}})
	return raw, err
}
func (f *coordinatorFixtureImages) Schedule(ctx context.Context, tenantID, actorID string, input ScoringAIInput) (string, string, error) {
	f.schedules++
	runID := uuid.NewString()
	_, err := f.db.ExecContext(ctx, `INSERT INTO subjective_grading_run(id,tenant_id,answer_segment_id,answer_version,question_id,rubric_version,model_version,prompt_version,request_id,status) SELECT $1::uuid,$2::uuid,seg.id,$4,seg.question_id,'fixture-v1','fixture-v1','fixture-v1',$5,'queued' FROM answer_segment seg WHERE seg.tenant_id=$2::uuid AND seg.id=$3::uuid`, runID, tenantID, input.SegmentID, input.InputVersionID, input.IdempotencyKey)
	if err != nil {
		return "", "", err
	}
	task, err := f.runtime.CreateTask(ctx, tenantID, actorID, workerruntime.CreateTaskInput{TaskType: "ai_grade", QueueName: "subjective-grading", SourceType: "subjective_grading_run", SourceID: runID, PayloadSchemaVersion: "fixture-v1", Payload: map[string]any{}, IdempotencyKey: input.IdempotencyKey})
	return runID, task.ID, err
}

// 在独立 schema 执行正式 203 与生产启动的计划/排队事务，再使用真实 runtime 和规则成绩写入。
// 观察/AI transport 是离线夹具；此测试不证明真实模型准确性，也不替代纸卷准备度确认的独立验收。
func TestPostgresDurableScoringCoordinatorStartObserveResume(t *testing.T) {
	ctx, db := newDurableScoringFixture(t)
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	tenant, actor, exam, submission, crop, otherCrop := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	runID := uuid.NewString()
	hash := strings.Repeat("a", 64)
	templateID := uuid.NewString()
	mustExec(`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1,$1,'Synthetic coordinator',$2,'active')`, tenant, "coordinator-"+tenant)
	// 冻结 AI 方案会先核对考试存在；使用隔离 schema 中的考试，不能依赖 public 的业务数据。
	mustExec(`INSERT INTO exam(id,tenant_id,school_id,name,subject,exam_type,total_score,status,grading_mode,publish_policy,created_by) VALUES($1,$2,$3,'Synthetic coordinator','math','quiz',20,'draft','ai_assisted','manual_after_confirmation',$4)`, exam, tenant, uuid.NewString(), actor)
	mustExec(`INSERT INTO submission(id,tenant_id,exam_id,candidate_no,source_type,status,collected_by) VALUES($1,$2,$3,'synthetic','manual_import','created',$4)`, submission, tenant, exam, actor)
	for _, assetID := range []string{crop, otherCrop} {
		mustExec(`INSERT INTO file_asset(id,tenant_id,exam_id,owner_type,owner_id,original_name,content_type,size_bytes,hash_sha256,storage_bucket,storage_key,visibility,uploaded_by) VALUES($1::uuid,$2,$3,'answer_segment',$1::uuid,'fixture.png','image/png',1,$4,'fixture',$1::uuid::text,'private',$5)`, assetID, tenant, exam, hash, actor)
	}
	segments := []scoringRunSegment{}
	for _, no := range []string{"12", "13", "14", "15"} {
		questionID, segmentID, snapshotID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		kind, mode, scope, assetID := "fill_blank", "RULE_AUTO", "grading_group", crop
		if no == "15" {
			kind = "calculation"
			mode = "AI_ASSIST"
			scope = "question"
			assetID = otherCrop
		}
		mustExec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status,answer_area) VALUES($1,$2,$3,$4,$5,5,1,'draft','{}')`, questionID, tenant, exam, no, kind)
		mustExec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,normalized_bbox,source,status,processing_status,crop_file_asset_id,crop_sha256,template_id,template_content_hash) VALUES($1,$2,$3,$4,$5,$6,'{"x":0,"y":0,"width":1,"height":1}',jsonb_build_object('answer_scope',$7::text),'manual','accepted','completed',$8,$9,$10,$9)`, segmentID, tenant, submission, uuid.NewString(), questionID, no, scope, assetID, hash, templateID)
		mustExec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'exact_text','[]','R1','{}','{}','{}',jsonb_build_object('mode',$6::text),$7)`, snapshotID, tenant, exam, questionID, uuid.NewString(), mode, hash)
		ruleID := ""
		if mode == "RULE_AUTO" {
			ruleID = uuid.NewString()
			// 冻结比较会校验真实配置摘要；输入哈希不能冒充规则内容哈希。
			_, ruleHash, err := marshalRule("fill_blank", map[string]any{"answer": "1/2", "max_score": 5.0})
			if err != nil {
				t.Fatal(err)
			}
			mustExec(`INSERT INTO question_answer_key(tenant_id,question_id,standard_answer,equivalent_answers,tolerance,answer_version,created_by) VALUES($1,$2,'"1/2"','[]','{}','fixture-v1',$3)`, tenant, questionID, actor)
			mustExec(`INSERT INTO scoring_rule(id,tenant_id,exam_id,question_id,version,rule_type,config,status,content_hash,created_by,published_by,published_at) VALUES($1,$2,$3,$4,1,'fill_blank','{"answer":"1/2","max_score":5}','published',$5,$6,$6,now())`, ruleID, tenant, exam, questionID, ruleHash, actor)
		}
		area, _ := json.Marshal(map[string]any{"answer_scope": scope})
		segments = append(segments, scoringRunSegment{id: segmentID, submissionID: submission, questionID: questionID, no: no, kind: kind, cropID: assetID, cropHash: hash, templateID: templateID, templateHash: hash, ruleID: ruleID, scoringMode: mode, area: area, anonymous: "synthetic"})
	}
	mustExec(`INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,total_count,queued_count,started_by) VALUES($1,$2,$3,'fixture','processing',4,4,$4)`, runID, tenant, exam, actor)
	store, runtime := NewPostgresStore(db), workerruntime.NewPostgresStore(db)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = freezeScoringRunRulesTx(ctx, tx, tenant, exam, runID); err != nil {
		t.Fatal(err)
	}
	segments, err = prepareScoringPlanTx(ctx, tx, tenant, runID, segments)
	if err != nil {
		t.Fatal(err)
	}
	queued, review, err := store.createScoringRunTasksTx(ctx, tx, tenant, exam, runID, actor, segments)
	if err != nil || queued != 4 || review != 0 {
		t.Fatalf("start queued=%d review=%d err=%v", queued, review, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	images := &coordinatorFixtureImages{db: db, runtime: runtime}
	coordinator := NewScoringCoordinator(store, runtime, images, time.Minute)
	for range 3 {
		if err = coordinator.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if images.observations != 2 || images.schedules != 1 {
		t.Fatalf("shared/recovery dispatch observations=%d schedules=%d", images.observations, images.schedules)
	}
	var answers, grades, reviews, items, inputs int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM answer_segment_answer),(SELECT count(*) FROM question_grade),(SELECT count(*) FROM review_task),(SELECT count(*) FROM scoring_run_item),(SELECT count(*) FROM answer_input_version)`).Scan(&answers, &grades, &reviews, &items, &inputs); err != nil {
		t.Fatal(err)
	}
	if answers != 2 || grades != 2 || reviews != 1 || items != 4 || inputs != 2 {
		t.Fatalf("facts answers=%d grades=%d reviews=%d items=%d inputs=%d", answers, grades, reviews, items, inputs)
	}
	if _, _, _, err = store.ResumeScoringRun(ctx, tenant, runID, false); err != nil {
		t.Fatal(err)
	}
	if err = coordinator.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if images.observations != 2 || images.schedules != 1 {
		t.Fatal("resume repeated successful external work")
	}
	var finalGrades int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM question_grade WHERE source='human'`).Scan(&finalGrades); err != nil || finalGrades != 0 {
		t.Fatal("AI suggestion became teacher grade", err)
	}
}

func newDurableScoringFixture(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "durable_scoring_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	db := stdlib.OpenDB(*cfg)
	db.SetMaxOpenConns(4)
	t.Cleanup(func() {
		db.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	for _, table := range []string{"tenant", "exam", "question", "question_answer_key", "question_rubric", "rubric_version", "submission", "answer_segment", "file_asset", "exam_question_snapshot", "scoring_rule", "scoring_run", "omr_run", "answer_candidate", "answer_segment_answer", "review_task", "review_draft", "question_grade", "arbitration_task", "double_mark_session", "subjective_grading_run", "agent_worker_task", "agent_worker_task_attempt", "agent_worker_heartbeat"} {
		if _, err = db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.ExecContext(ctx, `ALTER TABLE answer_candidate DROP COLUMN IF EXISTS input_version_id,DROP COLUMN IF EXISTS presence,DROP COLUMN IF EXISTS association_status`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000203_durable_scoring_plan.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(fmt.Errorf("migration 203: %w", err))
	}
	return ctx, db
}

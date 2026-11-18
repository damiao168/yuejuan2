package subjective

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// 隔离 schema 复制真实列类型和约束，直接走正式入队事务；不复制业务数据，也不调用模型。
// LIKE 不复制外键、触发器及 RLS，此处验证参数类型、幂等绑定和事务回滚。
func newImageSchedulePostgresFixture(t *testing.T) (context.Context, *PostgresStore, string, string, grading.ScoringAIInput, Context) {
	t.Helper()
	dsn := os.Getenv("YUEJUAN_BINDING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("YUEJUAN_BINDING_TEST_DATABASE_URL is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	admin := stdlib.OpenDB(*cfg)
	schema := "image_schedule_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema + ",public"
	database := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		database.Close()
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"scoring_run", "scoring_run_item", "answer_segment", "answer_input_version", "subjective_grading_run", "agent_worker_task"} {
		mustExec("CREATE TABLE " + table + " (LIKE public." + table + " INCLUDING ALL)")
	}
	tenant, actor, runID, segment, inputID, snapshot := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	question, submission, crop := uuid.NewString(), uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	mustExec(`INSERT INTO scoring_run(id,tenant_id,exam_id,idempotency_key,status,started_by) VALUES($1,$2,$3,'fixture','processing',$4)`, runID, tenant, uuid.NewString(), actor)
	mustExec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,crop_file_asset_id,crop_sha256) VALUES($1,$2,$3,$4,$5,'17','{}','manual','accepted',$6,$7)`, segment, tenant, submission, uuid.NewString(), question, crop, hash)
	mustExec(`INSERT INTO answer_input_version(id,tenant_id,answer_segment_id,submission_id,crop_file_asset_id,crop_sha256,template_content_hash,answer_scope,members_json,input_hash) VALUES($1,$2,$3,$4,$5,$6,$6,'question','[{}]',$6)`, inputID, tenant, segment, submission, crop, hash)
	observation := []byte(`{"fixture":true}`)
	mustExec(`INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,observation_json) VALUES($1,$2,$3,$4,$5,$6,'image_ai',$7::jsonb)`, tenant, runID, segment, question, inputID, snapshot, string(observation))
	value := serviceTestContext()
	value.SegmentID, value.Question.ID, value.AssessmentSnapshot.ID = segment, question, snapshot
	value.InputVersionID, value.AnswerVersion, value.CropSHA256 = inputID, inputID, hash
	input := grading.ScoringAIInput{ScoringRunID: runID, SegmentID: segment, InputVersionID: inputID, CropSHA256: hash, AssessmentSnapshotID: snapshot, IdempotencyKey: "fixture-" + uuid.NewString(), Observation: observation}
	return ctx, NewPostgresStore(database), tenant, actor, input, value
}

func TestPostgresScheduleScoringItemSharesSingleConnection(t *testing.T) {
	ctx, store, tenant, actor, input, value := newImageSchedulePostgresFixture(t)
	model := coordinatorModel()
	plan := coordinatorPlan(model)
	value.Rubric.QuestionID = value.Question.ID
	value.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringAIAssist
	frozen := map[string]any{"question": value.Question, "rubric": value.Rubric, "assessment_snapshot": value.AssessmentSnapshot}
	if _, err := store.db.ExecContext(ctx, `UPDATE scoring_run SET rule_snapshot=$3::jsonb WHERE tenant_id=$1 AND id=$2`, tenant, input.ScoringRunID,
		string(imageJSON(t, map[string]any{"ai_plan": plan, "questions": map[string]any{value.Question.ID: frozen}}))); err != nil {
		t.Fatal(err)
	}
	observation := imageTestObservation(value.Question.ID, value.CropSHA256)
	observation.InputVersionID, observation.ModelBinding = input.InputVersionID, imageModelBinding(model)
	input.Observation = imageJSON(t, map[string]any{"student_observation": observation})
	if _, err := store.db.ExecContext(ctx, `UPDATE scoring_run_item SET observation_json=$3::jsonb WHERE tenant_id=$1 AND scoring_run_id=$2`, tenant, input.ScoringRunID, string(input.Observation)); err != nil {
		t.Fatal(err)
	}
	store.db.SetMaxOpenConns(1)
	// 使用正式处理器读取快照和写入任务。模型解析是替身，但仍从同一连接池读取该租户的配置来源。
	handler := coordinatorHandler(t, store.db, "http://127.0.0.1:1", model).WithImageModelByIDResolver(func(ctx context.Context, scope, modelID, revision string) (*ImageModelConfig, error) {
		if scope != tenant || modelID != model.ConfigID || revision != plan.SingleModel.BindingRevision {
			return nil, errors.New("model binding escaped the frozen plan")
		}
		var exists bool
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scoring_run WHERE tenant_id=$1 AND id=$2)`, scope, input.ScoringRunID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, errors.New("missing model scope")
		}
		return model, nil
	})
	scheduleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	before := store.db.Stats().WaitCount
	run, task, err := handler.ScheduleScoringItem(scheduleCtx, tenant, actor, input)
	if err != nil || run == "" || task == "" || store.db.Stats().WaitCount != before {
		t.Fatalf("single-pool enqueue: run=%q task=%q err=%v", run, task, err)
	}
	var bound bool
	err = store.db.QueryRowContext(ctx, `SELECT EXISTS(
 SELECT 1 FROM scoring_run_item item
 JOIN subjective_grading_run r ON r.tenant_id=item.tenant_id AND r.id=item.subjective_run_id
 JOIN agent_worker_task wt ON wt.tenant_id=item.tenant_id AND wt.id=item.runtime_task_id AND wt.source_id=r.id
 WHERE item.tenant_id=$1::uuid AND item.scoring_run_id=$2::uuid AND r.id=$3::uuid AND wt.id=$4::uuid
 AND r.input_version_id=item.input_version_id AND wt.status='queued' AND item.processing_status='queued')`, tenant, input.ScoringRunID, run, task).Scan(&bound)
	if err != nil || !bound {
		t.Fatalf("handler did not atomically bind run and task: bound=%v err=%v", bound, err)
	}
}

func TestPostgresScheduleImageRunTypedInputAndReplay(t *testing.T) {
	ctx, store, tenant, actor, input, value := newImageSchedulePostgresFixture(t)
	policy := ModelPolicy{ModelVersion: "fixture-model", PromptVersion: "fixture-prompt", MinConfidence: .8}
	runID, taskID, err := store.scheduleImageRun(ctx, tenant, actor, input, value, policy)
	if err != nil {
		t.Fatalf("schedule image input into text and uuid columns: %v", err)
	}
	var answer, version, mode, protocol, source, status string
	var bound bool
	err = store.db.QueryRowContext(ctx, `SELECT ai.answer_version,ai.input_version_id::text,ai.input_mode,ai.input_protocol,wt.source_id::text,item.processing_status,item.subjective_run_id=ai.id AND item.runtime_task_id=wt.id FROM subjective_grading_run ai JOIN agent_worker_task wt ON wt.source_id=ai.id JOIN scoring_run_item item ON item.subjective_run_id=ai.id WHERE ai.id=$1::uuid AND wt.id=$2::uuid`, runID, taskID).Scan(&answer, &version, &mode, &protocol, &source, &status, &bound)
	if err != nil || answer != input.InputVersionID || version != input.InputVersionID || mode != "image" || protocol != ImageProtocolVersion || source != runID || status != "queued" || !bound {
		t.Fatalf("image/runtime identity lost: answer=%q version=%q mode=%q protocol=%q source=%q status=%q bound=%v err=%v", answer, version, mode, protocol, source, status, bound, err)
	}
	replayedRun, replayedTask, err := store.scheduleImageRun(ctx, tenant, actor, input, value, policy)
	if err != nil || replayedRun != runID || replayedTask != taskID {
		t.Fatalf("same input repeated paid work: run=%q task=%q err=%v", replayedRun, replayedTask, err)
	}
	// 同一请求不能悄悄改用新冻结上下文；拒绝后仍只保留原任务。
	value.AnswerText = "changed frozen context"
	if _, _, err = store.scheduleImageRun(ctx, tenant, actor, input, value, policy); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed context accepted on replay: %v", err)
	}
	var runs, tasks int
	if err = store.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM subjective_grading_run),(SELECT count(*) FROM agent_worker_task)`).Scan(&runs, &tasks); err != nil || runs != 1 || tasks != 1 {
		t.Fatalf("replay duplicated rows: runs=%d tasks=%d err=%v", runs, tasks, err)
	}
}

func TestPostgresScheduleImageRunConcurrentReplayKeepsOneTask(t *testing.T) {
	ctx, store, tenant, actor, input, value := newImageSchedulePostgresFixture(t)
	store.db.SetMaxOpenConns(2)
	policy := ModelPolicy{ModelVersion: "fixture-model", PromptVersion: "fixture-prompt", MinConfidence: .8}
	type scheduledImage struct {
		run, task string
		err       error
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan scheduledImage, 2)
	for range 2 {
		go func() {
			ready <- struct{}{}
			<-start
			run, task, err := store.scheduleImageRun(ctx, tenant, actor, input, value, policy)
			results <- scheduledImage{run: run, task: task, err: err}
		}()
	}
	<-ready
	<-ready
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.run == "" || first.task == "" || first.run != second.run || first.task != second.task {
		t.Fatalf("concurrent replay must return the same run and task: first=%+v second=%+v", first, second)
	}
	// 任务完成后允许重放原请求，但换请求键不能为同一条目留下未关联的新任务。
	if _, err := store.db.ExecContext(ctx, `UPDATE scoring_run_item SET processing_status='succeeded' WHERE tenant_id=$1 AND scoring_run_id=$2`, tenant, input.ScoringRunID); err != nil {
		t.Fatal(err)
	}
	run, task, err := store.scheduleImageRun(ctx, tenant, actor, input, value, policy)
	if err != nil || run != first.run || task != first.task {
		t.Fatalf("finished replay changed the binding: run=%q task=%q err=%v", run, task, err)
	}
	input.IdempotencyKey = uuid.NewString()
	if _, _, err := store.scheduleImageRun(ctx, tenant, actor, input, value, policy); !errors.Is(err, ErrWorkerResultVersionConflict) {
		t.Fatalf("already linked item accepted a different request: %v", err)
	}
	var runs, tasks int
	var bound bool
	err = store.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM subjective_grading_run),
 (SELECT count(*) FROM agent_worker_task),
 EXISTS(SELECT 1 FROM scoring_run_item WHERE subjective_run_id=$1::uuid AND runtime_task_id=$2::uuid AND processing_status='succeeded')`, first.run, first.task).Scan(&runs, &tasks, &bound)
	if err != nil || runs != 1 || tasks != 1 || !bound {
		t.Fatalf("unexpected persisted result: runs=%d tasks=%d bound=%v err=%v", runs, tasks, bound, err)
	}
}

func TestPostgresScheduleImageRunRejectsInactiveOrChangedItem(t *testing.T) {
	for _, test := range []struct {
		name, change string
		want         error
	}{
		{"cancelling", `UPDATE scoring_run SET status='cancelling'`, ErrBatchCancelled},
		{"cancelled", `UPDATE scoring_run SET status='cancelled'`, ErrBatchCancelled},
		{"blocked meanwhile", `UPDATE scoring_run_item SET processing_status='blocked'`, ErrWorkerResultVersionConflict},
		{"superseded", `UPDATE scoring_run_item SET processing_status='superseded'`, ErrWorkerResultVersionConflict},
		{"new registration", `UPDATE answer_segment SET registration_run_id=gen_random_uuid()`, ErrWorkerResultVersionConflict},
		{"new snapshot", `UPDATE scoring_run_item SET assessment_snapshot_id=gen_random_uuid()`, ErrWorkerResultVersionConflict},
		{"new observation", `UPDATE scoring_run_item SET observation_json='{"fixture":false}'`, ErrWorkerResultVersionConflict},
		{"observation reset", `UPDATE scoring_run_item SET observation_json=NULL`, ErrWorkerResultVersionConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, store, tenant, actor, input, value := newImageSchedulePostgresFixture(t)
			// 请求继续使用原输入，模拟协调器读出候选条目后、实际入队前，状态或来源已改变。
			if _, err := store.db.ExecContext(ctx, test.change); err != nil {
				t.Fatal(err)
			}
			run, task, err := store.scheduleImageRun(ctx, tenant, actor, input, value, ModelPolicy{ModelVersion: "fixture-model", PromptVersion: "fixture-prompt", MinConfidence: .8})
			if !errors.Is(err, test.want) || run != "" || task != "" {
				t.Fatalf("stale schedule returned run=%q task=%q err=%v, want %v", run, task, err, test.want)
			}
			var runs, tasks, bindings int
			err = store.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM subjective_grading_run),
 (SELECT count(*) FROM agent_worker_task),
 (SELECT count(*) FROM scoring_run_item WHERE subjective_run_id IS NOT NULL OR runtime_task_id IS NOT NULL OR revision<>1)`).Scan(&runs, &tasks, &bindings)
			if err != nil || runs != 0 || tasks != 0 || bindings != 0 {
				t.Fatalf("stale request changed persisted state: runs=%d tasks=%d bindings=%d err=%v", runs, tasks, bindings, err)
			}
		})
	}
}

func TestPostgresScheduleImageRunTaskFailureRollsBack(t *testing.T) {
	ctx, store, tenant, actor, input, value := newImageSchedulePostgresFixture(t)
	// 强制后半段 runtime 插入失败，验证前面成功的图像运行也随事务撤销。
	if _, err := store.db.ExecContext(ctx, `ALTER TABLE agent_worker_task ADD CONSTRAINT fixture_reject_task CHECK (false)`); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.scheduleImageRun(ctx, tenant, actor, input, value, ModelPolicy{ModelVersion: "fixture-model", PromptVersion: "fixture-prompt", MinConfidence: .8})
	if err == nil || !strings.Contains(err.Error(), "fixture_reject_task") {
		t.Fatalf("did not reach injected task failure: %v", err)
	}
	var runs, tasks, bindings int
	if err = store.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM subjective_grading_run),(SELECT count(*) FROM agent_worker_task),(SELECT count(*) FROM scoring_run_item WHERE subjective_run_id IS NOT NULL OR runtime_task_id IS NOT NULL)`).Scan(&runs, &tasks, &bindings); err != nil || runs != 0 || tasks != 0 || bindings != 0 {
		t.Fatalf("partial image schedule survived rollback: runs=%d tasks=%d bindings=%d err=%v", runs, tasks, bindings, err)
	}
}

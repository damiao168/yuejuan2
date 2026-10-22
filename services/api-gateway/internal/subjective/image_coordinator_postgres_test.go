package subjective

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
	mustExec(`INSERT INTO scoring_run_item(tenant_id,scoring_run_id,answer_segment_id,question_id,input_version_id,assessment_snapshot_id,route,observation_json) VALUES($1,$2,$3,$4,$5,$6,'image_ai','{"fixture":true}')`, tenant, runID, segment, question, inputID, snapshot)
	value := serviceTestContext()
	value.SegmentID, value.Question.ID, value.AssessmentSnapshot.ID = segment, question, snapshot
	value.InputVersionID, value.AnswerVersion, value.CropSHA256 = inputID, inputID, hash
	input := grading.ScoringAIInput{ScoringRunID: runID, SegmentID: segment, InputVersionID: inputID, CropSHA256: hash, AssessmentSnapshotID: snapshot, IdempotencyKey: "fixture-" + uuid.NewString()}
	return ctx, NewPostgresStore(database), tenant, actor, input, value
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

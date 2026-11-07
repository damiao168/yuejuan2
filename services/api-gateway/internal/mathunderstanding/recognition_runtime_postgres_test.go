package mathunderstanding

import (
	"context"
	"errors"
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

// 在自有 schema 复制当前表约束，安装 000146 的真实入队触发器；不写 public 或执行全库迁移。
// LIKE 不复制外键和 RLS，本测试只证明识别、触发器入队和任务完成的事务边界。
func TestPostgresRecognitionCompletionRollsBackArtifactsAndTriggeredTasks(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_REVIEW_PG_DSN")
	if dsn == "" {
		t.Skip("EDUGRADE_REVIEW_PG_DSN is not configured")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// DSN 由调用者显式提供；下方只写随机 schema，不能绑定某次审查的端口和库名。
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin := stdlib.OpenDB(*cfg)
	defer admin.Close()
	schema := "root20_math_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean isolated schema: %v", err)
		}
	}()
	cfg.RuntimeParams["search_path"] = schema
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	// 单连接还检查幂等回读使用当前事务，不会在持有连接时到连接池再次取连接。
	db.SetMaxOpenConns(1)
	for _, table := range []string{"tenant", "question", "answer_segment", "exam_question_snapshot", "math_understanding_artifact", "agent_worker_task", "agent_worker_task_attempt", "agent_worker_heartbeat"} {
		if _, err := db.ExecContext(ctx, "CREATE TABLE "+table+" (LIKE public."+table+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000146_story_math11_symbolic_verification_stage.sql"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(raw), "CREATE FUNCTION enqueue_math_verification_for_artifact()")
	end := strings.Index(string(raw), "CREATE FUNCTION enqueue_math_verification_for_correction()")
	if start < 0 || end <= start {
		t.Fatal("production enqueue function is missing")
	}
	if _, err := db.ExecContext(ctx, string(raw[start:end])); err != nil {
		t.Fatal(err)
	}
	restoreMigration, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000226_restore_math_crop_artifact_pointer.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(restoreMigration)); err != nil {
		t.Fatal(err)
	}
	tenant, actor, exam, question, segment, snapshot := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO question(id,tenant_id,exam_id,question_no,question_type,score,sort_order,status) VALUES($1,$2,$3,'1','short_answer',5,1,'draft')`, question, tenant, exam)
	mustExec(`INSERT INTO answer_segment(id,tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status) VALUES($1,$2,$3,$3,$4,'1','{"x":0,"y":0,"width":1,"height":1}','manual','accepted')`, segment, tenant, uuid.NewString(), question)
	mustExec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash) VALUES($1,$2,$3,$4,$5,1,'equation','[]','R2','{"subject_code":"mathematics"}','{}','{}','{}',$6)`, snapshot, tenant, exam, question, uuid.NewString(), strings.Repeat("a", 64))
	artifacts := NewPostgresStore(db)
	runtime := workerruntime.NewPostgresStore(db)
	task, err := runtime.CreateTask(ctx, tenant, actor, workerruntime.CreateTaskInput{
		TaskType: "evidence_verify", QueueName: "math-understanding", SourceType: "answer_segment", SourceID: segment,
		PayloadSchemaVersion: "math-understanding-task-v1", IdempotencyKey: "math:" + segment,
		Payload: map[string]any{"answer_segment_id": segment, "exam_question_snapshot_id": snapshot, "subject_code": "mathematics", "input_hash": "sha256:crop-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim := func(instance string) workerruntime.Task {
		t.Helper()
		items, err := runtime.Claim(ctx, tenant, workerruntime.ClaimInput{QueueName: "math-understanding", WorkerService: "ocr-worker", WorkerInstanceID: instance, Limit: 1, LeaseSeconds: 300})
		if err != nil || len(items) != 1 {
			t.Fatalf("claim items=%d err=%v", len(items), err)
		}
		return items[0]
	}
	first := claim("first")
	input := runtimeFormulaInput()
	input.AnswerSegmentID, input.ExamQuestionSnapshotID = segment, snapshot
	input.SolutionGraph.AnswerSegmentID = segment
	mustExec(`UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, segment, input.InputHash)
	prepared, err := prepareRuntimeArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	// 后续会修改 input 的公式切片；先深拷贝被拒内容，避免测试输入自身别名把两份结果都改成 x=9。
	rejectedInput := cloneInput(prepared)
	completion := recognitionRuntimeCompletion{Task: task, ActorID: actor, Input: prepared, LeaseToken: first.LeaseToken, DurationMS: 12}
	// 在写入产物后、完成任务前强制租约过期，故障与真实入队触发器处于同一事务。
	mustExec(`CREATE FUNCTION expire_recognition_lease_for_test() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE tenant_id=NEW.tenant_id AND source_id=NEW.answer_segment_id AND queue_name='math-understanding'; RETURN NEW; END $$; CREATE TRIGGER zz_expire_recognition_lease_for_test AFTER INSERT ON math_understanding_artifact FOR EACH ROW EXECUTE FUNCTION expire_recognition_lease_for_test()`)
	if _, _, err := artifacts.completeRuntimeArtifact(ctx, completion); !errors.Is(err, workerruntime.ErrLeaseExpired) {
		t.Fatalf("post-insert expired lease: %v", err)
	}
	assertCount := func(query string, want int) {
		t.Helper()
		var got int
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("count got=%d want=%d err=%v query=%s", got, want, err, query)
		}
	}
	assertCount("SELECT count(*) FROM math_understanding_artifact", 0)
	assertCount("SELECT count(*) FROM agent_worker_task WHERE queue_name='math-verification'", 0)
	assertCount("SELECT count(*) FROM agent_worker_task_attempt WHERE status='succeeded'", 0)
	mustExec("DROP TRIGGER zz_expire_recognition_lease_for_test ON math_understanding_artifact")
	mustExec("UPDATE agent_worker_task SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", task.ID)
	second := claim("second")
	// 第一次被拒的公式不能占住输入哈希；新租约可接受另一份正确识别内容。
	input.Formulas[0].RawLatex, input.Formulas[0].CanonicalLatex = "x=9", "x=9"
	input.Formulas[0].AST.Children[1].Value = "9"
	input.Formulas[0].Candidates[0].Latex = "x=9"
	completion.Input, err = prepareRuntimeArtifact(input)
	if err != nil {
		t.Fatal(err)
	}
	completion.LeaseToken = second.LeaseToken
	accepted, verificationID, err := artifacts.completeRuntimeArtifact(ctx, completion)
	if err != nil || accepted.Version != 1 || verificationID == "" {
		t.Fatalf("valid completion artifact=%#v verification=%q err=%v", accepted, verificationID, err)
	}
	replayed, replayVerification, err := artifacts.completeRuntimeArtifact(ctx, completion)
	if err != nil || replayed.ID != accepted.ID || replayVerification != verificationID {
		t.Fatalf("same-result replay artifact=%#v verification=%q err=%v", replayed, replayVerification, err)
	}
	completion.Input = rejectedInput
	if _, _, err := artifacts.completeRuntimeArtifact(ctx, completion); !errors.Is(err, workerruntime.ErrConflict) {
		t.Fatalf("different formula reused success: %v", err)
	}
	completion.Input, _ = prepareRuntimeArtifact(input)
	completion.LeaseToken = first.LeaseToken
	if _, _, err := artifacts.completeRuntimeArtifact(ctx, completion); !errors.Is(err, workerruntime.ErrLeaseMismatch) {
		t.Fatalf("old lease reused success: %v", err)
	}
	assertCount("SELECT count(*) FROM math_understanding_artifact WHERE is_current", 1)
	assertCount("SELECT count(*) FROM math_understanding_artifact", 1)
	assertCount("SELECT count(*) FROM agent_worker_task WHERE queue_name='math-verification'", 1)
	assertCount("SELECT count(*) FROM agent_worker_task_attempt WHERE status='succeeded'", 1)
	stored, err := artifacts.GetLatestArtifact(ctx, tenant, segment)
	if err != nil || stored.Formulas[0].CanonicalLatex != "x=9" {
		t.Fatalf("accepted result changed: %#v err=%v", stored, err)
	}
	if _, err := artifacts.GetLatestArtifact(ctx, uuid.NewString(), segment); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign tenant result: %v", err)
	}
	// 新裁切先完成后，旧任务即使拿着当时有效的成功租约重放，也不能重新激活旧哈希。
	newInput := cloneInput(input)
	newInput.InputHash = "sha256:crop-2"
	for index := range newInput.Blocks {
		newInput.Blocks[index].SourceImageHash = newInput.InputHash
	}
	newInput, err = prepareRuntimeArtifact(newInput)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(`UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, segment, newInput.InputHash)
	newCurrent, err := artifacts.CreateArtifact(ctx, tenant, newInput)
	if err != nil {
		t.Fatal(err)
	}
	// 另一个更旧哈希的任务此前从未写过产物；它迟到完成才是原问题的覆盖入口。
	neverAccepted := cloneInput(completion.Input)
	neverAccepted.InputHash = "sha256:never-accepted-old-crop"
	for index := range neverAccepted.Blocks {
		neverAccepted.Blocks[index].SourceImageHash = neverAccepted.InputHash
	}
	oldTask, err := runtime.CreateTask(ctx, tenant, actor, workerruntime.CreateTaskInput{
		TaskType: "evidence_verify", QueueName: "math-understanding", SourceType: "answer_segment", SourceID: segment,
		PayloadSchemaVersion: "math-understanding-task-v1", IdempotencyKey: "old-math:" + segment,
		Payload: map[string]any{"answer_segment_id": segment, "exam_question_snapshot_id": snapshot, "subject_code": "mathematics", "input_hash": neverAccepted.InputHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldClaim := claim("late-old-crop")
	if _, _, err := artifacts.completeRuntimeArtifact(ctx, recognitionRuntimeCompletion{Task: oldTask, ActorID: actor, Input: neverAccepted, LeaseToken: oldClaim.LeaseToken}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("never-accepted stale task replaced current: %v", err)
	}
	completion.LeaseToken = second.LeaseToken
	if _, _, err := artifacts.completeRuntimeArtifact(ctx, completion); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale completion replaced current crop: %v", err)
	}
	if _, err := artifacts.CreateArtifact(ctx, tenant, input); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("direct stale artifact replay: %v", err)
	}
	currentAfterReplay, err := artifacts.GetLatestArtifact(ctx, tenant, segment)
	if err != nil || currentAfterReplay.ID != newCurrent.ID || currentAfterReplay.InputHash != newInput.InputHash {
		t.Fatalf("new crop pointer lost: %+v %v", currentAfterReplay, err)
	}
	// 无需重跑被去重的识别任务：恢复原裁切就恢复其证据，且不能改动实体裁切文件。
	cropFile := uuid.NewString()
	mustExec(`UPDATE answer_segment SET crop_sha256=$2,crop_file_asset_id=$3,processing_status='completed' WHERE id=$1`, segment, input.InputHash, cropFile)
	restored, err := artifacts.GetLatestArtifact(ctx, tenant, segment)
	if err != nil || restored.InputHash != input.InputHash || restored.ID != stored.ID {
		t.Fatalf("return to recognized crop did not restore pointer: %+v %v", restored, err)
	}
	verified, err := artifacts.CreateDerivedArtifact(ctx, tenant, restored.ID, 0, restored.CreateArtifactInput, map[string]any{"status": "verified"})
	if err != nil || verified.Version <= newCurrent.Version {
		t.Fatalf("restored crop reused an occupied version: %+v %v", verified, err)
	}
	mustExec(`UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, segment, newInput.InputHash)
	mustExec(`UPDATE answer_segment SET crop_sha256=$2 WHERE id=$1`, segment, input.InputHash)
	if _, err := artifacts.CreateArtifact(ctx, tenant, input); err != nil {
		t.Fatalf("matching recognition replay: %v", err)
	}
	latest, err := artifacts.GetLatestArtifact(ctx, tenant, segment)
	if err != nil || latest.ID != verified.ID || latest.Stage != "verified" {
		t.Fatalf("crop restore or replay lost verified evidence: %+v %v", latest, err)
	}
	var actualHash, actualFile string
	if err := db.QueryRowContext(ctx, `SELECT crop_sha256,crop_file_asset_id::text FROM answer_segment WHERE id=$1`, segment).Scan(&actualHash, &actualFile); err != nil || actualHash != input.InputHash || actualFile != cropFile {
		t.Fatalf("artifact restoration modified entity image: %q %q %v", actualHash, actualFile, err)
	}
	// 升级演练：同一哈希下已有新版冻结快照的产物，旧 current 却仍指向旧快照；正式回填段也必须识别这类存量。
	newSnapshot := uuid.NewString()
	mustExec(`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version)
SELECT $2,tenant_id,exam_id,question_id,subject_profile_id,subject_profile_version,archetype_code,allowed_evidence_types,risk_tier,profile_snapshot_json,archetype_snapshot_json,rubric_snapshot_json,scoring_policy_snapshot_json,content_hash,snapshot_version+1 FROM exam_question_snapshot WHERE id=$1`, snapshot, newSnapshot)
	newSnapshotInput := cloneInput(input)
	newSnapshotInput.ExamQuestionSnapshotID = newSnapshot
	newSnapshotInput, err = prepareRuntimeArtifact(newSnapshotInput)
	if err != nil {
		t.Fatal(err)
	}
	newSnapshotArtifact, err := artifacts.CreateArtifact(ctx, tenant, newSnapshotInput)
	if err != nil || newSnapshotArtifact.ID == restored.ID {
		t.Fatalf("same hash reused evidence from an obsolete frozen snapshot: %+v %v", newSnapshotArtifact, err)
	}
	mustExec(`UPDATE math_understanding_artifact SET is_current=false WHERE tenant_id=$1 AND answer_segment_id=$2 AND is_current`, tenant, segment)
	mustExec(`UPDATE math_understanding_artifact SET is_current=true WHERE id=$1`, verified.ID)
	backfill := strings.LastIndex(string(restoreMigration), "\nDO $$")
	if backfill < 0 {
		t.Fatal("formal 000226 backfill block not found")
	}
	mustExec(string(restoreMigration[backfill:]))
	upgraded, err := artifacts.GetLatestArtifact(ctx, tenant, segment)
	if err != nil || upgraded.ID != newSnapshotArtifact.ID || upgraded.ExamQuestionSnapshotID != newSnapshot {
		t.Fatalf("formal backfill kept same-hash stale snapshot pointer: %+v %v", upgraded, err)
	}
	if _, err := artifacts.CreateArtifact(ctx, tenant, input); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("late old-snapshot writer replaced restored current evidence: %v", err)
	}
	if db.Stats().InUse != 0 {
		t.Fatal("single connection did not return to the pool")
	}
}

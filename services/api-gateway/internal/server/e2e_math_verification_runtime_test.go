package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"github.com/google/uuid"
)

// 使用生产运行时结构与 MATH-11 迁移进行真实 PostgreSQL 来源适配器回归测试，
// 与完整评分和认证流程隔离。
func TestPostgresMathVerificationRuntimeAtomicActivation(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL evidence")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
CREATE TABLE tenant(id uuid PRIMARY KEY, status text DEFAULT 'active', deleted_at timestamptz);
CREATE TABLE app_user(id uuid PRIMARY KEY, tenant_id uuid NOT NULL, UNIQUE(tenant_id,id));
CREATE TABLE answer_segment(id uuid PRIMARY KEY,tenant_id uuid NOT NULL);
CREATE TABLE math_understanding_artifact(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid NOT NULL,subject_code text NOT NULL,
 answer_segment_id uuid NOT NULL,exam_question_snapshot_id uuid NOT NULL,version bigint NOT NULL,
 input_hash text NOT NULL,engine_version text NOT NULL,is_current boolean NOT NULL,
 blocks_json jsonb NOT NULL,formulas_json jsonb NOT NULL,relations_json jsonb NOT NULL,
 solution_graph_json jsonb NOT NULL,verifications_json jsonb NOT NULL,rubric_evidence_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(tenant_id,id),UNIQUE(tenant_id,answer_segment_id,version));
CREATE UNIQUE INDEX current_math_artifact ON math_understanding_artifact(tenant_id,answer_segment_id) WHERE is_current;
CREATE TABLE math_understanding_correction(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid NOT NULL,artifact_id uuid NOT NULL,
 answer_segment_id uuid NOT NULL,revision bigint NOT NULL,operations_json jsonb NOT NULL,
 corrected_contract_json jsonb NOT NULL,reason text NOT NULL DEFAULT '',created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),UNIQUE(tenant_id,artifact_id,revision));`)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []string{"000023_story051_agent_worker_runtime.sql", "000146_story_math11_symbolic_verification_stage.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "migrations", migration))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(raw)); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}
	_, err = db.ExecContext(ctx, `ALTER TABLE agent_worker_task ADD COLUMN progress jsonb NOT NULL DEFAULT '{}', ADD COLUMN revision bigint NOT NULL DEFAULT 1, ADD COLUMN paper_import_run_id uuid;
CREATE TRIGGER immutable_math BEFORE UPDATE ON math_understanding_artifact FOR EACH ROW EXECUTE FUNCTION reject_math_understanding_content_update();`)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := mathunderstanding.NewPostgresStore(db)
	corrections := mathunderstanding.NewPostgresCorrectionStore(db, artifacts)
	runtime := workerruntime.NewPostgresStore(db)
	handler := mathunderstanding.NewHandler(artifacts, corrections, nil, nil, nil).WithRuntime(runtime)
	fixture := mathVerificationRuntimeFixture{DB: db, Artifacts: artifacts, Corrections: corrections, Runtime: runtime, Handler: handler}
	for _, scenario := range []string{"invalid_lease", "success", "new_correction"} {
		t.Run(scenario, func(t *testing.T) {
			e2eRunMathVerificationActivationScenario(t, ctx, fixture, scenario)
		})
	}
}

type mathVerificationRuntimeFixture struct {
	DB          *sql.DB
	Artifacts   *mathunderstanding.PostgresStore
	Corrections *mathunderstanding.PostgresCorrectionStore
	Runtime     *workerruntime.PostgresStore
	Handler     *mathunderstanding.Handler
}

type mathVerificationActivationScenario struct {
	TenantID, ActorID, SegmentID, SnapshotID, ParentID string
	Contract                                           mathunderstanding.CreateArtifactInput
}

type mathVerificationRuntimeOutcome struct {
	Current  mathunderstanding.Artifact
	Task     workerruntime.Task
	Recorder *httptest.ResponseRecorder
}

// 原三个子测试分别保留无效租约、正常激活和新人工修订，逐个创建独立租户后运行原回调。
func e2eRunMathVerificationActivationScenario(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, scenario string) {
	t.Helper()
	db, artifacts, corrections, runtime, handler := fixture.DB, fixture.Artifacts, fixture.Corrections, fixture.Runtime, fixture.Handler
	state, encode := e2ePrepareMathVerificationActivation(t, ctx, fixture)
	tenantID, actorID, segmentID, parentID, contract := state.TenantID, state.ActorID, state.SegmentID, state.ParentID, state.Contract
	tasks, err := runtime.Claim(ctx, tenantID, workerruntime.ClaimInput{QueueName: "math-verification", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("claim=%#v err=%v", tasks, err)
	}
	task := tasks[0]
	lease := task.LeaseToken
	if scenario == "invalid_lease" {
		lease = "expired-lease"
	}
	if scenario == "new_correction" {
		_, err = corrections.CreateCorrection(ctx, tenantID, parentID, actorID, mathunderstanding.CreateCorrectionInput{ExpectedArtifactVersion: 1, CorrectedContract: contract, Operations: []mathunderstanding.CorrectionOperation{{Type: "correct_formula", TargetID: "f1"}}})
		if err != nil {
			t.Fatal(err)
		}
		var queued int
		if err = db.QueryRowContext(ctx, `SELECT count(*) FROM agent_worker_task WHERE tenant_id=$1::uuid AND payload->>'correction_revision'='1'`, tenantID).Scan(&queued); err != nil || queued != 1 {
			t.Fatalf("correction verification task count=%d err=%v", queued, err)
		}
	}
	check := mathunderstanding.MathVerification{ID: "solve-f1", FormulaID: "f1", StepID: "s1", Kind: "constraint", Status: "verified", ReasonCode: "solution_set_computed", Domain: "real", Engine: "sympy", EngineVersion: "1.14.0", RulesetVersion: "v1", Confidence: 1}
	body := encode(map[string]any{"lease_token": lease, "duration_ms": 1, "artifact_id": parentID, "artifact_version": 1, "correction_revision": 0, "verifications": []mathunderstanding.MathVerification{check}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-verification/tasks/"+task.ID+"/complete", bytes.NewReader(body))
	req.SetPathValue("taskId", task.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: actorID, TenantID: tenantID}))
	rec := httptest.NewRecorder()
	handler.CompleteVerificationRuntimeTask(rec, req)
	current, err := artifacts.GetLatestArtifact(ctx, tenantID, segmentID)
	if err != nil {
		t.Fatal(err)
	}
	storedTask, err := runtime.Get(ctx, tenantID, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	e2eAssertMathVerificationActivationOutcome(t, ctx, fixture, state, mathVerificationRuntimeOutcome{Current: current, Task: storedTask, Recorder: rec}, scenario)
}

// 同一场景先建立租户、答案和不可变识别产物；原 JSON 编码闭包继续用于随后的完成回调。
func e2ePrepareMathVerificationActivation(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture) (mathVerificationActivationScenario, func(any) []byte) {
	t.Helper()
	db := fixture.DB
	tenantID, actorID, segmentID, snapshotID, parentID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenant(id) VALUES($1::uuid)`, []any{tenantID}},
		{`INSERT INTO app_user(id,tenant_id) VALUES($1::uuid,$2::uuid)`, []any{actorID, tenantID}},
		{`INSERT INTO answer_segment(id,tenant_id) VALUES($1::uuid,$2::uuid)`, []any{segmentID, tenantID}},
	} {
		if _, err := db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	contract := mathunderstanding.CreateArtifactInput{
		SubjectCode: "mathematics", AnswerSegmentID: segmentID, ExamQuestionSnapshotID: snapshotID, InputHash: "sha256:synthetic-crop", EngineVersion: "fixture-v1",
		Blocks:        []mathunderstanding.MathAnswerBlock{{ID: "b1", Kind: "formula", Status: "active", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2}, RecognitionEngine: "fixture", RecognitionVersion: "v1", RecognitionConfidence: .9, StructureConfidence: .9, SourceImageHash: "sha256:synthetic-crop"}},
		Formulas:      []mathunderstanding.FormulaArtifact{{ID: "f1", BlockID: "b1", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2}, CanonicalLatex: "x=2", RecognitionEngine: "fixture", RecognitionVersion: "v1", ParserVersion: "v1", ParseStatus: "parsed", Confidence: .9}},
		SolutionGraph: mathunderstanding.SolutionGraph{ID: "g1", AnswerSegmentID: segmentID, BuilderVersion: "v1", FormulaModelVersion: "v1", OverallConfidence: .9, Steps: []mathunderstanding.SolutionStep{{ID: "s1", OrderHint: 1, BlockIDs: []string{"b1"}, FormulaIDs: []string{"f1"}, Confidence: .9}}},
		Verifications: []mathunderstanding.MathVerification{}, RubricEvidence: []mathunderstanding.RubricEvidence{}, Relations: []mathunderstanding.SpatialRelation{},
	}
	encode := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	_, err := db.ExecContext(ctx, `INSERT INTO math_understanding_artifact(id,tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,is_current,blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json)
VALUES($1::uuid,$2::uuid,'mathematics',$3::uuid,$4::uuid,1,$5,'fixture-v1',true,$6::jsonb,$7::jsonb,'[]',$8::jsonb,'[]','[]')`, parentID, tenantID, segmentID, snapshotID, contract.InputHash, encode(contract.Blocks), encode(contract.Formulas), encode(contract.SolutionGraph))
	if err != nil {
		t.Fatal(err)
	}
	return mathVerificationActivationScenario{TenantID: tenantID, ActorID: actorID, SegmentID: segmentID, SnapshotID: snapshotID, ParentID: parentID, Contract: contract}, encode
}

// 回调后无效租约保持 leased，新修订使旧回调 superseded，只有正常场景激活新的验证产物。
func e2eAssertMathVerificationActivationOutcome(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, state mathVerificationActivationScenario, outcome mathVerificationRuntimeOutcome, scenario string) {
	t.Helper()
	current, storedTask, rec, parentID := outcome.Current, outcome.Task, outcome.Recorder, state.ParentID
	if scenario == "invalid_lease" {
		if rec.Code != http.StatusConflict || current.ID != parentID || storedTask.Status != workerruntime.StatusLeased {
			t.Fatalf("invalid lease mutated evidence: code=%d current=%#v task=%#v body=%s", rec.Code, current, storedTask, rec.Body.String())
		}
	} else if scenario == "new_correction" {
		if rec.Code != http.StatusOK || current.ID != parentID || storedTask.Result["superseded"] != true {
			t.Fatalf("stale correction overwritten: code=%d current=%#v task=%#v body=%s", rec.Code, current, storedTask, rec.Body.String())
		}
	} else {
		e2eAssertActivatedMathVerificationHistory(t, ctx, fixture, state, outcome)
	}

}

// 激活只替换当前指针，原识别产物仍非 current 且不补写验证内容，保持历史证据不可变。
func e2eAssertActivatedMathVerificationHistory(t *testing.T, ctx context.Context, fixture mathVerificationRuntimeFixture, state mathVerificationActivationScenario, outcome mathVerificationRuntimeOutcome) {
	t.Helper()
	artifacts, tenantID, parentID := fixture.Artifacts, state.TenantID, state.ParentID
	current, storedTask, rec := outcome.Current, outcome.Task, outcome.Recorder
	if rec.Code != http.StatusOK || current.Stage != "verified" || current.ParentArtifactID != parentID || storedTask.Status != workerruntime.StatusSucceeded {
		t.Fatalf("verification not activated: code=%d current=%#v task=%#v body=%s", rec.Code, current, storedTask, rec.Body.String())
	}
	parent, err := artifacts.GetArtifact(ctx, tenantID, parentID)
	if err != nil || parent.IsCurrent || len(parent.Verifications) != 0 {
		t.Fatalf("recognition history mutated: %#v %v", parent, err)
	}

}

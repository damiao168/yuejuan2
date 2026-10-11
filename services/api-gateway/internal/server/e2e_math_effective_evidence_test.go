package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"github.com/google/uuid"
)

// 这是使用隔离数据库与生产存储的真实 PostgreSQL 查询和锁回归测试，
// 不覆盖完整迁移或评分流程。
func TestPostgresMathEffectiveEvidenceProjection(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for real PostgreSQL evidence")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
CREATE TABLE math_understanding_artifact (
 id uuid PRIMARY KEY, tenant_id uuid NOT NULL, subject_code text NOT NULL,
 answer_segment_id uuid NOT NULL, exam_question_snapshot_id uuid NOT NULL,
 version bigint NOT NULL, input_hash text NOT NULL, engine_version text NOT NULL,
 is_current boolean NOT NULL, blocks_json jsonb NOT NULL, formulas_json jsonb NOT NULL,
 relations_json jsonb NOT NULL, solution_graph_json jsonb NOT NULL,
 verifications_json jsonb NOT NULL, rubric_evidence_json jsonb NOT NULL,
 stage text NOT NULL DEFAULT 'recognition', parent_artifact_id uuid,
 correction_revision bigint NOT NULL DEFAULT 0, quality_summary_json jsonb NOT NULL DEFAULT '{}',
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE math_understanding_correction (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL,
 artifact_id uuid NOT NULL, answer_segment_id uuid NOT NULL, revision bigint NOT NULL,
 operations_json jsonb NOT NULL, corrected_contract_json jsonb NOT NULL,
 reason text NOT NULL DEFAULT '', created_by uuid NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,artifact_id,revision)
);`)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, segmentID, snapshotID, artifactID, actorID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	contract := mathunderstanding.CreateArtifactInput{
		SubjectCode: "mathematics", AnswerSegmentID: segmentID, ExamQuestionSnapshotID: snapshotID,
		InputHash: "sha256:synthetic-math-crop", EngineVersion: "fixture-v1",
		Blocks: []mathunderstanding.MathAnswerBlock{{
			ID: "block-1", Kind: "text", Status: "active", Text: "base recognition",
			BoundingBox:       mathunderstanding.BoundingBox{X: .1, Y: .2, Width: .5, Height: .2},
			RecognitionEngine: "fixture", RecognitionVersion: "v1", RecognitionConfidence: .9,
			StructureConfidence: .9, SourceImageHash: "sha256:synthetic-math-crop",
		}},
		SolutionGraph: mathunderstanding.SolutionGraph{
			ID: "graph-1", AnswerSegmentID: segmentID, BuilderVersion: "fixture-v1",
			FormulaModelVersion: "text-only", OverallConfidence: .9,
			Steps: []mathunderstanding.SolutionStep{{ID: "step-1", OrderHint: 1, BlockIDs: []string{"block-1"}, Confidence: .9}},
		},
	}
	encode := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	_, err = db.ExecContext(ctx, `INSERT INTO math_understanding_artifact(id,tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,is_current,blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json)
VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,1,$6,$7,true,$8::jsonb,$9::jsonb,$10::jsonb,$11::jsonb,$12::jsonb,$13::jsonb)`,
		artifactID, tenantID, contract.SubjectCode, segmentID, snapshotID, contract.InputHash, contract.EngineVersion,
		encode(contract.Blocks), encode(contract.Formulas), encode(contract.Relations), encode(contract.SolutionGraph), encode(contract.Verifications), encode(contract.RubricEvidence))
	if err != nil {
		t.Fatal(err)
	}
	artifacts := mathunderstanding.NewPostgresStore(db)
	corrections := mathunderstanding.NewPostgresCorrectionStore(db, artifacts)
	effective, err := mathunderstanding.ResolveEffectiveArtifact(ctx, artifacts, corrections, tenantID, segmentID)
	if err != nil || effective.Corrected || mathunderstanding.ValidateCreateArtifact(effective.EffectiveContract) != nil {
		t.Fatalf("invalid base projection: %#v %v", effective, err)
	}
	revision := int64(0)
	contract.SolutionGraph.Steps[0].NormalizedText = "teacher corrected step"
	input := mathunderstanding.CreateCorrectionInput{
		ExpectedArtifactVersion: 1, ExpectedCorrectionRevision: &revision, CorrectedContract: contract,
		Operations: []mathunderstanding.CorrectionOperation{{Type: "move_step", TargetID: "step-1", Payload: map[string]any{"to": 1}}},
	}
	if _, err = corrections.CreateCorrection(ctx, tenantID, artifactID, actorID, input); err != nil {
		t.Fatal(err)
	}
	if _, err = corrections.CreateCorrection(ctx, tenantID, artifactID, actorID, input); !errors.Is(err, mathunderstanding.ErrRevisionConflict) {
		t.Fatalf("stale revision accepted: %v", err)
	}
	revision = 1
	assertConcurrentMathCorrectionRevision(t, ctx, corrections, tenantID, artifactID, actorID, input)
	assertMathCorrectionHistoryProjection(t, ctx, db, artifacts, corrections, mathCorrectionHistoryFixture{TenantID: tenantID, SegmentID: segmentID, ArtifactID: artifactID, ActorID: actorID, Contract: contract, Input: input}, encode)
}

type mathCorrectionHistoryFixture struct {
	TenantID, SegmentID, ArtifactID, ActorID string
	Contract                                 mathunderstanding.CreateArtifactInput
	Input                                    mathunderstanding.CreateCorrectionInput
}

// 历史列表最多返回 500 条，最新有效修订仍须读到第 503 条；随后检验租户隔离和过时产物拒绝。
func assertMathCorrectionHistoryProjection(t *testing.T, ctx context.Context, db *sql.DB, artifacts *mathunderstanding.PostgresStore, corrections *mathunderstanding.PostgresCorrectionStore, fixture mathCorrectionHistoryFixture, encode func(any) []byte) {
	t.Helper()
	tenantID, segmentID, artifactID, actorID := fixture.TenantID, fixture.SegmentID, fixture.ArtifactID, fixture.ActorID
	contract, input := fixture.Contract, fixture.Input
	contract.SolutionGraph.Steps[0].NormalizedText = "latest correction beyond history cap"
	_, err := db.ExecContext(ctx, `INSERT INTO math_understanding_correction(tenant_id,artifact_id,answer_segment_id,revision,operations_json,corrected_contract_json,created_by)
SELECT $1::uuid,$2::uuid,$3::uuid,r,$4::jsonb,$5::jsonb,$6::uuid FROM generate_series(3,503) AS r`, tenantID, artifactID, segmentID, encode(input.Operations), encode(contract), actorID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := corrections.ListCorrections(ctx, tenantID, artifactID)
	if err != nil || len(history) != 500 {
		t.Fatalf("bounded history: %d %v", len(history), err)
	}
	effective, err := mathunderstanding.ResolveEffectiveArtifact(ctx, artifacts, corrections, tenantID, segmentID)
	if err != nil || effective.CorrectionRevision != 503 || effective.EffectiveContract.SolutionGraph.Steps[0].NormalizedText != "latest correction beyond history cap" {
		t.Fatalf("latest projection fell back to capped history: %#v %v", effective, err)
	}
	if effective.BaseArtifact.SolutionGraph.Steps[0].NormalizedText != "" {
		t.Fatal("projection changed immutable base graph")
	}
	if _, err = corrections.GetLatestCorrection(ctx, uuid.NewString(), artifactID); !errors.Is(err, mathunderstanding.ErrNotFound) {
		t.Fatalf("cross-tenant correction leaked: %v", err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE math_understanding_artifact SET is_current=false WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, artifactID); err != nil {
		t.Fatal(err)
	}
	input.ExpectedCorrectionRevision = nil // 旧版客户端遇到过时产物也必须失败。
	if _, err = corrections.CreateCorrection(ctx, tenantID, artifactID, actorID, input); !errors.Is(err, mathunderstanding.ErrRevisionConflict) {
		t.Fatalf("obsolete artifact correction accepted: %v", err)
	}
}

// 两个请求共用预期修订号，等待全部写入结束后必须只有一次提交和一次版本冲突。
func assertConcurrentMathCorrectionRevision(t *testing.T, ctx context.Context, corrections *mathunderstanding.PostgresCorrectionStore, tenantID, artifactID, actorID string, input mathunderstanding.CreateCorrectionInput) {
	t.Helper()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, createErr := corrections.CreateCorrection(ctx, tenantID, artifactID, actorID, input)
			results <- createErr
		}()
	}
	wg.Wait()
	close(results)
	accepted, conflicted := 0, 0
	for createErr := range results {
		switch {
		case createErr == nil:
			accepted++
		case errors.Is(createErr, mathunderstanding.ErrRevisionConflict):
			conflicted++
		default:
			t.Fatalf("unexpected concurrent correction result: %v", createErr)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("PostgreSQL lock allowed %d edits / %d conflicts", accepted, conflicted)
	}
}

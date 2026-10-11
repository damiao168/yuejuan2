package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/mathunderstanding"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"github.com/google/uuid"
)

type mathScoringAssignment struct{ allowed bool }

func (s *mathScoringAssignment) HasActiveAssignment(_ context.Context, _, _, _ string) (bool, error) {
	return s.allowed, nil
}

// 在隔离数据库中使用真实生产来源和 handler 查询。
// 此测试不覆盖完整认证、迁移升级或教师最终提交端到端流程。
func TestPostgresMathRubricScoreUsesOnlyFrozenExamRubric(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	ctx := context.Background()
	_, err := db.ExecContext(ctx, `
CREATE TABLE exam(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,deleted_at timestamptz);
CREATE TABLE question(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,exam_id uuid NOT NULL,deleted_at timestamptz);
CREATE TABLE answer_segment(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,question_id uuid NOT NULL,crop_sha256 text,deleted_at timestamptz);
CREATE TABLE exam_question_snapshot(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,exam_id uuid NOT NULL,question_id uuid NOT NULL,profile_snapshot_json jsonb NOT NULL,rubric_snapshot_json jsonb NOT NULL);
CREATE TABLE question_rubric(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,question_id uuid NOT NULL,points jsonb NOT NULL);
CREATE TABLE ai_grade(id uuid PRIMARY KEY);
CREATE TABLE final_grade(id uuid PRIMARY KEY);
CREATE TABLE math_understanding_artifact(
 id uuid PRIMARY KEY,tenant_id uuid NOT NULL,subject_code text NOT NULL,answer_segment_id uuid NOT NULL,exam_question_snapshot_id uuid NOT NULL,
 version bigint NOT NULL,input_hash text NOT NULL,engine_version text NOT NULL,is_current boolean NOT NULL,stage text NOT NULL,parent_artifact_id uuid,
 correction_revision bigint NOT NULL DEFAULT 0,quality_summary_json jsonb NOT NULL DEFAULT '{}',created_at timestamptz NOT NULL DEFAULT now(),
 blocks_json jsonb NOT NULL,formulas_json jsonb NOT NULL,relations_json jsonb NOT NULL,solution_graph_json jsonb NOT NULL,verifications_json jsonb NOT NULL,rubric_evidence_json jsonb NOT NULL);
CREATE TABLE math_understanding_correction(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),tenant_id uuid NOT NULL,artifact_id uuid NOT NULL,answer_segment_id uuid NOT NULL,revision bigint NOT NULL,
 operations_json jsonb NOT NULL,corrected_contract_json jsonb NOT NULL,reason text NOT NULL DEFAULT '',created_by uuid NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,artifact_id,revision));`)
	if err != nil {
		t.Fatal(err)
	}
	tenant, exam, question, segment, snapshot, artifact, actor := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	encode := func(value any) []byte {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	cropHash := strings.Repeat("a", 64)
	// 最小旧版结构夹具省略 ID/version，正常生产快照则包含它们。
	// 这种情况下仍须通过快照身份和哈希绑定。
	frozen := paper.Rubric{Status: "locked", MaxScore: 2, Points: []paper.RubricPoint{{ID: "P1", Score: 2, Description: "final result", EvidenceRequirements: []paper.EvidenceRequirement{{Type: "final_result", Target: "x=2"}}}}}
	seedFrozenMathRubricRows(t, ctx, db, mathRubricSnapshotIdentity{Tenant: tenant, Exam: exam, Question: question, Segment: segment, Snapshot: snapshot, CropHash: cropHash}, frozen, encode)
	contract := mathunderstanding.CreateArtifactInput{
		SubjectCode: "mathematics", AnswerSegmentID: segment, ExamQuestionSnapshotID: snapshot, InputHash: "sha256:" + cropHash, EngineVersion: "fixture-v1",
		Blocks:        []mathunderstanding.MathAnswerBlock{{ID: "b1", Kind: "formula", Status: "active", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2}, RecognitionEngine: "fixture", RecognitionVersion: "v1", RecognitionConfidence: .9, StructureConfidence: .9, SourceImageHash: "sha256:" + cropHash}},
		Formulas:      []mathunderstanding.FormulaArtifact{{ID: "f1", BlockID: "b1", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2}, CanonicalLatex: "x=2", RecognitionEngine: "fixture", RecognitionVersion: "v1", ParserVersion: "v1", ParseStatus: "parsed", Confidence: .9, AST: &mathunderstanding.FormulaAST{Kind: "equation", Children: []mathunderstanding.FormulaAST{{Kind: "symbol", Value: "x"}, {Kind: "number", Value: "2"}}}}},
		SolutionGraph: mathunderstanding.SolutionGraph{ID: "g1", AnswerSegmentID: segment, BuilderVersion: "v1", FormulaModelVersion: "v1", OverallConfidence: .9, Steps: []mathunderstanding.SolutionStep{{ID: "s1", Kind: "conclusion", BlockIDs: []string{"b1"}, FormulaIDs: []string{"f1"}, Confidence: .9}}},
		Verifications: []mathunderstanding.MathVerification{{ID: "syntax-1", StepID: "s1", FormulaID: "f1", Kind: "syntax", Status: "verified", ReasonCode: "ast_well_formed", Domain: "real", Engine: "fixture", EngineVersion: "v1", RulesetVersion: "v1", Confidence: 1}},
	}
	_, err = db.ExecContext(ctx, `INSERT INTO math_understanding_artifact(id,tenant_id,subject_code,answer_segment_id,exam_question_snapshot_id,version,input_hash,engine_version,is_current,stage,blocks_json,formulas_json,relations_json,solution_graph_json,verifications_json,rubric_evidence_json)
VALUES($1,$2,'mathematics',$3,$4,2,$5,'fixture-v1',true,'verified',$6,$7,'[]',$8,$9,'[]')`, artifact, tenant, segment, snapshot, contract.InputHash, encode(contract.Blocks), encode(contract.Formulas), encode(contract.SolutionGraph), encode(contract.Verifications))
	if err != nil {
		t.Fatal(err)
	}
	store := mathunderstanding.NewPostgresStore(db)
	corrections := mathunderstanding.NewPostgresCorrectionStore(db, store)
	lookup := &mathScoringAssignment{allowed: true}
	handler := mathunderstanding.NewHandler(store, corrections, nil, lookup, nil)
	get := func(tenantID string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("segmentId", segment)
		r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: actor, TenantID: tenantID}))
		w := httptest.NewRecorder()
		handler.GetRubricScore(w, r)
		return w
	}
	assertScore := func(w *httptest.ResponseRecorder) mathunderstanding.RubricScore {
		t.Helper()
		var result mathunderstanding.RubricScore
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
		return result
	}
	first := assertScore(get(tenant))
	assertFrozenRubricPreviewAuthority(t, first)
	preview := mathRubricPreviewScenario{Tenant: tenant, Segment: segment, Artifact: artifact, Actor: actor,
		Contract: contract, Lookup: lookup, Get: get, AssertScore: assertScore, First: first}
	e2eAssertLiveRubricAndScopeFenced(t, ctx, db, store, preview)
	e2eAssertPendingMathCorrectionHasNoGradeWrites(t, ctx, db, corrections, preview)
}

type mathRubricPreviewScenario struct {
	Tenant, Segment, Artifact, Actor string
	Contract                         mathunderstanding.CreateArtifactInput
	Lookup                           *mathScoringAssignment
	Get                              func(string) *httptest.ResponseRecorder
	AssertScore                      func(*httptest.ResponseRecorder) mathunderstanding.RubricScore
	First                            mathunderstanding.RubricScore
}

// 活动量规变化不得影响冻结得分；跨租户、未派发及错误快照身份仍分别拒绝。
func e2eAssertLiveRubricAndScopeFenced(t *testing.T, ctx context.Context, db *sql.DB, store *mathunderstanding.PostgresStore, preview mathRubricPreviewScenario) {
	t.Helper()
	tenant, segment, lookup, get, assertScore, first := preview.Tenant, preview.Segment, preview.Lookup, preview.Get, preview.AssertScore, preview.First

	if _, err := db.ExecContext(ctx, `UPDATE question_rubric SET points='[{"id":"P1","score":12345}]'`); err != nil {
		t.Fatal(err)
	}
	second := assertScore(get(tenant))
	if first.RubricSnapshotHash != second.RubricSnapshotHash || second.VerifiedScore != 2 {
		t.Fatal("live rubric mutation changed frozen scoring")
	}
	if w := get(uuid.NewString()); w.Code != http.StatusNotFound {
		t.Fatalf("cross tenant leaked preview: %d", w.Code)
	}
	lookup.allowed = false
	if w := get(tenant); w.Code != http.StatusForbidden {
		t.Fatalf("unassigned preview: %d", w.Code)
	}
	lookup.allowed = true
	if _, err := store.LoadFrozenRubric(ctx, tenant, segment, uuid.NewString()); !errors.Is(err, mathunderstanding.ErrNotFound) {
		t.Fatalf("foreign snapshot accepted: %v", err)
	}
}

// 未验证的新人工修订不能复用旧验证得分；预览整个过程中不得写入 AI 或最终成绩链。
func e2eAssertPendingMathCorrectionHasNoGradeWrites(t *testing.T, ctx context.Context, db *sql.DB, corrections *mathunderstanding.PostgresCorrectionStore, preview mathRubricPreviewScenario) {
	t.Helper()
	tenant, artifact, actor, contract, get, assertScore := preview.Tenant, preview.Artifact, preview.Actor, preview.Contract, preview.Get, preview.AssertScore

	_, err := corrections.CreateCorrection(ctx, tenant, artifact, actor, mathunderstanding.CreateCorrectionInput{ExpectedArtifactVersion: 2, Operations: []mathunderstanding.CorrectionOperation{{Type: "move_step", TargetID: "s1"}}, CorrectedContract: contract})
	if err != nil {
		t.Fatal(err)
	}
	pending := assertScore(get(tenant))
	if pending.VerifiedScore != 0 || pending.UnresolvedScore != 2 || pending.SuggestedScore != nil || pending.CorrectionRevision != 1 || len(pending.MissingPoints) != 0 {
		t.Fatalf("pending correction reused old score: %#v", pending)
	}
	var writes int
	if err = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM ai_grade)+(SELECT count(*) FROM final_grade)`).Scan(&writes); err != nil || writes != 0 {
		t.Fatalf("preview wrote grading chain: %d %v", writes, err)
	}

}

type mathRubricSnapshotIdentity struct {
	Tenant, Exam, Question, Segment, Snapshot, CropHash string
}

// 同时保存冻结量规与故意错误的活动量规，后续预览才能区分两种来源。
func seedFrozenMathRubricRows(t *testing.T, ctx context.Context, db *sql.DB, identity mathRubricSnapshotIdentity, frozen paper.Rubric, encode func(any) []byte) {
	t.Helper()
	tenant, exam, question, segment, snapshot, cropHash := identity.Tenant, identity.Exam, identity.Question, identity.Segment, identity.Snapshot, identity.CropHash
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO exam(id,tenant_id) VALUES($1,$2)`, []any{exam, tenant}},
		{`INSERT INTO question(id,tenant_id,exam_id) VALUES($1,$2,$3)`, []any{question, tenant, exam}},
		{`INSERT INTO answer_segment(id,tenant_id,question_id,crop_sha256) VALUES($1,$2,$3,$4)`, []any{segment, tenant, question, cropHash}},
		{`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,profile_snapshot_json,rubric_snapshot_json) VALUES($1,$2,$3,$4,'{"subject_code":"mathematics"}',$5)`, []any{snapshot, tenant, exam, question, encode(frozen)}},
		{`INSERT INTO question_rubric(id,tenant_id,question_id,points) VALUES($1,$2,$3,'[{"id":"P1","score":999}]')`, []any{uuid.NewString(), tenant, question}},
	} {
		if _, err := db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
}

// 预览仍只是人工复核建议：成绩、快照哈希与版本必须全部来自冻结量规。
func assertFrozenRubricPreviewAuthority(t *testing.T, preview mathunderstanding.RubricScore) {
	t.Helper()
	if preview.VerifiedScore != 2 || preview.SuggestedScore == nil || *preview.SuggestedScore != 2 || preview.RubricSnapshotHash == "" || preview.RubricVersion != preview.RubricSnapshotHash || !preview.RequiresHumanReview {
		t.Fatalf("mutable rubric used or authority expanded: %#v", preview)
	}
}

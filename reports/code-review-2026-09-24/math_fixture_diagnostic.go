package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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

// Real production source/handler queries in an isolated database. This is not
// a full authentication, migration-upgrade or teacher final-submit E2E.
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
CREATE TABLE answer_segment(id uuid PRIMARY KEY,tenant_id uuid NOT NULL,question_id uuid NOT NULL,deleted_at timestamptz,crop_sha256 text NOT NULL DEFAULT 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa');
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
	// A minimal legacy-shaped fixture omits ID/version; normal production
	// snapshots include them. Snapshot identity/hash must still bind this case.
	frozen := paper.Rubric{Status: "locked", MaxScore: 2, Points: []paper.RubricPoint{{ID: "P1", Score: 2, Description: "final result", EvidenceRequirements: []paper.EvidenceRequirement{{Type: "final_result", Target: "x=2"}}}}}
	for _, seed := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO exam(id,tenant_id) VALUES($1,$2)`, []any{exam, tenant}},
		{`INSERT INTO question(id,tenant_id,exam_id) VALUES($1,$2,$3)`, []any{question, tenant, exam}},
		{`INSERT INTO answer_segment(id,tenant_id,question_id) VALUES($1,$2,$3)`, []any{segment, tenant, question}},
		{`INSERT INTO exam_question_snapshot(id,tenant_id,exam_id,question_id,profile_snapshot_json,rubric_snapshot_json) VALUES($1,$2,$3,$4,'{"subject_code":"mathematics"}',$5)`, []any{snapshot, tenant, exam, question, encode(frozen)}},
		{`INSERT INTO question_rubric(id,tenant_id,question_id,points) VALUES($1,$2,$3,'[{"id":"P1","score":999}]')`, []any{uuid.NewString(), tenant, question}},
	} {
		if _, err = db.ExecContext(ctx, seed.query, seed.args...); err != nil {
			t.Fatal(err)
		}
	}
	contract := mathunderstanding.CreateArtifactInput{
		SubjectCode: "mathematics", AnswerSegmentID: segment, ExamQuestionSnapshotID: snapshot, InputHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", EngineVersion: "fixture-v1",
		Blocks:        []mathunderstanding.MathAnswerBlock{{ID: "b1", Kind: "formula", Status: "active", BoundingBox: mathunderstanding.BoundingBox{X: .1, Y: .1, Width: .5, Height: .2}, RecognitionEngine: "fixture", RecognitionVersion: "v1", RecognitionConfidence: .9, StructureConfidence: .9, SourceImageHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
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
	if first.VerifiedScore != 2 || first.SuggestedScore == nil || *first.SuggestedScore != 2 || first.RubricSnapshotHash == "" || first.RubricVersion != first.RubricSnapshotHash || !first.RequiresHumanReview {
		t.Fatalf("mutable rubric used or authority expanded: %#v", first)
	}
	if _, err = db.ExecContext(ctx, `UPDATE question_rubric SET points='[{"id":"P1","score":12345}]'`); err != nil {
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
	if _, err = store.LoadFrozenRubric(ctx, tenant, segment, uuid.NewString()); !errors.Is(err, mathunderstanding.ErrNotFound) {
		t.Fatalf("foreign snapshot accepted: %v", err)
	}
	_, err = corrections.CreateCorrection(ctx, tenant, artifact, actor, mathunderstanding.CreateCorrectionInput{ExpectedArtifactVersion: 2, Operations: []mathunderstanding.CorrectionOperation{{Type: "move_step", TargetID: "s1"}}, CorrectedContract: contract})
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

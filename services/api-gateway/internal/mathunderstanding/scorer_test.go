package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func scoringFixture() (FrozenRubric, EffectiveArtifact) {
	input := validInput()
	input.SolutionGraph.Steps[0].Kind = "setup"
	block := input.Blocks[0]
	block.ID = "block-2"
	formula := input.Formulas[0]
	formula.ID = "formula-2"
	formula.BlockID = block.ID
	formula.CanonicalLatex = "2*x=4"
	formula.RawLatex = "2*x=4"
	formula.AST = &FormulaAST{Kind: "equation", Children: []FormulaAST{{Kind: "operator", Value: "*", Children: []FormulaAST{{Kind: "number", Value: "2"}, {Kind: "symbol", Value: "x"}}}, {Kind: "number", Value: "4"}}}
	formula.Candidates = []FormulaCandidate{{Latex: "2*x=4", Engine: "fixture", Version: "v1", Confidence: .8, SyntaxValid: true}}
	input.Blocks = append(input.Blocks, block)
	input.Formulas = append(input.Formulas, formula)
	input.SolutionGraph.Steps = append(input.SolutionGraph.Steps, SolutionStep{ID: "step-2", Kind: "conclusion", BlockIDs: []string{block.ID}, FormulaIDs: []string{formula.ID}, Confidence: .9})
	input.SolutionGraph.Edges = []SolutionEdge{{FromStepID: "step-1", ToStepID: "step-2", Kind: "derives"}}
	syntax := input.Verifications[0]
	syntax.ID = "syntax-2"
	syntax.StepID = "step-2"
	syntax.FormulaID = "formula-2"
	input.Verifications = append(input.Verifications, syntax, MathVerification{ID: "eq-2", StepID: "step-2", FormulaID: "formula-2", Kind: "equivalence", Status: "verified", ReasonCode: "equivalent_transform", Domain: "real", Engine: "sympy", EngineVersion: "1.14.0", RulesetVersion: "v1", Confidence: 1, Details: map[string]any{"from_step_id": "step-1", "from_formula_id": "formula-1"}})
	frozen := FrozenRubric{SnapshotID: "snapshot-1", Rubric: paper.Rubric{ID: "rubric-1", Version: "frozen-v1", Status: "locked", MaxScore: 6, Points: []paper.RubricPoint{
		{ID: "P1", Description: "transformation", Score: 2, EvidenceRequirements: []paper.EvidenceRequirement{{Type: "valid_transformation", Target: "2*x=4"}}},
		{ID: "P2", Description: "result", Score: 2, EvidenceRequirements: []paper.EvidenceRequirement{{Type: "final_result", Target: "2*x=4"}}},
		{ID: "P3", Description: "explain method", Score: 2, EvidenceRequirements: []paper.EvidenceRequirement{{Type: "concept", Target: "factorization"}}},
	}}}
	base := Artifact{ID: "verified-1", TenantID: "tenant-a", AnswerSegmentID: input.AnswerSegmentID, ExamQuestionSnapshotID: input.ExamQuestionSnapshotID, Version: 2, Stage: "verified", IsCurrent: true, InputHash: input.InputHash, EngineVersion: input.EngineVersion, CreateArtifactInput: input}
	return frozen, EffectiveArtifact{BaseArtifact: base, EffectiveContract: input}
}

func TestServerScorePreservesUncertaintyAndFrozenPointAmounts(t *testing.T) {
	frozen, effective := scoringFixture()
	before, _ := json.Marshal(effective)
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil {
		t.Fatal(err)
	}
	if score.VerifiedScore != 4 || score.UnresolvedScore != 2 || score.ScoreRange != (ScoreRange{Min: 4, Max: 6}) || score.SuggestedScore != nil || !score.RequiresHumanReview {
		t.Fatalf("false certain total: %#v", score)
	}
	if len(score.MatchedPoints) != 2 || len(score.MissingPoints) != 0 || score.CriterionDecisions[2].AwardedScore != nil || !score.CriterionDecisions[2].RequiresHumanReview {
		t.Fatalf("uncertainty became missing or zero: %#v", score)
	}
	if !reflect.DeepEqual(score.CriterionDecisions[0].VerificationIDs, []string{"eq-2"}) || score.CriterionDecisions[0].DecisionSource != "symbolic" {
		t.Fatalf("lost symbolic provenance: %#v", score.CriterionDecisions[0])
	}
	projected := cloneInput(effective.EffectiveContract)
	projected.RubricEvidence = score.RubricEvidence
	if err = ValidateCreateArtifact(projected); err != nil {
		t.Fatalf("matcher produced invalid artifact references: %v", err)
	}
	after, _ := json.Marshal(effective)
	if string(before) != string(after) {
		t.Fatal("scoring mutated immutable evidence")
	}
}

func TestMissingChecksAndLegacyEvidenceNeverBecomeFalseZero(t *testing.T) {
	for _, scenario := range []string{"missing_target", "unbound", "solve_only", "wrong_edge", "wrong_source", "low_confidence", "syntax_uncertain", "crossed_out", "scratch", "not_applicable", "ambiguous", "recognition_pending", "correction_pending", "deductions"} {
		t.Run(scenario, func(t *testing.T) {
			frozen, effective := scoringFixture()
			frozen.Rubric.Points = frozen.Rubric.Points[:1]
			frozen.Rubric.MaxScore = 2
			input := &effective.EffectiveContract
			switch scenario {
			case "missing_target":
				frozen.Rubric.Points[0].EvidenceRequirements[0].Target = "x=999"
			case "unbound":
				frozen.Rubric.Points[0].EvidenceRequirements[0].Target = ""
			case "solve_only":
				input.Verifications[2].Kind = "constraint"
				input.Verifications[2].ReasonCode = "solution_set_computed"
			case "wrong_edge":
				input.SolutionGraph.Edges[0].Kind = "next"
			case "wrong_source":
				input.Verifications[2].Details["from_formula_id"] = "formula-2"
			case "low_confidence":
				input.Formulas[1].Confidence = .4
			case "syntax_uncertain":
				input.Verifications[1].Status = "uncertain"
			case "crossed_out":
				input.Blocks[1].Status = "crossed_out"
			case "scratch":
				input.Blocks[1].Status = "scratch_candidate"
			case "not_applicable":
				input.Verifications[2].Status = "not_applicable"
			case "ambiguous":
				check := input.Verifications[2]
				check.ID = "conflict"
				check.Status = "contradicted"
				input.Verifications = append(input.Verifications, check)
			case "recognition_pending":
				effective.BaseArtifact.Stage = "recognition"
			case "correction_pending":
				effective.CorrectionRevision = 1
				effective.Corrected = true
			case "deductions":
				frozen.Rubric.Deductions = []any{map[string]any{"score": 1}}
			}
			// 产物中旧的支持或不支持证据不能作为权威依据。
			input.RubricEvidence[0].RubricCriterionKey = "P1"
			input.RubricEvidence[0].Status = "unsupported"
			score, err := ScoreFrozenRubric(frozen, effective, nil)
			if err != nil {
				t.Fatal(err)
			}
			if score.VerifiedScore != 0 || score.UnresolvedScore != 2 || score.SuggestedScore != nil || score.CriterionDecisions[0].AwardedScore != nil || len(score.MissingPoints) != 0 {
				t.Fatalf("unsafe score for %s: %#v", scenario, score)
			}
		})
	}
}

func TestContradictedCriterionHasExplicitZeroAndOnlyThenMissingPoint(t *testing.T) {
	frozen, effective := scoringFixture()
	frozen.Rubric.Points = frozen.Rubric.Points[:1]
	frozen.Rubric.MaxScore = 2
	effective.EffectiveContract.Verifications[2].Status = "contradicted"
	effective.EffectiveContract.SolutionGraph.OverallConfidence = 0
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil {
		t.Fatal(err)
	}
	decision := score.CriterionDecisions[0]
	if decision.Status != "contradicted" || decision.AwardedScore == nil || *decision.AwardedScore != 0 || score.UnresolvedScore != 0 || score.SuggestedScore == nil || *score.SuggestedScore != 0 || len(score.MissingPoints) != 1 {
		t.Fatalf("explicit contradiction lost: %#v", score)
	}
	if score.RubricEvidence[0].Confidence < .8 {
		t.Fatal("global validity confidence erased locally confident contradiction")
	}
}

func TestOneFrozenPointWithMultipleRequirementsCannotBeAwardedTwice(t *testing.T) {
	frozen, effective := scoringFixture()
	frozen.Rubric.Points = frozen.Rubric.Points[:1]
	frozen.Rubric.MaxScore = 2
	frozen.Rubric.Points[0].EvidenceRequirements = append(frozen.Rubric.Points[0].EvidenceRequirements, paper.EvidenceRequirement{Type: "final_result", Target: "2*x=4"})
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil || score.VerifiedScore != 2 || len(score.CriterionDecisions) != 1 || len(score.MatchedPoints) != 1 {
		t.Fatalf("duplicate point award: %#v %v", score, err)
	}
	frozen.Rubric.Points[0].EvidenceRequirements = append(frozen.Rubric.Points[0].EvidenceRequirements, paper.EvidenceRequirement{Type: "unit", Target: "m"})
	score, err = ScoreFrozenRubric(frozen, effective, nil)
	if err != nil || score.UnresolvedScore != 2 || len(score.MatchedPoints) != 0 || score.SuggestedScore != nil {
		t.Fatalf("ignored conjunctive requirement: %#v %v", score, err)
	}
}

func TestCompositeRequirementsAndAlternativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		minimum int
		status  string
	}{{"any_of", 0, "supported"}, {"all_of", 0, "uncertain"}, {"at_least", 1, "supported"}, {"at_least", 2, "uncertain"}} {
		t.Run(tc.kind+tc.status, func(t *testing.T) {
			frozen, effective := scoringFixture()
			frozen.Rubric.Points = frozen.Rubric.Points[:1]
			frozen.Rubric.MaxScore = 2
			frozen.Rubric.Points[0].EvidenceRequirements = []paper.EvidenceRequirement{{Type: tc.kind, Minimum: tc.minimum, Children: []paper.EvidenceRequirement{{Type: "final_result", Target: "2*x=4"}, {Type: "concept", Target: "factorization"}}}}
			score, err := ScoreFrozenRubric(frozen, effective, nil)
			if err != nil {
				t.Fatal(err)
			}
			if score.CriterionDecisions[0].Status != tc.status {
				t.Fatalf("bad composition: %#v", score)
			}
		})
	}
}

func TestCandidateCannotAwardOrDenyPointsAndRejectsInventedIDs(t *testing.T) {
	for _, status := range []string{"supported", "contradicted", "uncertain", "unsupported", "not_applicable"} {
		frozen, effective := scoringFixture()
		score, err := ScoreFrozenRubric(frozen, effective, []CriterionCandidate{{RubricPointID: "P3", Status: status, EvidenceIDs: []string{"step-2"}}})
		if err != nil || score.CriterionDecisions[2].AwardedScore != nil || score.CriterionDecisions[2].DecisionSource != "model_candidate" || score.VerifiedScore != 4 || len(score.MissingPoints) != 0 {
			t.Fatalf("model became score authority: %#v %v", score, err)
		}
	}
	for _, candidate := range []CriterionCandidate{{RubricPointID: "invented", Status: "supported"}, {RubricPointID: "P3", Status: "supported", EvidenceIDs: []string{"invented"}}} {
		frozen, effective := scoringFixture()
		if _, err := ScoreFrozenRubric(frozen, effective, []CriterionCandidate{candidate}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invented candidate accepted: %v", err)
		}
	}
}

func TestCandidateWireContractRejectsPointScoreAndTotalInjection(t *testing.T) {
	for _, field := range []string{"score", "awarded_score", "suggested_score", "max_score"} {
		raw := []byte(`{"rubric_point_id":"P1","status":"supported","evidence_ids":["step-2"],"` + field + `":999}`)
		r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
		w := httptest.NewRecorder()
		var candidate CriterionCandidate
		if decodeMathJSON(w, r, &candidate) || w.Code != http.StatusBadRequest {
			t.Fatalf("candidate injected %s: %s", field, w.Body.String())
		}
	}
}

func TestFollowThroughAndSelfCorrectionNeedExplicitReviewedPolicy(t *testing.T) {
	for _, scenario := range []string{"contradicted_predecessor", "uncertain_predecessor", "missing_predecessor", "self_correction", "complex_domain", "explicit_constraints"} {
		t.Run(scenario, func(t *testing.T) {
			assertMathFollowThroughNeedsReviewedPolicy(t, scenario)
		})
	}
}

func TestFrozenRubricValidationAndExactDecimalAggregation(t *testing.T) {
	for _, scenario := range []string{"duplicate", "nan", "inf", "negative", "total", "draft", "snapshot", "obsolete", "precision"} {
		t.Run(scenario, func(t *testing.T) {
			frozen, effective := scoringFixture()
			switch scenario {
			case "duplicate":
				frozen.Rubric.Points[1].ID = "P1"
			case "nan":
				frozen.Rubric.Points[0].Score = math.NaN()
			case "inf":
				frozen.Rubric.MaxScore = math.Inf(1)
			case "negative":
				frozen.Rubric.Points[0].Score = -1
			case "total":
				frozen.Rubric.MaxScore = 7
			case "draft":
				frozen.Rubric.Status = "draft"
			case "snapshot":
				frozen.SnapshotID = "other"
			case "obsolete":
				effective.BaseArtifact.IsCurrent = false
			case "precision":
				frozen.Rubric.Points[0].Score = .12345
			}
			if _, err := ScoreFrozenRubric(frozen, effective, nil); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid rubric accepted: %v", err)
			}
		})
	}
	frozen, effective := scoringFixture()
	frozen.Rubric.ID = ""
	frozen.Rubric.Version = ""
	frozen.Rubric.Points = frozen.Rubric.Points[:2]
	frozen.Rubric.Points[0].Score = .1
	frozen.Rubric.Points[1].Score = .2
	frozen.Rubric.MaxScore = .3
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil || score.VerifiedScore != .3 || score.ScoreRange.Max != .3 || score.RubricSnapshotHash == "" || score.RubricVersion != score.RubricSnapshotHash {
		t.Fatalf("decimal drift or missing frozen identity: %#v %v", score, err)
	}
}

type frozenRubricStub struct {
	frozen FrozenRubric
	calls  int
	mutate func()
}

func (s *frozenRubricStub) LoadFrozenRubric(_ context.Context, tenantID, segmentID, snapshotID string) (FrozenRubric, error) {
	s.calls++
	if s.mutate != nil {
		s.mutate()
	}
	return s.frozen, nil
}
func TestRubricScoreAPIIsAssignedTenantScopedReadOnlyAndRevisionBound(t *testing.T) {
	ctx := context.Background()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	frozen, effective := scoringFixture()
	parent, err := artifacts.CreateArtifact(ctx, "tenant-a", effective.EffectiveContract)
	if err != nil {
		t.Fatal(err)
	}
	derived, err := artifacts.CreateDerivedArtifact(ctx, "tenant-a", parent.ID, 0, effective.EffectiveContract, nil)
	if err != nil {
		t.Fatal(err)
	}
	lookup := &assignmentLookupStub{allowed: true}
	source := &frozenRubricStub{frozen: frozen}
	h := NewHandler(artifacts, corrections, nil, lookup, nil).WithFrozenRubrics(source)
	get := func(tenant string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.SetPathValue("segmentId", parent.AnswerSegmentID)
		r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "teacher", TenantID: tenant}))
		w := httptest.NewRecorder()
		h.GetRubricScore(w, r)
		return w
	}
	if w := get("tenant-a"); w.Code != http.StatusOK {
		t.Fatalf("preview failed: %d %s", w.Code, w.Body.String())
	}
	if w := get("tenant-b"); w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant preview: %d", w.Code)
	}
	lookup.allowed = false
	if w := get("tenant-a"); w.Code != http.StatusForbidden || source.calls != 1 {
		t.Fatalf("unassigned rubric accessed: %d calls=%d", w.Code, source.calls)
	}
	lookup.allowed = true
	source.mutate = func() {
		_, err := corrections.CreateCorrection(ctx, "tenant-a", derived.ID, "teacher", CreateCorrectionInput{ExpectedArtifactVersion: derived.Version, Operations: []CorrectionOperation{{Type: "move_step", TargetID: "step-2"}}, CorrectedContract: derived.CreateArtifactInput})
		if err != nil {
			t.Fatal(err)
		}
		source.mutate = nil
	}
	if w := get("tenant-a"); w.Code != http.StatusConflict {
		t.Fatalf("superseded preview returned: %d %s", w.Code, w.Body.String())
	}
	if w := get("tenant-a"); w.Code != http.StatusOK {
		t.Fatalf("corrected pending preview failed: %d %s", w.Code, w.Body.String())
	} else {
		var score RubricScore
		if json.Unmarshal(w.Body.Bytes(), &score) != nil || score.VerifiedScore != 0 || score.UnresolvedScore != 6 || score.CorrectionRevision != 1 {
			t.Fatalf("pending correction reused symbolic score: %s", w.Body.String())
		}
	}
}

func assertMathFollowThroughNeedsReviewedPolicy(t *testing.T, scenario string) {
	t.Helper()
	frozen, effective := scoringFixture()
	frozen.Rubric.Points = frozen.Rubric.Points[:1]
	frozen.Rubric.MaxScore = 2
	input := &effective.EffectiveContract
	switch scenario {
	case "complex_domain":
		input.Verifications[2].Domain = "complex"
	case "explicit_constraints":
		input.Verifications[2].Constraints = []string{"x>0"}
	case "self_correction":
		input.SolutionGraph.Steps = append(input.SolutionGraph.Steps, SolutionStep{ID: "earlier", BlockIDs: []string{"block-1"}, Confidence: .9})
		input.SolutionGraph.Edges = append(input.SolutionGraph.Edges, SolutionEdge{FromStepID: "earlier", ToStepID: "step-1", Kind: "corrects"})
	default:
		input.SolutionGraph.Steps = append(input.SolutionGraph.Steps, SolutionStep{ID: "earlier", BlockIDs: []string{"block-1"}, FormulaIDs: []string{"formula-1"}, Confidence: .9})
		input.SolutionGraph.Edges = append(input.SolutionGraph.Edges, SolutionEdge{FromStepID: "earlier", ToStepID: "step-1", Kind: "derives"})
		if scenario != "missing_predecessor" {
			check := input.Verifications[2]
			check.ID = "earlier-check"
			check.StepID = "step-1"
			check.FormulaID = "formula-1"
			check.Details = map[string]any{"from_step_id": "earlier", "from_formula_id": "formula-1"}
			check.Status = "contradicted"
			if scenario == "uncertain_predecessor" {
				check.Status = "uncertain"
			}
			input.Verifications = append(input.Verifications, check)
		}
	}
	score, err := ScoreFrozenRubric(frozen, effective, nil)
	if err != nil || score.VerifiedScore != 0 || score.UnresolvedScore != 2 || score.CriterionDecisions[0].AwardedScore != nil || len(score.MissingPoints) != 0 {
		t.Fatalf("implicit follow-through credit: %#v %v", score, err)
	}
}
